<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $site */
/** @var list<array<string, mixed>> $addons */
/** @var array<int, array<string, mixed>> $moves */
/** @var list<array<string, mixed>> $plans */
$id = (int) $site['id'];
$errors ??= [];
$old ??= [];
$open = ($old['convert'] ?? '') === '' && $errors !== [];
$addonError = $open ? ($errors['domain'] ?? $errors['system_user'] ?? null) : null;
$canConvert = $this->can(\NimVor\Security\Permissions::USERS_MANAGE) && $this->hasRoute('sites.convert');
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'addons']) ?>
<p class="small text-body-secondary">An addon domain is a separate website for another domain you own, running under this website's account <code><?= e($site['system_user']) ?></code> (its own folder, PHP, SSL, databases…). Point the domain's DNS to this server. Manage each one like any website; delete it from its own Advanced page.</p>

<div class="card" id="addons">
    <div class="card-header nim-toolbar">
        <h2 class="nim-toolbar-title"><i class="bi bi-plus-square me-1"></i>Addon domains <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($addons) ?></span></h2>
        <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#new-addon"><i class="bi bi-plus-lg me-1"></i>Add addon domain</button>
    </div>
    <?= $this->modalStart('new-addon', 'Add addon domain', $open) ?>
        <form class="p-3" method="post" action="<?= e($this->url('sites.addons.store', ['id' => $id])) ?>" data-nim-busy>
            <?= $this->csrfField() ?><input type="hidden" name="type" value="addon">
            <div class="row g-2 align-items-end">
                <div class="col-md-6">
                    <label class="form-label small" for="ad-name">Domain</label>
                    <input class="form-control form-control-sm<?= $addonError !== null ? ' is-invalid' : '' ?>" id="ad-name" name="domain" value="<?= e($open ? (string) ($old['domain'] ?? '') : '') ?>" placeholder="another-domain.com" required autocomplete="off">
                </div>
                <div class="col-md-auto"><button class="btn btn-sm btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Add</button></div>
            </div>
            <?php if ($addonError !== null): ?><div class="text-danger small mt-1"><?= e((string) $addonError) ?></div><?php endif; ?>
            <div class="form-check small mt-2"><input type="hidden" name="www" value="0"><input class="form-check-input" type="checkbox" name="www" value="1" id="ad-www" checked><label class="form-check-label" for="ad-www">Also answer on www.</label></div>
            <div class="form-text">A separate website for another domain you own, under this account. Point the domain's DNS to this server.</div>
        </form>
    <?= $this->modalEnd() ?>
    <?php if ($addons === []): ?>
        <div class="nim-empty-state"><i class="bi bi-plus-square" aria-hidden="true"></i><div class="small">No addon domains yet.</div></div>
    <?php else: ?>
        <div class="table-responsive">
            <table class="table align-middle mb-0 nim-table">
                <thead><tr><th>Domain</th><th>Folder</th><th>Status</th><th>HTTPS</th><th class="text-end">Actions</th></tr></thead>
                <tbody>
                <?php foreach ($addons as $c): ?>
                    <tr>
                        <td class="fw-semibold"><?= e($c['domain']) ?></td>
                        <td class="small"><code><?= e($c['document_root']) ?></code></td>
                        <td class="small"><?= $c['enabled'] ? '<span class="nim-state is-ok">Active</span>' : '<span class="nim-state is-off">Disabled</span>' ?></td>
                        <td class="small"><?= $c['https'] ? '<i class="bi bi-lock-fill text-success"></i> On' : '<span class="text-body-secondary">Off</span>' ?></td>
                        <td class="text-end text-nowrap">
                            <?php if ($c['enabled']): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e(($c['https'] ? 'https://' : 'http://') . $c['domain']) ?>/" target="_blank" rel="noopener noreferrer" title="Visit" aria-label="Visit <?= e($c['domain']) ?>"><i class="bi bi-box-arrow-up-right"></i></a><?php endif; ?>
                            <a class="btn btn-sm btn-outline-primary" href="<?= e($this->url('sites.show', ['id' => (int) $c['id']])) ?>">Manage</a>
                            <?php if ($canConvert): ?><button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#convert-<?= (int) $c['id'] ?>">Convert to account</button><?php endif; ?>
                        </td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
</div>
<?php if ($canConvert): foreach ($addons as $c): $cid = (int) $c['id']; $mv = $moves[$cid] ?? null; $mine = ($old['convert'] ?? '') === (string) $cid && $errors !== [];
    $fv = fn (string $k, string $d = ''): string => $mine ? (string) ($old[$k] ?? $d) : $d;
    $fe = fn (string $k): string => $mine && isset($errors[$k]) ? '<div class="invalid-feedback d-block">' . e((string) $errors[$k]) . '</div>' : '';
    $suggest = substr(preg_replace('/[^a-z0-9]/', '', explode('.', (string) $c['domain'])[0]) ?: 'site', 0, 16); ?>
    <?= $this->modalStart('convert-' . $cid, 'Convert ' . $c['domain'] . ' to an account', $mine) ?>
        <form method="post" action="<?= e($this->url('sites.convert', ['id' => $cid])) ?>" data-nim-busy data-nim-confirm="Move <?= e($c['domain']) ?> to the new account? Its files are re-owned in place; it no longer runs as <?= e($site['system_user']) ?>." data-nim-confirm-word="<?= e($c['domain']) ?>">
            <?= $this->csrfField() ?>
            <p class="small mb-2"><?= e($c['domain']) ?> becomes a hosting account of its own, with a new login and a new system account. Nothing is copied: its folder is re-owned in place.</p>
            <?php if ($mv !== null): ?>
                <ul class="small mb-3">
                    <li>Websites: <?= e(implode(', ', array_column($mv['websites'], 'domain'))) ?></li>
                    <li>Databases: <?= $mv['databases'] === [] ? 'none' : e(implode(', ', $mv['databases'])) ?> (database users that only these use move too)</li>
                    <li>DNS zone: <?= $mv['dns'] ? 'moves' : 'none here' ?> · email domain with its mailboxes: <?= $mv['mail'] ? 'moves' : 'none here' ?></li>
                    <li>Cron jobs: <?= (int) $mv['cron'] ?> · workers: <?= (int) $mv['workers'] ?> · FTP accounts move with their passwords</li>
                </ul>
            <?php endif; ?>
            <div class="row g-2">
                <div class="col-sm-6"><label class="form-label small" for="cv-u-<?= $cid ?>">Username (login and system account)</label><input class="form-control form-control-sm" id="cv-u-<?= $cid ?>" name="username" value="<?= e($fv('username', $suggest)) ?>" required maxlength="31"><?= $fe('username') ?></div>
                <div class="col-sm-6"><label class="form-label small" for="cv-e-<?= $cid ?>">Email</label><input class="form-control form-control-sm" id="cv-e-<?= $cid ?>" type="email" name="email" value="<?= e($fv('email')) ?>" required><?= $fe('email') ?></div>
                <div class="col-sm-6"><label class="form-label small" for="cv-p-<?= $cid ?>">Password</label><input class="form-control form-control-sm" id="cv-p-<?= $cid ?>" type="password" name="password" autocomplete="new-password" required><?= $fe('password') ?></div>
                <div class="col-sm-6"><label class="form-label small" for="cv-pc-<?= $cid ?>">Repeat password</label><input class="form-control form-control-sm" id="cv-pc-<?= $cid ?>" type="password" name="password_confirmation" autocomplete="new-password" required></div>
                <div class="col-12"><label class="form-label small" for="cv-pl-<?= $cid ?>">Hosting plan</label><select class="form-select form-select-sm" id="cv-pl-<?= $cid ?>" name="plan_id"><option value="">No plan</option><?php foreach ($plans as $p): ?><option value="<?= (int) $p['id'] ?>"<?= $fv('plan_id') === (string) $p['id'] ? ' selected' : '' ?>><?= e($p['name']) ?></option><?php endforeach; ?></select><?= $fe('plan_id') ?></div>
            </div>
            <div class="mt-3"><button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Convert to account</button></div>
        </form>
    <?= $this->modalEnd() ?>
<?php endforeach; endif; ?>
