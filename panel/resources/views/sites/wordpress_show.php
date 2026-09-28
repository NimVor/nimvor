<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $site
 * @var array<string, mixed> $install
 * @var array<string, mixed>|null $info
 * @var list<array{level: string, title: string, detail: string, fix: ?string}> $checks
 * @var bool $canManage
 * @var bool $others
 * @var list<string>|null $verify
 */
use WhaCP\Services\WordPressService as WP;

$id = (int) $site['id'];
$wid = (int) $install['id'];
$manage = $this->url('sites.wordpress.manage', ['id' => $id, 'wp' => $wid]);
$suspended = !empty($site['account_suspended']);
$can = $canManage && !$suspended;
// A small form that runs one operation.
$op = function (string $op, array $items, string $label, string $class = 'btn-outline-secondary', string $confirm = '', string $value = '') use ($manage, $can): string {
    if (!$can) {
        return '';
    }
    $h = '<form class="d-inline" method="post" action="' . e($manage) . '" data-wcp-busy' . ($confirm !== '' ? ' data-wcp-confirm="' . e($confirm) . '"' : '') . '>' . $this->csrfField()
        . '<input type="hidden" name="op" value="' . e($op) . '">';
    foreach ($items as $it) {
        $h .= '<input type="hidden" name="items[]" value="' . e($it) . '">';
    }
    if ($value !== '') {
        $h .= '<input type="hidden" name="value" value="' . e($value) . '">';
    }
    return $h . '<button class="btn btn-sm ' . e($class) . '"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>' . e($label) . '</button></form>';
};
$where = $install['path'] === '' ? '/' : $install['path'];
$backups = $this->hasRoute('sites.backups') ? $this->url('sites.backups', ['id' => $id]) : null;
$levels = ['danger' => ['bi-x-octagon-fill text-danger', 'is-error'], 'warning' => ['bi-exclamation-triangle-fill text-warning', 'is-warn'], 'info' => ['bi-info-circle text-primary', '']];
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'wordpress']) ?>

<div class="d-flex flex-wrap align-items-center gap-2 mb-3">
    <div class="me-auto">
        <span class="fw-semibold"><i class="bi bi-wordpress me-1" aria-hidden="true"></i><?= e($info['title'] ?? 'WordPress') ?></span>
        <span class="text-body-secondary small ms-1">in <code><?= e($where) ?></code><?php if ($install['url'] !== null): ?> · <a href="<?= e($install['url']) ?>/" target="_blank" rel="noopener noreferrer"><?= e($install['url']) ?></a><?php endif; ?></span>
    </div>
    <?php if ($others): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.wordpress', ['id' => $id])) ?>?list=1"><i class="bi bi-list-ul me-1" aria-hidden="true"></i>All installations</a><?php endif; ?>
    <form method="post" action="<?= e($this->url('sites.wordpress.check', ['id' => $id, 'wp' => $wid])) ?>" data-wcp-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><i class="bi bi-arrow-repeat me-1" aria-hidden="true"></i>Check for updates</button></form>
    <?php if ($can && $info !== null && ($info['admins'] ?? []) !== []): ?>
        <form class="d-flex gap-1" method="post" action="<?= e($this->url('sites.wordpress.login', ['id' => $id, 'wp' => $wid])) ?>" target="_blank">
            <?= $this->csrfField() ?>
            <?php if (count($info['admins']) > 1): ?>
                <select class="form-select form-select-sm" name="admin_id" aria-label="Administrator">
                    <?php foreach ($info['admins'] as $a): ?><option value="<?= (int) $a['id'] ?>"><?= e($a['login']) ?></option><?php endforeach; ?>
                </select>
            <?php else: ?><input type="hidden" name="admin_id" value="<?= (int) $info['admins'][0]['id'] ?>"><?php endif; ?>
            <button class="btn btn-sm btn-primary text-nowrap" title="Opens WordPress signed in, with a one-time link that works for a minute"><i class="bi bi-box-arrow-in-right me-1" aria-hidden="true"></i>Log in to WordPress</button>
        </form>
    <?php endif; ?>
</div>

<?php if ($suspended): ?><div class="alert alert-warning small">The hosting account is suspended: WordPress can be viewed but not changed.</div><?php endif; ?>
<?php if ($agentError !== null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError]) ?>
    <?php return; ?>
<?php endif; ?>
<?php if ($verify !== null): ?>
    <div class="wcp-panel mb-3">
        <div class="fw-semibold mb-1">Core file verification</div>
        <?php if ($verify === []): ?>
            <div class="small text-success"><i class="bi bi-check-circle me-1" aria-hidden="true"></i>Every WordPress core file matches the official release.</div>
        <?php else: ?>
            <div class="small mb-1">These files differ from the official WordPress <?= e($info['version'] ?? '') ?> release or should not be there. If you did not change them, the site may be compromised: reinstall WordPress core (Update WordPress) and scan for malware.</div>
            <ul class="small font-monospace mb-0"><?php foreach ($verify as $f): ?><li><?= e($f) ?></li><?php endforeach; ?></ul>
        <?php endif; ?>
    </div>
<?php endif; ?>

<div class="row g-3 mb-3">
    <div class="col-lg-5">
        <div class="wcp-panel h-100 small">
            <div class="fw-semibold mb-2">Overview</div>
            <dl class="row mb-0">
                <dt class="col-5 fw-normal wcp-muted">WordPress</dt>
                <dd class="col-7"><?= e($info['version'] ?? '?') ?>
                    <?php if (WP::outdated($install)): ?><span class="wcp-state is-warn"><?= e($install['latest']) ?> available</span><?php elseif (($info['core']['latest'] ?? '') !== ''): ?><span class="wcp-state is-ok">Up to date</span><?php endif; ?></dd>
                <dt class="col-5 fw-normal wcp-muted">PHP</dt>
                <dd class="col-7"><?= e($site['php_version']) ?><?php if (($info['core']['php_needed'] ?? '') !== ''): ?> <span class="text-body-secondary">(latest WordPress needs <?= e($info['core']['php_needed']) ?>+)</span><?php endif; ?></dd>
                <dt class="col-5 fw-normal wcp-muted">Address</dt>
                <dd class="col-7 text-break"><?= e($info['siteurl'] ?? '') ?><?php if (!$site['https']): ?> <span class="wcp-state is-warn">no HTTPS</span><?php endif; ?></dd>
                <dt class="col-5 fw-normal wcp-muted">Database</dt>
                <dd class="col-7"><code><?= e($info['db']['name'] ?? '') ?></code> · <?= (int) ($info['db']['tables'] ?? 0) ?> tables · <?= e(format_bytes((int) ($info['db']['bytes'] ?? 0))) ?><div class="text-body-secondary">prefix <code><?= e($info['db']['prefix'] ?? '') ?></code>, <?= e($info['mysql'] ?? '') ?></div></dd>
                <dt class="col-5 fw-normal wcp-muted">Administrators</dt>
                <dd class="col-7"><?= e(implode(', ', array_map(static fn ($a): string => (string) $a['login'], (array) ($info['admins'] ?? [])))) ?: '—' ?></dd>
                <?php if (!empty($info['multisite'])): ?><dt class="col-5 fw-normal wcp-muted">Network</dt><dd class="col-7">Multisite</dd><?php endif; ?>
                <dt class="col-5 fw-normal wcp-muted">Updates checked</dt>
                <dd class="col-7"><?= ($info['core']['latest'] ?? '') === '' ? '<span class="wcp-state is-warn">WordPress.org not reachable</span>' : e(gmdate('Y-m-d H:i', (int) $info['core']['checked_at'])) . ' UTC' ?></dd>
            </dl>
            <?php if ($backups !== null): ?><div class="mt-2 text-body-secondary">Before large updates, <a href="<?= e($backups) ?>#backup-now">back up the website</a>.</div><?php endif; ?>
        </div>
    </div>
    <div class="col-lg-7">
        <div class="wcp-panel h-100">
            <div class="fw-semibold mb-2">Security and health <?php $n = count(array_filter($checks, static fn ($c): bool => $c['level'] !== 'info')); ?><span class="badge rounded-pill <?= $n > 0 ? 'text-bg-warning' : 'text-bg-success' ?> fw-normal"><?= $n > 0 ? $n . ' to review' : 'OK' ?></span></div>
            <?php if ($checks === []): ?>
                <div class="small text-success"><i class="bi bi-check-circle me-1" aria-hidden="true"></i>Nothing to review.</div>
            <?php endif; ?>
            <ul class="list-unstyled mb-0 small">
                <?php foreach ($checks as $c): ?>
                    <li class="d-flex gap-2 py-1 border-bottom">
                        <i class="bi <?= e($levels[$c['level']][0]) ?> mt-1" aria-hidden="true"></i>
                        <div class="me-auto"><div class="fw-medium"><?= e($c['title']) ?></div><div class="text-body-secondary"><?= e($c['detail']) ?></div></div>
                        <?php if ($c['fix'] !== null): ?><div class="text-nowrap"><?= $op($c['fix'], [], $c['fix'] === 'core_update' ? 'Update' : 'Fix', 'btn-outline-primary', $c['fix'] === 'core_update' ? 'Update WordPress core now? Back the website up first.' : '') ?></div><?php endif; ?>
                    </li>
                <?php endforeach; ?>
            </ul>
        </div>
    </div>
</div>

<?php
$plugins = (array) ($info['plugins'] ?? []);
$themes = (array) ($info['themes'] ?? []);
$pUpd = array_values(array_filter($plugins, static fn ($p): bool => ($p['update'] ?? '') !== ''));
$tUpd = array_values(array_filter($themes, static fn ($t): bool => ($t['update'] ?? '') !== ''));
?>
<div class="wcp-panel mb-3" data-wcp-dt id="plugins">
    <div class="wcp-actionbar">
        <div class="fw-semibold">Plugins <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($plugins) ?></span></div>
        <div class="wcp-actionbar-end">
            <div class="wcp-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search plugins" data-wcp-dt-search><i class="bi bi-search" aria-hidden="true"></i></div>
            <?php if ($pUpd !== []): ?><?= $op('plugin_update', [], 'Update all (' . count($pUpd) . ')', 'btn-primary', 'Update ' . count($pUpd) . ' plugin(s) now?') ?><?php endif; ?>
        </div>
    </div>
    <?php if ($plugins === []): ?>
        <div class="wcp-empty-state"><i class="bi bi-plug" aria-hidden="true"></i><div>No plugins installed.</div></div>
    <?php else: ?>
    <div class="wcp-dtable table-responsive"><table class="table align-middle">
        <thead><tr><th>Plugin</th><th>Version</th><th>Status</th><th>Automatic updates</th><th></th></tr></thead>
        <tbody>
        <?php foreach ($plugins as $p): $name = (string) $p['name']; ?>
            <tr>
                <td><div class="fw-medium"><?= e($p['title'] ?: $name) ?></div><div class="small text-body-secondary font-monospace"><?= e($name) ?><?= !empty($p['must_use']) ? ' · must-use' : '' ?></div></td>
                <td class="small"><?= e($p['version']) ?><?php if ($p['update'] !== ''): ?><div><span class="wcp-state is-warn"><?= e($p['update']) ?> available</span></div><?php endif; ?></td>
                <td><?= !empty($p['active']) ? '<span class="wcp-state is-ok">Active</span>' : '<span class="wcp-state is-off">Inactive</span>' ?></td>
                <td class="small"><?php if (empty($p['must_use'])): ?><?= !empty($p['auto_update']) ? 'On ' . $op('plugin_auto_off', [$name], 'Turn off', 'btn-link p-0') : 'Off ' . $op('plugin_auto_on', [$name], 'Turn on', 'btn-link p-0') ?><?php endif; ?></td>
                <td class="text-end text-nowrap">
                    <?php if (empty($p['must_use'])): ?>
                        <?php if ($p['update'] !== ''): ?><?= $op('plugin_update', [$name], 'Update', 'btn-outline-primary') ?><?php endif; ?>
                        <?= !empty($p['active'])
                            ? $op('plugin_deactivate', [$name], 'Deactivate', 'btn-outline-secondary', 'Deactivate ' . ($p['title'] ?: $name) . '? The site stops using it at once.')
                            : $op('plugin_activate', [$name], 'Activate', 'btn-outline-success') ?>
                    <?php endif; ?>
                </td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table></div>
    <?php endif; ?>
</div>

<div class="wcp-panel mb-3" data-wcp-dt id="themes">
    <div class="wcp-actionbar">
        <div class="fw-semibold">Themes <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($themes) ?></span></div>
        <div class="wcp-actionbar-end"><?php if ($tUpd !== []): ?><?= $op('theme_update', [], 'Update all (' . count($tUpd) . ')', 'btn-primary', 'Update ' . count($tUpd) . ' theme(s) now?') ?><?php endif; ?></div>
    </div>
    <div class="wcp-dtable table-responsive"><table class="table align-middle">
        <thead><tr><th>Theme</th><th>Version</th><th>Status</th><th>Automatic updates</th><th></th></tr></thead>
        <tbody>
        <?php foreach ($themes as $t): $name = (string) $t['name']; ?>
            <tr>
                <td><div class="fw-medium"><?= e($t['title'] ?: $name) ?></div><div class="small text-body-secondary font-monospace"><?= e($name) ?></div></td>
                <td class="small"><?= e($t['version']) ?><?php if ($t['update'] !== ''): ?><div><span class="wcp-state is-warn"><?= e($t['update']) ?> available</span></div><?php endif; ?></td>
                <td><?= !empty($t['active']) ? '<span class="wcp-state is-ok">Active</span>' : (!empty($t['parent']) ? '<span class="wcp-state">Parent of the active theme</span>' : '<span class="wcp-state is-off">Inactive</span>') ?></td>
                <td class="small"><?= !empty($t['auto_update']) ? 'On ' . $op('theme_auto_off', [$name], 'Turn off', 'btn-link p-0') : 'Off ' . $op('theme_auto_on', [$name], 'Turn on', 'btn-link p-0') ?></td>
                <td class="text-end text-nowrap">
                    <?php if ($t['update'] !== ''): ?><?= $op('theme_update', [$name], 'Update', 'btn-outline-primary') ?><?php endif; ?>
                    <?php if (empty($t['active'])): ?><?= $op('theme_activate', [$name], 'Activate', 'btn-outline-success', 'Switch the site to ' . ($t['title'] ?: $name) . '? Visitors see the new theme at once.') ?><?php endif; ?>
                </td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table></div>
</div>

<?php if ($can): ?>
<div class="row g-3">
    <div class="col-md-6">
        <div class="wcp-panel h-100">
            <div class="fw-semibold mb-2">Updates</div>
            <?php $auto = $info['constants']['WP_AUTO_UPDATE_CORE'] ?? null; $autoNow = match (true) { $auto === "'minor'" => 'minor', $auto === 'true' => 'major', $auto === 'false' || !empty($info['constants']['AUTOMATIC_UPDATER_DISABLED']) => 'off', default => 'minor' }; ?>
            <form class="d-flex flex-wrap gap-2 align-items-end mb-3" method="post" action="<?= e($manage) ?>" data-wcp-busy>
                <?= $this->csrfField() ?><input type="hidden" name="op" value="core_auto_update">
                <div><label class="form-label small mb-1" for="wp-auto">Automatic WordPress updates</label>
                    <select class="form-select form-select-sm" id="wp-auto" name="value">
                        <option value="minor"<?= $autoNow === 'minor' ? ' selected' : '' ?>>Security and maintenance releases (recommended)</option>
                        <option value="major"<?= $autoNow === 'major' ? ' selected' : '' ?>>All releases, including new major versions</option>
                        <option value="off"<?= $autoNow === 'off' ? ' selected' : '' ?>>Off</option>
                    </select></div>
                <button class="btn btn-sm btn-outline-primary">Save</button>
            </form>
            <?= $op('core_update', [], WP::outdated($install) ? 'Update WordPress to ' . $install['latest'] : 'Reinstall / update WordPress core', WP::outdated($install) ? 'btn-primary' : 'btn-outline-secondary', 'Update WordPress core now? Back the website up first.') ?>
        </div>
    </div>
    <div class="col-md-6">
        <div class="wcp-panel h-100">
            <div class="fw-semibold mb-2">Tools</div>
            <div class="d-flex flex-wrap gap-2 mb-2">
                <?= !empty($info['maintenance'])
                    ? $op('maintenance_off', [], 'Turn maintenance mode off', 'btn-outline-warning')
                    : $op('maintenance_on', [], 'Turn maintenance mode on', 'btn-outline-secondary', 'Visitors will see "Briefly unavailable for scheduled maintenance" until you turn it off.') ?>
                <?= $op('verify_core', [], 'Verify core files', 'btn-outline-secondary') ?>
                <?= !empty($info['search_engines'])
                    ? $op('search_engines_off', [], 'Hide from search engines', 'btn-outline-secondary', 'Ask search engines not to index this site?')
                    : $op('search_engines_on', [], 'Allow search engines', 'btn-outline-secondary') ?>
            </div>
            <div class="small text-body-secondary">Commands run as the website account with WP-CLI; the site's own plugins and themes are not loaded for them. Every change is recorded in the audit log.</div>
        </div>
    </div>
</div>
<?php endif; ?>
