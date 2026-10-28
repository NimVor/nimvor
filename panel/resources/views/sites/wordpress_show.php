<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var array<string, mixed> $install
 * @var array<string, mixed>|null $info
 * @var list<array{level: string, title: string, detail: string, fix: ?string}> $checks
 * @var bool $canManage
 * @var bool $others
 * @var bool $isAdmin
 * @var list<string>|null $verify
 * @var array<string, mixed>|null $protection
 * @var array{measures: list<array<string, mixed>>, problems: list<string>}|null $security
 * @var list<array<string, mixed>> $restorePoints
 * @var list<array<string, mixed>> $copyTargets
 * @var list<array<string, mixed>> $syncTargets
 * @var array{items: list<array<string, mixed>>, checked_at: ?string} $vulns
 * @var int|null $dbId
 * @var string|null $newDbPassword
 */
use NimVor\Services\WordPressService as WP;
use NimVor\Services\WordPressToolsService as Tools;

$id = (int) $site['id'];
$wid = (int) $install['id'];
$manage = $this->url('sites.wordpress.manage', ['id' => $id, 'wp' => $wid]);
$toolUrl = $this->url('sites.wordpress.tool', ['id' => $id, 'wp' => $wid]);
$suspended = !empty($site['account_suspended']);
$can = $canManage && !$suspended;
// A small form that runs one WP-CLI operation.
$op = function (string $op, array $items, string $label, string $class = 'btn-outline-secondary', string $confirm = '', string $value = '') use ($manage, $can): string {
    if (!$can) {
        return '';
    }
    $h = '<form class="d-inline" method="post" action="' . e($manage) . '" data-nim-busy' . ($confirm !== '' ? ' data-nim-confirm="' . e($confirm) . '"' : '') . '>' . $this->csrfField()
        . '<input type="hidden" name="op" value="' . e($op) . '">';
    foreach ($items as $it) {
        $h .= '<input type="hidden" name="items[]" value="' . e($it) . '">';
    }
    if ($value !== '') {
        $h .= '<input type="hidden" name="value" value="' . e($value) . '">';
    }
    return $h . '<button class="btn btn-sm ' . e($class) . '"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>' . e($label) . '</button></form>';
};
// A switch that submits at once: a WP-CLI operation or a toolkit tool.
$switch = function (string $key, bool $on, string $action, array $fields, string $confirm = '') use ($can): string {
    $h = '<form method="post" action="' . e($action) . '" data-nim-busy' . ($confirm !== '' ? ' data-nim-confirm="' . e($confirm) . '"' : '') . '>' . $this->csrfField();
    foreach ($fields as $k => $v) {
        $h .= '<input type="hidden" name="' . e($k) . '" value="' . e($v) . '">';
    }
    return $h . '<div class="form-check form-switch m-0"><input class="form-check-input nim-wp-switch" type="checkbox" role="switch" id="wp-sw-' . e($key) . '" name="enabled" value="1" aria-label="' . e($key) . '"'
        . ($on ? ' checked' : '') . ($can ? ' data-nim-autosubmit' : ' disabled') . '></div></form>';
};
$where = $install['path'] === '' ? '/' : $install['path'];
$backups = $this->hasRoute('sites.backups') ? $this->url('sites.backups', ['id' => $id]) : null;
$levels = ['danger' => ['bi-x-octagon-fill text-danger', 'is-error'], 'warning' => ['bi-exclamation-triangle-fill text-warning', 'is-warn'], 'info' => ['bi-info-circle text-primary', '']];
$const = (array) ($info['constants'] ?? []);
$plugins = (array) ($info['plugins'] ?? []);
$themes = (array) ($info['themes'] ?? []);
$pUpd = array_values(array_filter($plugins, static fn ($p): bool => ($p['update'] ?? '') !== ''));
$tUpd = array_values(array_filter($themes, static fn ($t): bool => ($t['update'] ?? '') !== ''));
$measures = (array) ($security['measures'] ?? []);
$applied = count(array_filter($measures, static fn ($m): bool => !empty($m['applied'])));
$urlPath = null;
try {
    $urlPath = Tools::urlPath($site, $install);
} catch (\NimVor\Support\UserFacingException) {
}
$protected = null;
foreach ((array) ($protection['password'] ?? []) as $p) {
    if ($urlPath !== null && $p['path'] === $urlPath) {
        $protected = $p;
    }
}
$hotlink = $protection['hotlink'] ?? null;
$vulnItems = $vulns['items'];
$sevClass = ['critical' => 'text-bg-danger', 'high' => 'text-bg-danger', 'medium' => 'text-bg-warning', 'low' => 'text-bg-secondary', 'none' => 'text-bg-light border', '' => 'text-bg-light border'];
$fmStart = trim((string) $install['path'], '/');
$issues = count(array_filter($checks, static fn ($c): bool => $c['level'] !== 'info'));
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'wordpress']) ?>

<?php if ($suspended): ?><div class="alert alert-warning small">The hosting account is suspended: WordPress can be viewed but not changed.</div><?php endif; ?>

<div class="nim-panel nim-wp-hero mb-3">
    <div class="d-flex flex-wrap align-items-center gap-3">
        <div class="nim-wp-logo" aria-hidden="true"><i class="bi bi-wordpress"></i></div>
        <div class="me-auto min-w-0">
            <div class="d-flex flex-wrap align-items-center gap-2">
                <h2 class="h5 mb-0 text-truncate"><?= e($info['title'] ?? 'WordPress') ?></h2>
                <span class="badge rounded-pill text-bg-light border fw-normal">WordPress <?= e($info['version'] ?? $install['version'] ?: '?') ?></span>
                <?php if ($install['cloned_from'] !== null): ?><span class="badge rounded-pill text-bg-info fw-normal">Staging copy</span><?php endif; ?>
            </div>
            <div class="small text-body-secondary mt-1">
                <code><?= e($where) ?></code>
                <?php if ($install['url'] !== null): ?> · <a href="<?= e($install['url']) ?>/" target="_blank" rel="noopener noreferrer"><?= e($install['url']) ?> <i class="bi bi-box-arrow-up-right small" aria-hidden="true"></i></a><?php endif; ?>
            </div>
            <?php if ($info !== null): ?>
            <div class="d-flex flex-wrap gap-2 mt-2">
                <?php if (WP::outdated($install)): ?><span class="nim-state is-warn">Update to <?= e($install['latest']) ?></span><?php elseif (($info['core']['latest'] ?? '') !== ''): ?><span class="nim-state is-ok">Up to date</span><?php endif; ?>
                <?php if ($security !== null): ?><a class="nim-state <?= $applied === count($measures) ? 'is-ok' : 'is-warn' ?> text-decoration-none" href="#security">Security <?= $applied ?>/<?= count($measures) ?></a><?php endif; ?>
                <a class="nim-state <?= $vulnItems === [] ? ($vulns['checked_at'] !== null ? 'is-ok' : 'is-off') : 'is-error' ?> text-decoration-none" href="#security"><?= $vulns['checked_at'] === null ? 'Vulnerabilities not checked' : ($vulnItems === [] ? 'No known vulnerabilities' : count($vulnItems) . ' vulnerabilit' . (count($vulnItems) === 1 ? 'y' : 'ies')) ?></a>
                <?php if (count($pUpd) + count($tUpd) > 0): ?><a class="nim-state is-warn text-decoration-none" href="#plugins"><?= count($pUpd) + count($tUpd) ?> update(s)</a><?php endif; ?>
                <?php if ($issues > 0): ?><span class="nim-state is-warn"><?= $issues ?> to review</span><?php endif; ?>
            </div>
            <?php endif; ?>
        </div>
        <div class="d-flex flex-wrap gap-2">
            <?php if ($others): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.wordpress', ['id' => $id])) ?>?list=1"><i class="bi bi-list-ul me-1" aria-hidden="true"></i>All installations</a><?php endif; ?>
            <form method="post" action="<?= e($this->url('sites.wordpress.check', ['id' => $id, 'wp' => $wid])) ?>" data-nim-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><i class="bi bi-arrow-repeat me-1" aria-hidden="true"></i>Check for updates</button></form>
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
    </div>
    <?php if ($info !== null): ?>
    <div class="nim-wp-tools mt-3" role="toolbar" aria-label="Tools">
        <?php if ($this->hasRoute('sites.files')): ?><a class="nim-wp-tool" href="<?= e($this->url('sites.files', ['id' => $id])) ?>?path=<?= e(rawurlencode($fmStart)) ?>" target="_blank" rel="noopener"><i class="bi bi-folder2-open" aria-hidden="true"></i><span>File Manager</span></a><?php endif; ?>
        <?php if ($dbId !== null && $this->hasRoute('databases.browser')): ?><a class="nim-wp-tool" href="<?= e($this->url('databases.browser', ['id' => $dbId])) ?>" target="_blank" rel="noopener"><i class="bi bi-table" aria-hidden="true"></i><span>Database</span></a><?php else: ?><a class="nim-wp-tool" href="#database"><i class="bi bi-database" aria-hidden="true"></i><span>Database</span></a><?php endif; ?>
        <a class="nim-wp-tool" href="<?= e($this->url('sites.wordpress.logs', ['id' => $id, 'wp' => $wid])) ?>"><i class="bi bi-journal-text" aria-hidden="true"></i><span>Logs</span></a>
        <?php if ($can): ?>
            <a class="nim-wp-tool" href="#wp-clone"><i class="bi bi-copy" aria-hidden="true"></i><span>Clone</span></a>
            <a class="nim-wp-tool" href="#wp-sync"><i class="bi bi-arrow-left-right" aria-hidden="true"></i><span>Sync</span></a>
            <a class="nim-wp-tool" href="#security"><i class="bi bi-shield-lock" aria-hidden="true"></i><span>Security</span></a>
        <?php endif; ?>
        <?php if ($backups !== null): ?><a class="nim-wp-tool" href="<?= e($backups) ?>"><i class="bi bi-cloud-arrow-up" aria-hidden="true"></i><span>Back up</span></a><?php endif; ?>
        <a class="nim-wp-tool" href="#staging"><i class="bi bi-clock-history" aria-hidden="true"></i><span>Restore points</span></a>
    </div>
    <?php endif; ?>
</div>

<?php if ($agentError !== null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError]) ?>
    <?php return; ?>
<?php endif; ?>
<?php if ($verify !== null): ?>
    <div class="nim-panel mb-3">
        <div class="fw-semibold mb-1">Core file verification</div>
        <?php if ($verify === []): ?>
            <div class="small text-success"><i class="bi bi-check-circle me-1" aria-hidden="true"></i>Every WordPress core file matches the official release.</div>
        <?php else: ?>
            <div class="small mb-1">These files differ from the official WordPress <?= e($info['version'] ?? '') ?> release or should not be there. If you did not change them, the site may be compromised: reinstall WordPress core (Update WordPress) and scan for malware.</div>
            <ul class="small font-monospace mb-0"><?php foreach ($verify as $f): ?><li><?= e($f) ?></li><?php endforeach; ?></ul>
        <?php endif; ?>
    </div>
<?php endif; ?>

<div class="nim-subtabs" role="tablist">
    <button class="active" type="button" role="tab" data-bs-toggle="tab" data-bs-target="#dashboard" aria-controls="dashboard" aria-selected="true">Dashboard</button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#plugins" aria-controls="plugins" aria-selected="false">Plugins <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($plugins) ?></span></button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#themes" aria-controls="themes" aria-selected="false">Themes <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($themes) ?></span></button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#security" aria-controls="security" aria-selected="false">Security</button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#database" aria-controls="database" aria-selected="false">Database</button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#staging" aria-controls="staging" aria-selected="false">Staging &amp; restore</button>
</div>

<div class="tab-content">
<div class="tab-pane fade show active" id="dashboard" role="tabpanel">
<div class="row g-3 mb-3">
    <div class="col-lg-6">
        <div class="nim-panel h-100">
            <div class="fw-semibold mb-2">Tools</div>
            <ul class="list-unstyled mb-0 nim-wp-toggles">
                <li>
                    <div><div class="fw-medium">Search engine indexing</div><div class="small text-body-secondary">Let search engines list this site.</div></div>
                    <?= $switch('indexing', !empty($info['search_engines']), $manage, ['op' => !empty($info['search_engines']) ? 'search_engines_off' : 'search_engines_on'], !empty($info['search_engines']) ? 'Ask search engines not to index this site?' : '') ?>
                </li>
                <li>
                    <div><div class="fw-medium">Debugging</div><div class="small text-body-secondary"><?= !empty($const['WP_DEBUG']) ? 'On' : 'Off' ?><?php if ($can): ?> · <a href="#wp-debug">Settings</a><?php endif; ?></div></div>
                    <?= $switch('debugging', !empty($const['WP_DEBUG']), $toolUrl, ['tool' => 'debug'] + (empty($const['WP_DEBUG']) ? ['debug[]' => 'WP_DEBUG'] : [])) ?>
                </li>
                <li>
                    <div><div class="fw-medium">Maintenance mode</div><div class="small text-body-secondary">Visitors see "Briefly unavailable for scheduled maintenance".</div></div>
                    <?= $switch('maintenance', !empty($info['maintenance']), $manage, ['op' => !empty($info['maintenance']) ? 'maintenance_off' : 'maintenance_on'], empty($info['maintenance']) ? 'Put the site into maintenance mode?' : '') ?>
                </li>
                <li>
                    <div><div class="fw-medium">Password protection</div><div class="small text-body-secondary"><?= $protected !== null ? 'Visitors sign in as <code>' . e($protected['user']) . '</code>' : 'Visitors must sign in before they see the site.' ?><?php if ($can && $urlPath !== null): ?> · <a href="#wp-protect"><?= $protected !== null ? 'Change' : 'Set up' ?></a><?php endif; ?></div></div>
                    <?php if ($protected !== null): ?><?= $switch('password', true, $toolUrl, ['tool' => 'password_protection'], 'Turn password protection off? Everyone can see the site again.') ?>
                    <?php else: ?><a class="form-check form-switch m-0 text-decoration-none<?= $can && $urlPath !== null ? '' : ' pe-none opacity-50' ?>" href="#wp-protect" aria-label="Turn password protection on"><input class="form-check-input pe-none" type="checkbox" role="switch" tabindex="-1" aria-hidden="true"></a><?php endif; ?>
                </li>
                <li>
                    <div><div class="fw-medium">Take over wp-cron.php</div><div class="small text-body-secondary">Scheduled tasks run every 5 minutes from a cron job instead of on visits.</div></div>
                    <?= $switch('wpcron', $install['cron_job_id'] !== null || !empty($const['DISABLE_WP_CRON']), $toolUrl, ['tool' => 'cron']) ?>
                </li>
                <li>
                    <div><div class="fw-medium">Hotlink protection</div><div class="small text-body-secondary">Other sites cannot embed this website's images and media<?= is_array($hotlink) && ($hotlink['allowed'] ?? []) !== [] ? ' (allowed: ' . e(implode(', ', $hotlink['allowed'])) . ')' : '' ?>.<?php if ($can): ?> · <a href="#wp-hotlink">Settings</a><?php endif; ?></div></div>
                    <?php if ($hotlink !== null): ?><?= $switch('hotlink', true, $toolUrl, ['tool' => 'hotlink']) ?>
                    <?php else: ?><?= $switch('hotlink', false, $toolUrl, ['tool' => 'hotlink']) ?><?php endif; ?>
                </li>
                <li>
                    <div><div class="fw-medium">Vulnerability protection</div><div class="small text-body-secondary">ModSecurity with the OWASP Core Rule Set filters attacks; known vulnerabilities are checked daily.</div></div>
                    <?= $switch('vulnerability', !empty($install['vuln_protection']), $toolUrl, ['tool' => 'vulnerability']) ?>
                </li>
            </ul>
            <?php if ($can && ($info['admins'] ?? []) !== []): ?><div class="mt-3"><a class="btn btn-sm btn-outline-secondary" href="#wp-admin-pass"><i class="bi bi-key me-1" aria-hidden="true"></i>Change administrator password</a></div><?php endif; ?>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="nim-panel mb-3 small">
            <div class="fw-semibold mb-2">Overview</div>
            <dl class="row mb-0">
                <dt class="col-5 fw-normal nim-muted">WordPress</dt>
                <dd class="col-7"><?= e($info['version'] ?? '?') ?>
                    <?php if (WP::outdated($install)): ?><span class="nim-state is-warn"><?= e($install['latest']) ?> available</span><?php elseif (($info['core']['latest'] ?? '') !== ''): ?><span class="nim-state is-ok">Up to date</span><?php endif; ?></dd>
                <dt class="col-5 fw-normal nim-muted">PHP</dt>
                <dd class="col-7"><?= e($site['php_version']) ?><?php if (($info['core']['php_needed'] ?? '') !== ''): ?> <span class="text-body-secondary">(latest WordPress needs <?= e($info['core']['php_needed']) ?>+)</span><?php endif; ?></dd>
                <dt class="col-5 fw-normal nim-muted">Address</dt>
                <dd class="col-7 text-break"><?= e($info['siteurl'] ?? '') ?><?php if (!$site['https']): ?> <span class="nim-state is-warn">no HTTPS</span><?php endif; ?></dd>
                <dt class="col-5 fw-normal nim-muted">Administrators</dt>
                <dd class="col-7"><?= e(implode(', ', array_map(static fn ($a): string => (string) $a['login'], (array) ($info['admins'] ?? [])))) ?: '—' ?></dd>
                <?php if (!empty($info['multisite'])): ?><dt class="col-5 fw-normal nim-muted">Network</dt><dd class="col-7">Multisite</dd><?php endif; ?>
                <dt class="col-5 fw-normal nim-muted">Updates checked</dt>
                <dd class="col-7"><?= ($info['core']['latest'] ?? '') === '' ? '<span class="nim-state is-warn">WordPress.org not reachable</span>' : e(gmdate('Y-m-d H:i', (int) $info['core']['checked_at'])) . ' UTC' ?></dd>
            </dl>
        </div>
        <?php if ($can): ?>
        <div class="nim-panel small">
            <div class="fw-semibold mb-2">Updates</div>
            <?php $auto = $const['WP_AUTO_UPDATE_CORE'] ?? null; $autoNow = match (true) { $auto === "'minor'" => 'minor', $auto === 'true' => 'major', $auto === 'false' || !empty($const['AUTOMATIC_UPDATER_DISABLED']) => 'off', default => 'minor' }; ?>
            <form class="d-flex flex-wrap gap-2 align-items-end mb-2" method="post" action="<?= e($manage) ?>" data-nim-busy>
                <?= $this->csrfField() ?><input type="hidden" name="op" value="core_auto_update">
                <div class="flex-grow-1"><label class="form-label small mb-1" for="wp-auto">Automatic WordPress updates</label>
                    <select class="form-select form-select-sm" id="wp-auto" name="value">
                        <option value="minor"<?= $autoNow === 'minor' ? ' selected' : '' ?>>Security and maintenance releases (recommended)</option>
                        <option value="major"<?= $autoNow === 'major' ? ' selected' : '' ?>>All releases, including new major versions</option>
                        <option value="off"<?= $autoNow === 'off' ? ' selected' : '' ?>>Off</option>
                    </select></div>
                <button class="btn btn-sm btn-outline-primary">Save</button>
            </form>
            <div class="d-flex flex-wrap gap-2">
                <?= $op('core_update', [], WP::outdated($install) ? 'Update WordPress to ' . $install['latest'] : 'Reinstall WordPress core', WP::outdated($install) ? 'btn-primary' : 'btn-outline-secondary', 'Update WordPress core now? Back the website up first.') ?>
                <?= $op('verify_core', [], 'Verify core files', 'btn-outline-secondary') ?>
            </div>
            <?php if ($backups !== null): ?><div class="mt-2 text-body-secondary">Before large updates, <a href="<?= e($backups) ?>#backup-now">back up the website</a>.</div><?php endif; ?>
        </div>
        <?php endif; ?>
    </div>
</div>
<div class="nim-panel mb-3">
    <div class="fw-semibold mb-2">Health checks <span class="badge rounded-pill <?= $issues > 0 ? 'text-bg-warning' : 'text-bg-success' ?> fw-normal"><?= $issues > 0 ? $issues . ' to review' : 'OK' ?></span></div>
    <?php if ($checks === []): ?><div class="small text-success"><i class="bi bi-check-circle me-1" aria-hidden="true"></i>Nothing to review.</div><?php endif; ?>
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

<div class="tab-pane fade" id="plugins" role="tabpanel">
<div class="nim-panel mb-3" data-nim-dt>
    <div class="nim-actionbar">
        <div class="fw-semibold">Plugins <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($plugins) ?></span></div>
        <div class="nim-actionbar-end">
            <div class="nim-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search plugins" data-nim-dt-search><i class="bi bi-search" aria-hidden="true"></i></div>
            <?php if ($pUpd !== []): ?><?= $op('plugin_update', [], 'Update all (' . count($pUpd) . ')', 'btn-primary', 'Update ' . count($pUpd) . ' plugin(s) now?') ?><?php endif; ?>
        </div>
    </div>
    <?php if ($plugins === []): ?>
        <div class="nim-empty-state"><i class="bi bi-plug" aria-hidden="true"></i><div>No plugins installed.</div></div>
    <?php else: ?>
    <div class="nim-dtable table-responsive"><table class="table align-middle">
        <thead><tr><th>Plugin</th><th>Version</th><th>Status</th><th>Automatic updates</th><th></th></tr></thead>
        <tbody>
        <?php foreach ($plugins as $p): $name = (string) $p['name']; ?>
            <tr>
                <td><div class="fw-medium"><?= e($p['title'] ?: $name) ?></div><div class="small text-body-secondary font-monospace"><?= e($name) ?><?= !empty($p['must_use']) ? ' · must-use' : '' ?></div></td>
                <td class="small"><?= e($p['version']) ?><?php if ($p['update'] !== ''): ?><div><span class="nim-state is-warn"><?= e($p['update']) ?> available</span></div><?php endif; ?></td>
                <td><?= !empty($p['active']) ? '<span class="nim-state is-ok">Active</span>' : '<span class="nim-state is-off">Inactive</span>' ?></td>
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
</div>

<div class="tab-pane fade" id="themes" role="tabpanel">
<div class="nim-panel mb-3" data-nim-dt>
    <div class="nim-actionbar">
        <div class="fw-semibold">Themes <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($themes) ?></span></div>
        <div class="nim-actionbar-end"><?php if ($tUpd !== []): ?><?= $op('theme_update', [], 'Update all (' . count($tUpd) . ')', 'btn-primary', 'Update ' . count($tUpd) . ' theme(s) now?') ?><?php endif; ?></div>
    </div>
    <div class="nim-dtable table-responsive"><table class="table align-middle">
        <thead><tr><th>Theme</th><th>Version</th><th>Status</th><th>Automatic updates</th><th></th></tr></thead>
        <tbody>
        <?php foreach ($themes as $t): $name = (string) $t['name']; ?>
            <tr>
                <td><div class="fw-medium"><?= e($t['title'] ?: $name) ?></div><div class="small text-body-secondary font-monospace"><?= e($name) ?></div></td>
                <td class="small"><?= e($t['version']) ?><?php if ($t['update'] !== ''): ?><div><span class="nim-state is-warn"><?= e($t['update']) ?> available</span></div><?php endif; ?></td>
                <td><?= !empty($t['active']) ? '<span class="nim-state is-ok">Active</span>' : (!empty($t['parent']) ? '<span class="nim-state">Parent of the active theme</span>' : '<span class="nim-state is-off">Inactive</span>') ?></td>
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
</div>

<div class="tab-pane fade" id="security" role="tabpanel">
<div class="row g-3 mb-3">
    <div class="col-xl-7">
        <div class="nim-panel h-100">
            <div class="nim-actionbar">
                <div class="fw-semibold">Security hardening <span class="badge rounded-pill <?= $applied === count($measures) && $measures !== [] ? 'text-bg-success' : 'text-bg-warning' ?> fw-normal"><?= $applied ?> of <?= count($measures) ?> applied</span></div>
                <?php if ($can && $measures !== []): ?>
                <div class="nim-actionbar-end">
                    <form method="post" action="<?= e($toolUrl) ?>" data-nim-busy data-nim-confirm="Apply the recommended security measures? Each one can be rolled back here.">
                        <?= $this->csrfField() ?><input type="hidden" name="tool" value="security"><input type="hidden" name="op" value="apply"><input type="hidden" name="recommended" value="1">
                        <button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><i class="bi bi-shield-check me-1" aria-hidden="true"></i>Secure (recommended)</button>
                    </form>
                </div>
                <?php endif; ?>
            </div>
            <?php if ($security === null): ?>
                <div class="small text-body-secondary">The security status could not be read<?= $urlPath === null ? ' (the installation is outside the document root)' : '' ?>.</div>
            <?php else: ?>
            <form method="post" action="<?= e($toolUrl) ?>" data-nim-busy id="wp-sec-form">
                <?= $this->csrfField() ?><input type="hidden" name="tool" value="security">
                <ul class="list-unstyled mb-2 small">
                    <?php foreach ($measures as $m): ?>
                        <li class="d-flex gap-2 py-2 border-bottom align-items-start">
                            <input class="form-check-input mt-1" type="checkbox" name="measures[]" value="<?= e($m['id']) ?>" id="m-<?= e($m['id']) ?>"<?= $can ? '' : ' disabled' ?>>
                            <label class="me-auto" for="m-<?= e($m['id']) ?>"><span class="fw-medium"><?= e($m['title']) ?></span> <span class="badge text-bg-light border fw-normal"><?= $m['kind'] === 'web' ? 'Web server' : 'WordPress' ?></span><div class="text-body-secondary"><?= e($m['help']) ?></div></label>
                            <span class="text-nowrap"><?= !empty($m['applied']) ? '<span class="nim-state is-ok">Applied</span>' : '<span class="nim-state is-off">Not applied</span>' ?><?= !empty($m['can_rollback']) ? '<div class="text-body-secondary text-end">can roll back</div>' : '' ?></span>
                        </li>
                    <?php endforeach; ?>
                </ul>
                <?php if ($can): ?>
                <div class="d-flex flex-wrap gap-2">
                    <button class="btn btn-sm btn-outline-primary" name="op" value="apply"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Apply selected</button>
                    <button class="btn btn-sm btn-outline-secondary" name="op" value="rollback" data-nim-confirm-submit="Roll back the selected measures? Their earlier state is put back."><i class="bi bi-arrow-counterclockwise me-1" aria-hidden="true"></i>Roll back selected</button>
                </div>
                <?php endif; ?>
            </form>
            <?php endif; ?>
        </div>
    </div>
    <div class="col-xl-5">
        <div class="nim-panel h-100">
            <div class="nim-actionbar">
                <div class="fw-semibold">Vulnerabilities</div>
                <?php if ($can): ?>
                <div class="nim-actionbar-end">
                    <form method="post" action="<?= e($toolUrl) ?>" data-nim-busy><?= $this->csrfField() ?><input type="hidden" name="tool" value="vuln_check"><button class="btn btn-sm btn-outline-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><i class="bi bi-bug me-1" aria-hidden="true"></i>Check now</button></form>
                </div>
                <?php endif; ?>
            </div>
            <div class="d-flex align-items-center justify-content-between border rounded p-2 mb-2 small">
                <div><div class="fw-medium">Vulnerability protection</div><div class="text-body-secondary">ModSecurity (OWASP CRS) + daily checks.</div></div>
                <?= $switch('vulnerability-2', !empty($install['vuln_protection']), $toolUrl, ['tool' => 'vulnerability']) ?>
            </div>
            <?php if ($vulns['checked_at'] === null): ?>
                <div class="small text-body-secondary">Not checked yet. The check compares WordPress, the plugins and the themes with the public WPVulnerability database.</div>
            <?php elseif ($vulnItems === []): ?>
                <div class="small text-success"><i class="bi bi-check-circle me-1" aria-hidden="true"></i>No known vulnerabilities (checked <?= e(substr((string) $vulns['checked_at'], 0, 16)) ?> UTC).</div>
            <?php else: ?>
                <ul class="list-unstyled small mb-0">
                    <?php foreach (array_slice($vulnItems, 0, 50) as $v): ?>
                        <li class="py-1 border-bottom">
                            <span class="badge <?= e($sevClass[$v['severity']] ?? 'text-bg-light border') ?>"><?= e($v['severity'] !== '' ? ucfirst((string) $v['severity']) : 'Unrated') ?><?= $v['score'] !== '' ? ' ' . e($v['score']) : '' ?></span>
                            <span class="fw-medium"><?= e($v['title']) ?> <?= e($v['version']) ?></span>
                            <div class="text-body-secondary"><?= e($v['name']) ?><?= $v['cve'] !== '' ? ' · ' . e($v['cve']) : '' ?></div>
                            <div><?= $v['fixed_in'] !== '' ? 'Fixed in ' . e($v['fixed_in']) . ': update it.' : ($v['unfixed'] ? '<span class="text-danger">No fix yet: consider deactivating it.</span>' : '') ?><?php if ($v['link'] !== ''): ?> <a href="<?= e($v['link']) ?>" target="_blank" rel="noopener noreferrer">Details</a><?php endif; ?></div>
                        </li>
                    <?php endforeach; ?>
                </ul>
            <?php endif; ?>
        </div>
    </div>
</div>
</div>

<div class="tab-pane fade" id="database" role="tabpanel">
<div class="nim-panel mb-3 small">
    <div class="fw-semibold mb-2">Database</div>
    <?php if ($newDbPassword !== null): ?>
        <div class="alert alert-success">New database password (shown once; wp-config.php already uses it): <code class="user-select-all"><?= e($newDbPassword) ?></code></div>
    <?php endif; ?>
    <dl class="row mb-3">
        <dt class="col-sm-3 fw-normal nim-muted">Name</dt><dd class="col-sm-9"><code><?= e($info['db']['name'] ?? $install['db_name']) ?></code></dd>
        <dt class="col-sm-3 fw-normal nim-muted">Host</dt><dd class="col-sm-9"><?= e($info['db']['host'] ?? '') ?></dd>
        <dt class="col-sm-3 fw-normal nim-muted">Table prefix</dt><dd class="col-sm-9"><code><?= e($info['db']['prefix'] ?? '') ?></code></dd>
        <dt class="col-sm-3 fw-normal nim-muted">Size</dt><dd class="col-sm-9"><?= (int) ($info['db']['tables'] ?? 0) ?> tables · <?= e(format_bytes((int) ($info['db']['bytes'] ?? 0))) ?> · <?= e($info['mysql'] ?? '') ?></dd>
    </dl>
    <div class="d-flex flex-wrap gap-2">
        <?php if ($dbId !== null && $this->hasRoute('databases.browser')): ?><a class="btn btn-sm btn-primary" href="<?= e($this->url('databases.browser', ['id' => $dbId])) ?>" target="_blank" rel="noopener"><i class="bi bi-table me-1" aria-hidden="true"></i>Open in database browser</a>
        <?php else: ?><span class="text-body-secondary">This database is not one of your databases in the panel, so the browser cannot open it.</span><?php endif; ?>
        <?php if ($can): ?>
            <form method="post" action="<?= e($toolUrl) ?>" data-nim-busy data-nim-confirm="Change the database password? A new random password is set in the database and in wp-config.php at once.">
                <?= $this->csrfField() ?><input type="hidden" name="tool" value="db_password">
                <button class="btn btn-sm btn-outline-secondary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><i class="bi bi-key me-1" aria-hidden="true"></i>Change database password</button>
            </form>
        <?php endif; ?>
    </div>
</div>
</div>

<div class="tab-pane fade" id="staging" role="tabpanel">
<div class="row g-3 mb-3">
    <div class="col-lg-6">
        <div class="nim-panel h-100 small">
            <div class="fw-semibold mb-1">Staging and cloning</div>
            <p class="text-body-secondary">Clone this site to another of your websites (for example a staging subdomain) with its own database, try changes there, then sync them back. Before anything is replaced, a restore point of the target is saved.</p>
            <?php if ($can): ?>
                <a class="btn btn-sm btn-primary" href="#wp-clone"><i class="bi bi-copy me-1" aria-hidden="true"></i>Clone</a>
                <a class="btn btn-sm btn-outline-primary<?= $syncTargets === [] ? ' disabled' : '' ?>" href="#wp-sync"><i class="bi bi-arrow-left-right me-1" aria-hidden="true"></i>Sync to another installation</a>
                <?php if ($syncTargets === []): ?><div class="text-body-secondary mt-2">There is no other installation of the same owner to sync with yet: clone first.</div><?php endif; ?>
            <?php endif; ?>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="nim-panel h-100 small">
            <div class="fw-semibold mb-2">Restore points</div>
            <?php if ($restorePoints === []): ?>
                <div class="text-body-secondary">None yet. A restore point is saved automatically before a sync or clone replaces this installation.</div>
            <?php else: ?>
                <ul class="list-unstyled mb-0">
                    <?php foreach ($restorePoints as $p): ?>
                        <li class="d-flex align-items-center gap-2 py-1 border-bottom">
                            <div class="me-auto"><div class="fw-medium"><?= e(preg_replace('/^(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})Z$/', '$1-$2-$3 $4:$5 UTC', (string) $p['id'])) ?></div><div class="text-body-secondary"><?= e((string) ($p['reason'] ?? '')) ?><?= !empty($p['database']) ? ' · files and database' : ' · files' ?></div></div>
                            <?php if ($can): ?>
                            <form method="post" action="<?= e($toolUrl) ?>" data-nim-busy data-nim-confirm="Put this restore point back? Files and the database return to that moment (files added since are kept).">
                                <?= $this->csrfField() ?><input type="hidden" name="tool" value="restore"><input type="hidden" name="restore_point" value="<?= e((string) $p['id']) ?>">
                                <button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-counterclockwise me-1" aria-hidden="true"></i>Restore</button>
                            </form>
                            <?php endif; ?>
                        </li>
                    <?php endforeach; ?>
                </ul>
            <?php endif; ?>
        </div>
    </div>
</div>
</div>
</div>

<?php if ($can): ?>
    <?= $this->modalStart('wp-debug', 'Debugging', false, 'modal-md') ?>
        <form method="post" action="<?= e($toolUrl) ?>" data-nim-busy>
            <?= $this->csrfField() ?><input type="hidden" name="tool" value="debug">
            <?php foreach (Tools::DEBUG as $k => $label): ?>
                <div class="form-check mb-2"><input class="form-check-input" type="checkbox" name="debug[]" value="<?= e($k) ?>" id="dbg-<?= e($k) ?>"<?= !empty($const[$k]) ? ' checked' : '' ?>><label class="form-check-label" for="dbg-<?= e($k) ?>"><?= e($label) ?></label></div>
            <?php endforeach; ?>
            <div class="form-text mb-3">Errors shown on pages reveal paths and code to visitors: use it briefly, and prefer the log. The debug log is under Logs.</div>
            <button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save</button>
        </form>
    <?= $this->modalEnd() ?>

    <?php if ($urlPath !== null): ?>
    <?= $this->modalStart('wp-protect', 'Password protection', false, 'modal-md') ?>
        <form method="post" action="<?= e($toolUrl) ?>" data-nim-busy autocomplete="off">
            <?= $this->csrfField() ?><input type="hidden" name="tool" value="password_protection"><input type="hidden" name="enabled" value="1">
            <p class="small text-body-secondary">Visitors to <code><?= e($site['domain'] . rtrim($urlPath, '/')) ?>/</code> must sign in with this user name and password before they see anything (certificate renewal keeps working).</p>
            <div class="mb-3"><label class="form-label" for="pp-user">User name</label><input class="form-control" id="pp-user" name="protect_user" required maxlength="64" pattern="[A-Za-z0-9._\-]+" value="<?= e($protected['user'] ?? '') ?>"></div>
            <div class="mb-3"><label class="form-label" for="pp-pass">Password</label><input class="form-control" id="pp-pass" name="protect_password" type="password" required minlength="8" maxlength="128" autocomplete="new-password"></div>
            <button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= $protected !== null ? 'Save' : 'Turn on' ?></button>
        </form>
    <?= $this->modalEnd() ?>
    <?php endif; ?>

    <?= $this->modalStart('wp-hotlink', 'Hotlink protection', false, 'modal-md') ?>
        <form method="post" action="<?= e($toolUrl) ?>" data-nim-busy>
            <?= $this->csrfField() ?><input type="hidden" name="tool" value="hotlink"><input type="hidden" name="enabled" value="1">
            <p class="small text-body-secondary">Images, video, audio and PDF files of <?= e($site['domain']) ?> are only served to its own pages, direct visits and the sites below.</p>
            <div class="mb-3"><label class="form-label" for="hl-allowed">Also allowed (optional)</label><input class="form-control" id="hl-allowed" name="hotlink_allowed" placeholder="partner.com, *.cdn.example.com" value="<?= e(implode(', ', (array) ($hotlink['allowed'] ?? []))) ?>"></div>
            <button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= $hotlink !== null ? 'Save' : 'Turn on' ?></button>
        </form>
    <?= $this->modalEnd() ?>

    <?php if (($info['admins'] ?? []) !== []): ?>
    <?= $this->modalStart('wp-admin-pass', 'Change administrator password', false, 'modal-md') ?>
        <form method="post" action="<?= e($toolUrl) ?>" data-nim-busy autocomplete="off">
            <?= $this->csrfField() ?><input type="hidden" name="tool" value="admin_password">
            <div class="mb-3"><label class="form-label" for="ap-admin">Administrator</label>
                <select class="form-select" id="ap-admin" name="admin_id"><?php foreach ($info['admins'] as $a): ?><option value="<?= (int) $a['id'] ?>"><?= e($a['login']) ?></option><?php endforeach; ?></select></div>
            <div class="mb-3"><label class="form-label" for="ap-pass">New password</label><input class="form-control" id="ap-pass" name="admin_password" type="password" required minlength="8" maxlength="128" autocomplete="new-password"></div>
            <div class="mb-3"><label class="form-label" for="ap-pass2">Repeat the password</label><input class="form-control" id="ap-pass2" name="admin_password_confirm" type="password" required minlength="8" maxlength="128" autocomplete="new-password"></div>
            <div class="form-text mb-3">The administrator's other sessions are signed out.</div>
            <button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Change password</button>
        </form>
    <?= $this->modalEnd() ?>
    <?php endif; ?>

    <?= $this->modalStart('wp-clone', 'Clone WordPress', false, 'modal-md') ?>
        <form method="post" action="<?= e($toolUrl) ?>" data-nim-busy>
            <?= $this->csrfField() ?><input type="hidden" name="tool" value="clone">
            <p class="small text-body-secondary">Files and the database are copied; the copy gets its own new database and its address is changed everywhere.</p>
            <div class="mb-3"><label class="form-label" for="cl-site">Copy to website</label>
                <select class="form-select" id="cl-site" name="target_site" required>
                    <?php foreach ($copyTargets as $t): ?><option value="<?= (int) $t['id'] ?>"><?= e($t['domain']) ?></option><?php endforeach; ?>
                </select>
                <div class="form-text">For a staging copy, create a subdomain such as staging.<?= e($site['domain']) ?> first (Domains).</div></div>
            <div class="mb-3"><label class="form-label" for="cl-path">Folder in its document root (optional)</label><input class="form-control" id="cl-path" name="target_path" placeholder="e.g. staging — empty for the document root" pattern="[A-Za-z0-9._\-/]*"></div>
            <div class="form-check mb-2"><input class="form-check-input" type="checkbox" name="staging" value="1" id="cl-staging" checked><label class="form-check-label" for="cl-staging">Hide the copy from search engines (staging)</label></div>
            <div class="form-check mb-3"><input class="form-check-input" type="checkbox" name="overwrite" value="1" id="cl-over"><label class="form-check-label" for="cl-over">Replace WordPress already there (a restore point is saved first)</label></div>
            <button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Clone</button>
        </form>
    <?= $this->modalEnd() ?>

    <?= $this->modalStart('wp-sync', 'Sync to another installation', false, 'modal-md') ?>
        <form method="post" action="<?= e($toolUrl) ?>" data-nim-busy data-nim-confirm="Copy from this installation over the chosen one? A restore point of the target is saved first.">
            <?= $this->csrfField() ?><input type="hidden" name="tool" value="sync">
            <p class="small text-body-secondary">Copies from <strong><?= e($site['domain'] . ($install['path'] !== '' ? '/' . $install['path'] : '')) ?></strong> to the installation below. Its wp-config.php and address are kept. To publish a staging site, open the staging copy and sync it to the live one.</p>
            <div class="mb-3"><label class="form-label" for="sy-target">Target</label>
                <select class="form-select" id="sy-target" name="target" required>
                    <?php foreach ($syncTargets as $t): ?><option value="<?= (int) $t['id'] ?>"><?= e($t['domain'] . ($t['path'] !== '' ? ' (' . $t['path'] . ')' : '')) ?></option><?php endforeach; ?>
                </select></div>
            <div class="form-check mb-2"><input class="form-check-input" type="checkbox" name="files" value="1" id="sy-files" checked><label class="form-check-label" for="sy-files">Files (new and changed files; nothing is deleted)</label></div>
            <div class="form-check mb-3"><input class="form-check-input" type="checkbox" name="database" value="1" id="sy-db" checked><label class="form-check-label" for="sy-db">Database (replaces the target's tables)</label></div>
            <button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Sync</button>
        </form>
    <?= $this->modalEnd() ?>
<?php endif; ?>
