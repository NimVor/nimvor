<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed>|null $status
 * @var string $custom
 * @var list<array<string, mixed>> $events
 * @var list<array<string, mixed>> $sites
 */
use WhaCP\Services\ModSecurityService;

$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE);
$cfg = (array) ($status['config'] ?? []);
$crs = (array) ($status['crs'] ?? []);
$crsCfg = (array) ($cfg['crs'] ?? []);
$loaded = (bool) ($status['module_loaded'] ?? false);
$active = (bool) ($status['active'] ?? false);
$vendorNames = [];
foreach ((array) ($status['vendors'] ?? []) as $v) {
    $vendorNames[$v['id']] = $v['name'];
}
$byDomain = [];
foreach ($sites as $s) {
    $byDomain[strtolower((string) $s['domain'])] = $s;
    foreach ((array) ($s['aliases'] ?? []) as $a) {
        $byDomain[strtolower((string) $a)] = $s;
    }
    $byDomain['www.' . strtolower((string) $s['domain'])] ??= $s;
}
$modeBadge = ['off' => ['Off', 'is-off'], 'detect' => ['Detection only', 'is-warn'], 'on' => ['On', 'is-ok']];
$paranoia = [1 => '1 — few false positives (recommended)', 2 => '2 — more rules, some false positives', 3 => '3 — strict, needs tuning', 4 => '4 — paranoid, many false positives'];
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($status === null) { return; } ?>

<div class="wcp-panel mb-3">
    <div class="wcp-statusbar">
        <span class="fw-medium">Web application firewall (ModSecurity)</span>
        <span><?= $loaded ? '<span class="wcp-state is-ok">Nginx connector loaded</span>' : '<span class="wcp-state is-off">Nginx connector not installed</span>' ?></span>
        <span class="small"><span class="wcp-muted">OWASP CRS</span> <?= ($crs['available'] ?? false) ? e((string) ($crs['version'] ?? '?')) : 'not installed' ?></span>
        <span><?= $active ? '<span class="wcp-state is-ok">Active</span>' : '<span class="wcp-state is-off">Off</span>' ?></span>
        <span class="small"><span class="wcp-muted">Audit log</span> <?= e(format_bytes((int) ($status['audit_log_bytes'] ?? 0))) ?></span>
        <?php if ($canManage && $loaded): ?>
            <form class="ms-auto" method="post" action="<?= e($this->url('security.modsec.update')) ?>" data-wcp-busy data-wcp-confirm="Update the rule packages now? Nginx is checked and reloaded afterwards; if the new rules do not load, the Core Rule Set is turned off and the websites keep working."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-repeat me-1"></i>Update rules</button></form>
        <?php endif; ?>
    </div>
</div>

<?php if (!$loaded): ?>
    <div class="wcp-infobar"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>
        <?php if ($status['packages_available'] ?? false): ?>
            Install <strong>Web application firewall for Nginx (ModSecurity)</strong> and <strong>OWASP Core Rule Set</strong> from <a href="<?= e($this->url('packages')) ?>">Packages</a>, then turn ModSecurity on here.
        <?php else: ?>
            This distribution has no ModSecurity connector for Nginx in its repositories (on AlmaLinux and RHEL it comes only from third-party repositories). Once a connector and the OWASP Core Rule Set are installed, WhaCP detects them here.
        <?php endif; ?>
    </span></div>
<?php endif; ?>

<div class="row g-3 mb-3">
    <div class="col-xl-5">
        <div class="wcp-panel h-100">
            <div class="fw-semibold mb-2">Server-wide settings</div>
            <form method="post" action="<?= e($this->url('security.modsec.configure')) ?>" data-wcp-busy>
                <?= $this->csrfField() ?>
                <fieldset<?= $canManage ? '' : ' disabled' ?>>
                <div class="form-check form-switch mb-1">
                    <input type="hidden" name="enabled" value="0">
                    <input class="form-check-input" type="checkbox" role="switch" id="ms-on" name="enabled" value="1"<?= !empty($cfg['enabled']) ? ' checked' : '' ?><?= $loaded ? '' : ' disabled' ?>>
                    <label class="form-check-label" for="ms-on">ModSecurity on</label>
                </div>
                <div class="form-text mt-0 mb-3">Loads the rules once for all websites. Each website is then off, detection only or on (below); off here filters no website.</div>
                <div class="form-check form-switch mb-2">
                    <input type="hidden" name="crs_enabled" value="0">
                    <input class="form-check-input" type="checkbox" role="switch" id="ms-crs" name="crs_enabled" value="1"<?= !empty($crsCfg['enabled']) ? ' checked' : '' ?>>
                    <label class="form-check-label" for="ms-crs">OWASP Core Rule Set</label>
                </div>
                <label class="form-label small" for="ms-pl">Paranoia level</label>
                <select class="form-select form-select-sm mb-2" id="ms-pl" name="paranoia">
                    <?php foreach ($paranoia as $n => $label): ?><option value="<?= $n ?>"<?= (int) ($crsCfg['paranoia'] ?? 1) === $n ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                </select>
                <div class="row g-2 mb-1">
                    <div class="col-6"><label class="form-label small" for="ms-in">Inbound anomaly threshold</label><input class="form-control form-control-sm" type="number" min="2" max="1000" id="ms-in" name="inbound_threshold" value="<?= (int) ($crsCfg['inbound_threshold'] ?? 5) ?>"></div>
                    <div class="col-6"><label class="form-label small" for="ms-out">Outbound anomaly threshold</label><input class="form-control form-control-sm" type="number" min="2" max="1000" id="ms-out" name="outbound_threshold" value="<?= (int) ($crsCfg['outbound_threshold'] ?? 4) ?>"></div>
                </div>
                <div class="form-text mt-0 mb-3">A request is blocked when the rules it matches add up to the threshold (a critical match counts 5). Higher: fewer false positives, less protection.</div>
                <div class="form-check form-switch mb-3">
                    <input type="hidden" name="custom_enabled" value="0">
                    <input class="form-check-input" type="checkbox" role="switch" id="ms-custom" name="custom_enabled" value="1"<?= !empty($cfg['custom']['enabled']) ? ' checked' : '' ?>>
                    <label class="form-check-label" for="ms-custom">Custom rules (<?= (int) ($status['custom_rules'] ?? 0) ?>)</label>
                </div>
                <?php if ($canManage): ?><button class="btn btn-sm btn-primary">Save and check</button><?php endif; ?>
                </fieldset>
            </form>
        </div>
    </div>
    <div class="col-xl-7">
        <div class="wcp-panel h-100">
            <div class="fw-semibold mb-2">Rule vendors</div>
            <div class="table-responsive">
                <table class="table align-middle mb-2">
                    <thead><tr><th>Vendor</th><th>Rule IDs</th><th>State</th></tr></thead>
                    <tbody>
                    <?php foreach ((array) ($status['vendors'] ?? []) as $v):
                        $on = $v['id'] === 'crs' ? (!empty($crsCfg['enabled']) && ($crs['available'] ?? false)) : (!empty($cfg['custom']['enabled']) && (int) ($status['custom_rules'] ?? 0) > 0); ?>
                        <tr>
                            <td><div class="fw-semibold"><?= e($v['name']) ?><?= $v['id'] === 'crs' && ($crs['version'] ?? '') !== '' ? ' <span class="small wcp-muted">' . e($crs['version']) . '</span>' : '' ?></div><div class="small wcp-muted"><?= e($v['description']) ?></div></td>
                            <td class="text-nowrap small font-monospace"><?= (int) $v['first_id'] ?>–<?= (int) $v['last_id'] ?></td>
                            <td><?php if ($v['id'] === 'crs' && !($crs['available'] ?? false)): ?><span class="wcp-state is-off">Not installed</span><?php else: ?><span class="wcp-state <?= $on ? 'is-ok' : 'is-off' ?>"><?= $on ? 'Enabled' : 'Disabled' ?></span><?php endif; ?></td>
                        </tr>
                    <?php endforeach; ?>
                    </tbody>
                </table>
            </div>
            <div class="small wcp-muted">Each website can leave out a vendor or single rules (below). Commercial rule sets need their vendor's licence and are not bundled. The Core Rule Set is updated with its package (<em>Update rules</em>, or the server's package updates).</div>
        </div>
    </div>
</div>

<div class="wcp-panel mb-3" data-wcp-dt id="sites">
    <div class="wcp-actionbar"><div class="fw-semibold">Websites</div>
        <div class="wcp-actionbar-end"><div class="wcp-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search websites" data-wcp-dt-search><i class="bi bi-search" aria-hidden="true"></i></div></div></div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Website</th><th>Mode</th><th>Rule vendors</th><th>Rules turned off</th><th></th></tr></thead>
            <tbody>
            <?php foreach ($sites as $s): $set = ModSecurityService::siteSetting($status, $s); [$ml, $mc] = $modeBadge[$set['mode']] ?? $modeBadge['off']; $rid = 'ms-edit-' . (int) $s['id']; ?>
                <tr data-wcp-row>
                    <td><a class="fw-semibold" href="<?= e($this->url('sites.firewall', ['id' => (int) $s['id']])) ?>"><?= e($s['domain']) ?></a><?php if (($s['owner_name'] ?? null) !== null): ?><div class="small wcp-muted"><?= e($s['owner_name']) ?></div><?php endif; ?></td>
                    <td><span class="wcp-state <?= e($mc) ?>"><?= e($ml) ?></span></td>
                    <td class="small"><?= e(implode(', ', array_map(static fn (string $v): string => $vendorNames[$v] ?? $v, array_values(array_diff(['crs', 'custom'], $set['exclude']))))) ?: '—' ?></td>
                    <td class="small font-monospace"><?= $set['disabled_rules'] === [] ? '—' : e(implode(' ', $set['disabled_rules'])) ?></td>
                    <td class="text-end"><?php if ($canManage): ?><button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#<?= $rid ?>">Edit</button><?php endif; ?></td>
                </tr>
                <?php if ($canManage): ?>
                <?php ob_start(); ?><?= $this->modalStart($rid, 'ModSecurity for ' . $s['domain']) ?>
                    <form class="row g-2 align-items-end" method="post" action="<?= e($this->url('security.modsec.site', ['id' => (int) $s['id']])) ?>" data-wcp-busy>
                        <?= $this->csrfField() ?>
                        <div class="col-md-3"><label class="form-label small" for="<?= $rid ?>-m">Mode</label>
                            <select class="form-select form-select-sm" id="<?= $rid ?>-m" name="mode"><?php foreach (ModSecurityService::MODES as $k => $label): ?><option value="<?= e($k) ?>"<?= $set['mode'] === $k ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?></select></div>
                        <div class="col-md-3"><div class="form-label small mb-1">Rule vendors</div>
                            <?php foreach ($vendorNames as $vid => $vname): ?><div class="form-check"><input class="form-check-input" type="checkbox" name="vendors[]" value="<?= e($vid) ?>" id="<?= $rid ?>-<?= e($vid) ?>"<?= in_array($vid, $set['exclude'], true) ? '' : ' checked' ?>><label class="form-check-label small" for="<?= $rid ?>-<?= e($vid) ?>"><?= e($vname) ?></label></div><?php endforeach; ?></div>
                        <div class="col-md-4"><label class="form-label small" for="<?= $rid ?>-d">Rules turned off (IDs)</label>
                            <input class="form-control form-control-sm font-monospace" id="<?= $rid ?>-d" name="disabled_rules" value="<?= e(implode(' ', $set['disabled_rules'])) ?>" placeholder="e.g. 942100 920350"></div>
                        <div class="col-md-2"><button class="btn btn-sm btn-primary w-100">Save and check</button></div>
                    </form>
                <?= $this->modalEnd() ?><?php $editWindows[] = ob_get_clean(); ?>
                <?php endif; ?>
            <?php endforeach; ?>
            <?php if ($sites === []): ?><tr><td colspan="5" class="text-center py-4 wcp-muted">No websites yet.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
</div>
<?= implode('', $editWindows ?? []) ?>

<div class="wcp-panel mb-3" id="custom">
    <div class="fw-semibold mb-1">Custom rules</div>
    <div class="small wcp-muted mb-2">ModSecurity rule directives only (<code>SecRule</code>, <code>SecAction</code>, <code>SecMarker</code>, <code>SecRuleRemove…</code>, <code>SecRuleUpdate…</code>), each <code>SecRule</code>/<code>SecAction</code> with an <code>id</code> from 10000 to 99999. Programs (<code>exec</code>), file operators and includes are refused. Nginx checks the rules before they are used; a rule it rejects is not saved.</div>
    <form method="post" action="<?= e($this->url('security.modsec.custom')) ?>" data-wcp-busy>
        <?= $this->csrfField() ?>
        <textarea class="form-control font-monospace small mb-2" name="rules" rows="10" spellcheck="false" aria-label="Custom rules" placeholder="SecRule REQUEST_URI &quot;@beginsWith /xmlrpc.php&quot; &quot;id:10001,phase:1,deny,status:403,log,msg:'xmlrpc blocked'&quot;"<?= $canManage ? '' : ' readonly' ?>><?= e($custom) ?></textarea>
        <?php if ($canManage): ?><button class="btn btn-sm btn-primary">Save and check</button><?php endif; ?>
    </form>
</div>

<div class="wcp-panel" data-wcp-dt id="events">
    <div class="wcp-actionbar"><div class="fw-semibold">Recent events <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($events) ?></span></div>
        <div class="wcp-actionbar-end"><div class="wcp-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search events" data-wcp-dt-search><i class="bi bi-search" aria-hidden="true"></i></div></div></div>
    <?= $this->partial('partials/modsec_events', ['events' => $events, 'byDomain' => $byDomain, 'showHost' => true]) ?>
</div>
