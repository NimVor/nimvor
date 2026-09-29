<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $site */
/** @var list<array<string, mixed>> $subdomains */
/** @var list<array<string, mixed>> $addons */
/** @var bool $hasZone */
$id = (int) $site['id'];
$errors ??= [];
$old ??= [];
// Each form reopens with its own errors (the type field tells them apart).
$openSub = ($old['type'] ?? '') === 'subdomain' && $errors !== [];
$openAddon = ($old['type'] ?? '') === 'addon' && $errors !== [];
$subError = $openSub ? ($errors['subdomain'] ?? $errors['domain'] ?? $errors['system_user'] ?? null) : null;
$addonError = $openAddon ? ($errors['domain'] ?? $errors['system_user'] ?? null) : null;
$canConvert = $this->can(\WhaCP\Security\Permissions::USERS_MANAGE) && $this->hasRoute('sites.convert');
$list = function (array $rows, string $empty, string $icon, bool $convert = false) use ($canConvert): string {
    ob_start(); ?>
    <?php if ($rows === []): ?>
        <div class="wcp-empty-state"><i class="bi bi-<?= e($icon) ?>" aria-hidden="true"></i><div class="small"><?= e($empty) ?></div></div>
    <?php else: ?>
        <div class="table-responsive">
            <table class="table align-middle mb-0 wcp-table">
                <thead><tr><th>Domain</th><th>Folder</th><th>Status</th><th>HTTPS</th><th class="text-end">Actions</th></tr></thead>
                <tbody>
                <?php foreach ($rows as $c): ?>
                    <tr>
                        <td class="fw-semibold"><?= e($c['domain']) ?></td>
                        <td class="small"><code><?= e($c['document_root']) ?></code></td>
                        <td class="small"><?= $c['enabled'] ? '<span class="wcp-state is-ok">Active</span>' : '<span class="wcp-state is-off">Disabled</span>' ?></td>
                        <td class="small"><?= $c['https'] ? '<i class="bi bi-lock-fill text-success"></i> On' : '<span class="text-body-secondary">Off</span>' ?></td>
                        <td class="text-end text-nowrap">
                            <?php if ($c['enabled']): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e(($c['https'] ? 'https://' : 'http://') . $c['domain']) ?>/" target="_blank" rel="noopener noreferrer" title="Visit" aria-label="Visit <?= e($c['domain']) ?>"><i class="bi bi-box-arrow-up-right"></i></a><?php endif; ?>
                            <a class="btn btn-sm btn-outline-primary" href="<?= e($this->url('sites.show', ['id' => (int) $c['id']])) ?>">Manage</a>
                            <?php if ($convert && $canConvert): ?><button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#convert-<?= (int) $c['id'] ?>">Convert to account</button><?php endif; ?>
                        </td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
    <?php return (string) ob_get_clean();
};
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'domains']) ?>
<p class="small text-body-secondary">Subdomains and addon domains are websites of their own (their own folder, PHP, SSL, databases…) that run under this website's account <code><?= e($site['system_user']) ?></code>. Manage each one like any website; delete it from its own Advanced page.</p>

<div class="card mb-3" id="subdomains">
    <div class="card-header wcp-toolbar">
        <h2 class="wcp-toolbar-title"><i class="bi bi-diagram-3 me-1"></i>Subdomains <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($subdomains) ?></span></h2>
        <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#new-subdomain"><i class="bi bi-plus-lg me-1"></i>Create subdomain</button>
    </div>
    <?= $this->modalStart('new-subdomain', 'Create subdomain', $openSub) ?>
        <form class="p-3" method="post" action="<?= e($this->url('sites.domains.store', ['id' => $id])) ?>" data-wcp-busy>
            <?= $this->csrfField() ?><input type="hidden" name="type" value="subdomain">
            <div class="row g-2 align-items-end">
                <div class="col-md-6">
                    <label class="form-label small" for="sd-name">Subdomain</label>
                    <div class="input-group input-group-sm">
                        <input class="form-control<?= $subError !== null ? ' is-invalid' : '' ?>" id="sd-name" name="subdomain" value="<?= e($openSub ? (string) ($old['subdomain'] ?? '') : '') ?>" placeholder="blog" required autocomplete="off">
                        <span class="input-group-text">.<?= e($site['domain']) ?></span>
                    </div>
                </div>
                <div class="col-md-auto"><button class="btn btn-sm btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Create</button></div>
            </div>
            <?php if ($subError !== null): ?><div class="text-danger small mt-1"><?= e((string) $subError) ?></div><?php endif; ?>
            <?php if ($hasZone): ?>
                <div class="form-check small mt-2"><input type="hidden" name="dns" value="0"><input class="form-check-input" type="checkbox" name="dns" value="1" id="sd-dns" checked><label class="form-check-label" for="sd-dns">Add an A record for it to this domain's DNS zone</label></div>
            <?php else: ?>
                <div class="form-text">Point the subdomain to this server in your domain's DNS (an A record).</div>
            <?php endif; ?>
        </form>
    <?= $this->modalEnd() ?>
    <?= $list($subdomains, 'No subdomains yet. A subdomain like blog.' . $site['domain'] . ' can host a separate site.', 'diagram-3') ?>
</div>

<div class="card" id="addons">
    <div class="card-header wcp-toolbar">
        <h2 class="wcp-toolbar-title"><i class="bi bi-plus-square me-1"></i>Addon domains <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($addons) ?></span></h2>
        <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#new-addon"><i class="bi bi-plus-lg me-1"></i>Add addon domain</button>
    </div>
    <?= $this->modalStart('new-addon', 'Add addon domain', $openAddon) ?>
        <form class="p-3" method="post" action="<?= e($this->url('sites.domains.store', ['id' => $id])) ?>" data-wcp-busy>
            <?= $this->csrfField() ?><input type="hidden" name="type" value="addon">
            <div class="row g-2 align-items-end">
                <div class="col-md-6">
                    <label class="form-label small" for="ad-name">Domain</label>
                    <input class="form-control form-control-sm<?= $addonError !== null ? ' is-invalid' : '' ?>" id="ad-name" name="domain" value="<?= e($openAddon ? (string) ($old['domain'] ?? '') : '') ?>" placeholder="another-domain.com" required autocomplete="off">
                </div>
                <div class="col-md-auto"><button class="btn btn-sm btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Add</button></div>
            </div>
            <?php if ($addonError !== null): ?><div class="text-danger small mt-1"><?= e((string) $addonError) ?></div><?php endif; ?>
            <div class="form-check small mt-2"><input type="hidden" name="www" value="0"><input class="form-check-input" type="checkbox" name="www" value="1" id="ad-www" checked><label class="form-check-label" for="ad-www">Also answer on www.</label></div>
            <div class="form-text">A separate website for another domain you own, under this account. Point the domain's DNS to this server.</div>
        </form>
    <?= $this->modalEnd() ?>
    <?= $list($addons, 'No addon domains yet.', 'plus-square', true) ?>
</div>
<?php if ($canConvert): foreach ($addons as $c): $cid = (int) $c['id']; $mv = $moves[$cid] ?? null; $mine = ($old['convert'] ?? '') === (string) $cid && $errors !== [];
    $fv = fn (string $k, string $d = ''): string => $mine ? (string) ($old[$k] ?? $d) : $d;
    $fe = fn (string $k): string => $mine && isset($errors[$k]) ? '<div class="invalid-feedback d-block">' . e((string) $errors[$k]) . '</div>' : '';
    $suggest = substr(preg_replace('/[^a-z0-9]/', '', explode('.', (string) $c['domain'])[0]) ?: 'site', 0, 16); ?>
    <?= $this->modalStart('convert-' . $cid, 'Convert ' . $c['domain'] . ' to an account', $mine) ?>
        <form method="post" action="<?= e($this->url('sites.convert', ['id' => $cid])) ?>" data-wcp-busy data-wcp-confirm="Move <?= e($c['domain']) ?> to the new account? Its files are re-owned in place; it no longer runs as <?= e($site['system_user']) ?>." data-wcp-confirm-word="<?= e($c['domain']) ?>">
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
