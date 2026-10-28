<?php
/** @var \NimVorSite\Core\View $v */
/** @var bool $keyReady */
/** @var bool $writable */
/** @var array<string, bool> $extensions */
/** @var array<string, string> $errors */
/** @var array<string, string> $old */
$v->layout('layouts/plain');
$v->set('title', 'Set up the site');
$suggest = bin2hex(random_bytes(16));
?>
<h1>Set up the NimVor website</h1>
<p class="muted">This page runs once. It creates the database tables and the first administrator, then locks itself.</p>

<?php if (!empty($errors['form'])): ?><div class="alert alert-error" role="alert"><?= e($errors['form']) ?></div><?php endif; ?>

<div class="panel">
    <h2>Before you start</h2>
    <ol class="setup-steps">
        <li><strong>Create an empty database</strong> and a database user with all rights on it only (in NimVor's panel: Databases).</li>
        <li><strong>Create the setup key.</strong> With your host's file manager, create the file <code>storage/setup.key</code> inside the site's folder and put one secret line in it (at least 16 characters). For example: <code><?= e($suggest) ?></code>. This proves you control the site's files; without it nobody can use this page.</li>
        <li>Fill in the form below. When it succeeds, the key file is deleted and this page disappears.</li>
    </ol>
    <ul class="checks">
        <li><?= $v->icon($keyReady ? 'check' : 'info', 18) ?><span>Setup key file: <strong><?= $keyReady ? 'found' : 'not found yet' ?></strong></span></li>
        <li><?= $v->icon($writable ? 'check' : 'info', 18) ?><span>Folders <code>config</code> and <code>storage</code> writable: <strong><?= $writable ? 'yes' : 'no: make them writable for the web server user' ?></strong></span></li>
        <?php foreach ($extensions as $name => $ok): ?>
            <li><?= $v->icon($ok ? 'check' : 'info', 18) ?><span>PHP extension <code><?= e($name) ?></code>: <?= $ok ? 'available' : (in_array($name, ['redis', 'memcached'], true) ? 'not installed (only needed if you choose that cache)' : '<strong>missing</strong>') ?></span></li>
        <?php endforeach; ?>
    </ul>
</div>

<form class="form panel" method="post" action="/setup" novalidate data-once autocomplete="off">
    <?= $v->csrf() ?>
    <h2 class="mb-0">Setup key</h2>
    <div class="field"><label for="setup_key">The line you put in storage/setup.key</label><input class="input" id="setup_key" name="setup_key" type="password" autocomplete="off" required><?= $v->error($errors, 'setup_key') ?></div>

    <h2 class="mb-0">Database (MySQL or MariaDB)</h2>
    <div class="grid grid-2">
        <div class="field"><label for="db_host">Host</label><input class="input" id="db_host" name="db_host" value="<?= e($old['db_host'] ?? '127.0.0.1') ?>" required><?= $v->error($errors, 'db_host') ?></div>
        <div class="field"><label for="db_port">Port</label><input class="input" id="db_port" name="db_port" value="<?= e($old['db_port'] ?? '3306') ?>"><?= $v->error($errors, 'db_port') ?></div>
        <div class="field"><label for="db_name">Database name</label><input class="input" id="db_name" name="db_name" value="<?= e($old['db_name'] ?? '') ?>" required><?= $v->error($errors, 'db_name') ?></div>
        <div class="field"><label for="db_user">Database user</label><input class="input" id="db_user" name="db_user" value="<?= e($old['db_user'] ?? '') ?>" required><?= $v->error($errors, 'db_user') ?></div>
    </div>
    <div class="field"><label for="db_pass">Database password</label><input class="input" id="db_pass" name="db_pass" type="password" autocomplete="off"></div>

    <h2 class="mb-0">Cache</h2>
    <p class="muted mb-0">Sessions, rate limits and cached pages. "Files" needs nothing else.</p>
    <div class="grid grid-2">
        <div class="field"><label for="cache_driver">Use</label>
            <select class="select" id="cache_driver" name="cache_driver">
                <?php foreach (['file' => 'Files (no extra software)', 'redis' => 'Redis', 'memcached' => 'Memcached'] as $k => $label): ?>
                    <option value="<?= e($k) ?>"<?= ($old['cache_driver'] ?? 'file') === $k ? ' selected' : '' ?>><?= e($label) ?></option>
                <?php endforeach; ?>
            </select><?= $v->error($errors, 'cache_driver') ?></div>
        <div class="field"><label for="cache_host">Cache host (Redis or Memcached)</label><input class="input" id="cache_host" name="cache_host" value="<?= e($old['cache_host'] ?? '127.0.0.1') ?>"></div>
        <div class="field"><label for="cache_port">Cache port</label><input class="input" id="cache_port" name="cache_port" value="<?= e($old['cache_port'] ?? '6379') ?>"><div class="hint">Redis 6379, Memcached 11211.</div></div>
        <div class="field"><label for="cache_password">Redis password (if any)</label><input class="input" id="cache_password" name="cache_password" type="password" autocomplete="off"></div>
    </div>

    <h2 class="mb-0">The site</h2>
    <div class="field"><label for="site_url">Public address</label><input class="input" id="site_url" name="site_url" value="<?= e($old['site_url'] ?? '') ?>" placeholder="https://nimvor.com" required><?= $v->error($errors, 'site_url') ?></div>

    <h2 class="mb-0">First administrator</h2>
    <div class="field"><label for="admin_name">Name</label><input class="input" id="admin_name" name="admin_name" value="<?= e($old['admin_name'] ?? '') ?>" required><?= $v->error($errors, 'admin_name') ?></div>
    <div class="field"><label for="admin_email">Email</label><input class="input" id="admin_email" type="email" name="admin_email" value="<?= e($old['admin_email'] ?? '') ?>" required><?= $v->error($errors, 'admin_email') ?></div>
    <div class="field"><label for="admin_password">Password</label><input class="input" id="admin_password" type="password" name="admin_password" autocomplete="new-password" required><div class="hint">At least 12 characters.</div><?= $v->error($errors, 'admin_password') ?></div>
    <div class="field"><label for="admin_password_confirm">Repeat the password</label><input class="input" id="admin_password_confirm" type="password" name="admin_password_confirm" autocomplete="new-password" required><?= $v->error($errors, 'admin_password_confirm') ?></div>
    <div><button class="btn btn-primary" type="submit">Set up the site</button></div>
</form>
