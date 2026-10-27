<?php
/**
 * Site → Runtime: the runtime version and app for a Node.js/Python website.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var string $appType
 * @var array<string, mixed>|null $app
 * @var list<string> $versions
 */
$id = (int) $site['id'];
$current = (string) ($site['app_runtime'] ?? '');
$prefix = $appType === 'python' ? 'python' : 'node';
$label = $appType === 'python' ? 'Python' : 'Node.js';
$route = $appType === 'python' ? 'sites.python.update' : 'sites.nodejs.update';
$workersUrl = \NimVor\View\SiteMenu::url($this, $site, 'workers');
$port = preg_match('/:(\d+)/', (string) ($site['proxy_pass'] ?? ''), $m) ? $m[1] : '';
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => $appType === 'python' ? 'python' : 'nodejs']) ?>
<p class="small text-body-secondary">Your <strong><?= e($label) ?></strong> app runs as a background service on a NimVor-managed runtime, and <?= e($site['domain']) ?> is served in front of it<?= $port !== '' ? ' (it must listen on port <code>' . e($port) . '</code>)' : '' ?>.</p>

<div class="row g-3">
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-<?= $appType === 'python' ? 'filetype-py' : 'hexagon' ?> me-1" aria-hidden="true"></i><?= e($label) ?> version</div>
            <form class="card-body" method="post" action="<?= e($this->url($route, ['id' => $id])) ?>" data-nim-busy>
                <?= $this->csrfField() ?>
                <label class="form-label small" for="rt-ver">Runtime</label>
                <div class="input-group">
                    <select class="form-select" id="rt-ver" name="app_runtime">
                        <?php if ($versions === []): ?><option value="<?= e($current) ?>"><?= e($current !== '' ? $current : 'None installed') ?></option><?php endif; ?>
                        <?php foreach ($versions as $r): ?><option value="<?= e($r) ?>"<?= $r === $current ? ' selected' : '' ?>><?= e($label) ?> <?= e(substr($r, strlen($prefix) + 1)) ?></option><?php endforeach; ?>
                    </select>
                    <button class="btn btn-primary" type="submit">Change</button>
                </div>
                <div class="form-text">Changing the version restarts your app on it. More versions are installed by your provider under Runtimes.</div>
            </form>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-gear-wide-connected me-1" aria-hidden="true"></i>Your app</div>
            <div class="card-body">
                <?php if ($app !== null): ?>
                    <dl class="row small mb-2">
                        <dt class="col-4">Command</dt><dd class="col-8 font-monospace text-truncate"><?= e((string) ($app['command'] ?? '')) ?></dd>
                        <dt class="col-4">Runtime</dt><dd class="col-8"><?= e((string) ($app['runtime'] ?? 'system')) ?></dd>
                        <dt class="col-4">State</dt><dd class="col-8"><?= !empty($app['enabled']) ? '<span class="nim-state is-ok">enabled</span>' : '<span class="nim-state is-off">disabled</span>' ?></dd>
                    </dl>
                    <?php if ($workersUrl !== null): ?><a class="btn btn-sm btn-outline-primary" href="<?= e($workersUrl) ?>">Edit command, env &amp; restart</a><?php endif; ?>
                <?php else: ?>
                    <p class="small text-body-secondary">Your app isn't running yet. Start it as a background process with your start command — <code>PORT</code><?= $port !== '' ? ' is <code>' . e($port) . '</code>' : '' ?> and the <?= e($label) ?> runtime are already set.</p>
                    <?php if ($workersUrl !== null): ?><a class="btn btn-sm btn-primary" href="<?= e($workersUrl) ?>">Start your app (Workers)</a><?php endif; ?>
                <?php endif; ?>
            </div>
        </div>
    </div>
</div>
