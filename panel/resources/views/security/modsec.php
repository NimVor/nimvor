<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed>|null $status
 * @var string $custom
 * @var list<array<string, mixed>> $events
 * @var list<array<string, mixed>> $sites
 * @var list<array<string, mixed>> $installs
 */
use NimVor\Services\ModSecurityService;

$canManage = $this->can(\NimVor\Security\Permissions::SECURITY_MANAGE);
$cfg = (array) ($status['config'] ?? []);
$crs = (array) ($status['crs'] ?? []);
$crsCfg = (array) ($cfg['crs'] ?? []);
$loaded = (bool) ($status['module_loaded'] ?? false);
$conn = (array) ($status['connector'] ?? []);
$connState = (string) ($conn['state'] ?? ($loaded ? 'functional' : 'missing'));
$installs ??= [];
$rules = ModSecurityService::splitRules((string) ($custom ?? ''));
// A failed build's error carries the whole compiler output: the table shows
// its error lines (or its last lines), the rest is behind "Show the full output".
$errSummary = static function (string $err): array {
    $lines = array_values(array_filter(array_map('trim', preg_split('/\R/', $err) ?: []), static fn (string $l): bool => $l !== ''));
    if (count($lines) <= 3 && mb_strlen($err) <= 400) {
        return [$err, false];
    }
    $hits = array_values(array_filter($lines, static fn (string $l): bool => (bool) preg_match('/\b(error|failed|fatal|not found|cannot|no such)\b/i', $l) && !preg_match('/^checking\b/i', $l)));
    $pick = $hits !== [] ? array_slice($hits, -3) : array_slice($lines, -3);
    $short = implode(' … ', array_map(static fn (string $l): string => mb_strimwidth($l, 0, 220, '…'), $pick));
    return [$lines[0] !== $pick[0] ? mb_strimwidth($lines[0], 0, 160, '…') . ' — ' . $short : $short, true];
};
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

<div class="nim-panel mb-3">
    <div class="nim-statusbar">
        <span class="fw-medium">Web application firewall (ModSecurity)</span>
        <span><?php if ($connState === 'functional'): ?><span class="nim-state is-ok" title="<?= e((string) ($conn['modules'][0]['path'] ?? '')) ?>">Nginx connector working</span><?php elseif ($connState === 'installed'): ?><span class="nim-state is-warn" title="<?= e((string) ($conn['reason'] ?? '')) ?>">Connector installed, not usable</span><?php else: ?><span class="nim-state is-off">Nginx connector not installed</span><?php endif; ?>
            <?php if ($connState === 'functional'): ?><span class="small nim-muted"><?= !empty($conn['package']) ? e('package ' . $conn['package']) : 'built or installed without a package' ?></span><?php endif; ?></span>
        <span class="small"><span class="nim-muted">OWASP CRS</span> <?= ($crs['available'] ?? false) ? e((string) ($crs['version'] ?? '?')) : 'not installed' ?></span>
        <span><?= $active ? '<span class="nim-state is-ok">Active</span>' : '<span class="nim-state is-off">Off</span>' ?></span>
        <span class="small"><span class="nim-muted">Audit log</span> <?= e(format_bytes((int) ($status['audit_log_bytes'] ?? 0))) ?></span>
        <span class="ms-auto d-flex gap-2">
        <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#ms-settings"><i class="bi bi-sliders me-1"></i>Server-wide settings</button>
        <?php if ($canManage && $loaded): ?>
            <form method="post" action="<?= e($this->url('security.modsec.update')) ?>" data-nim-busy data-nim-confirm="Update the rule packages now? Nginx is checked and reloaded afterwards; if the new rules do not load, the Core Rule Set is turned off and the websites keep working."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-repeat me-1"></i>Update rules</button></form>
        <?php endif; ?>
        </span>
    </div>
</div>

<?php if ($connState !== 'functional' || !($crs['available'] ?? false)): ?>
    <div class="nim-infobar" id="install"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>
        <?php if ($connState === 'installed'): ?><strong>The connector is present but not usable:</strong> <?= e((string) ($conn['reason'] ?? '')) ?><br><?php endif; ?>
        <?php if ($connState !== 'functional'): ?>
            <strong>Install ModSecurity</strong> uses the distribution's package when installing it would not touch Nginx itself, and otherwise builds the official connector for the exact Nginx installed here. Everything is backed up first; if any step fails (build, libraries, loading, <code>nginx -t</code>, reload) it is all rolled back and Nginx keeps working as before. The Nginx program itself is never changed.
        <?php else: ?>
            The OWASP Core Rule Set is not installed. It is installed from the distribution's package, or from the official release, and starts in <strong>detection only</strong>.
        <?php endif; ?>
    </span></div>
<?php endif; ?>

<?php if ($canManage): ?>

<div class="nim-subtabs" role="tablist">
    <button class="active" type="button" role="tab" data-bs-toggle="tab" data-bs-target="#sites" aria-controls="sites" aria-selected="true">Websites <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($sites) ?></span></button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#events" aria-controls="events" aria-selected="false">Recent events <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($events) ?></span></button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#vendors" aria-controls="vendors" aria-selected="false">Vendors</button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#custom" aria-controls="custom" aria-selected="false">Custom rules <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($rules) ?></span></button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#installs" aria-controls="installs" aria-selected="false">Installation</button>
</div>
<div class="tab-content">
<div class="tab-pane fade show active" id="sites" role="tabpanel">
<div class="nim-panel" data-nim-dt>
    <div class="nim-actionbar"><div class="fw-semibold">Websites</div>
        <div class="nim-actionbar-end"><div class="nim-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search websites" data-nim-dt-search><i class="bi bi-search" aria-hidden="true"></i></div></div></div>
    <div class="nim-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Website</th><th>Mode</th><th>Rule vendors</th><th>Rules turned off</th><th></th></tr></thead>
            <tbody>
            <?php foreach ($sites as $s): $set = ModSecurityService::siteSetting($status, $s); [$ml, $mc] = $modeBadge[$set['mode']] ?? $modeBadge['off']; $rid = 'ms-edit-' . (int) $s['id']; ?>
                <tr data-nim-row>
                    <td><a class="fw-semibold" href="<?= e($this->url('sites.firewall', ['id' => (int) $s['id']])) ?>"><?= e($s['domain']) ?></a><?php if (($s['owner_name'] ?? null) !== null): ?><div class="small nim-muted"><?= e($s['owner_name']) ?></div><?php endif; ?></td>
                    <td><span class="nim-state <?= e($mc) ?>"><?= e($ml) ?></span></td>
                    <td class="small"><?= e(implode(', ', array_map(static fn (string $v): string => $vendorNames[$v] ?? $v, array_values(array_diff(['crs', 'custom'], $set['exclude']))))) ?: '—' ?></td>
                    <td class="small font-monospace"><?= $set['disabled_rules'] === [] ? '—' : e(implode(' ', $set['disabled_rules'])) ?></td>
                    <td class="text-end"><?php if ($canManage): ?><button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#<?= $rid ?>">Edit</button><?php endif; ?></td>
                </tr>
                <?php if ($canManage): ?>
                <?php ob_start(); ?><?= $this->modalStart($rid, 'ModSecurity for ' . $s['domain']) ?>
                    <form class="row g-2 align-items-end" method="post" action="<?= e($this->url('security.modsec.site', ['id' => (int) $s['id']])) ?>" data-nim-busy>
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
            <?php if ($sites === []): ?><tr><td colspan="5" class="text-center py-4 nim-muted">No websites yet.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
</div>
<?= implode('', $editWindows ?? []) ?>

</div>
<div class="tab-pane fade" id="events" role="tabpanel">
<div class="nim-panel" data-nim-dt>
    <div class="nim-actionbar"><div class="fw-semibold">Recent events <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($events) ?></span></div>
        <div class="nim-actionbar-end"><div class="nim-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search events" data-nim-dt-search><i class="bi bi-search" aria-hidden="true"></i></div></div></div>
    <?= $this->partial('partials/modsec_events', ['events' => $events, 'byDomain' => $byDomain, 'showHost' => true]) ?>
</div>
</div>
<div class="tab-pane fade" id="vendors" role="tabpanel">
<div class="nim-panel">
            <div class="fw-semibold mb-2">Rule vendors</div>
            <div class="table-responsive">
                <table class="table align-middle mb-2">
                    <thead><tr><th>Vendor</th><th>Rule IDs</th><th>State</th></tr></thead>
                    <tbody>
                    <?php foreach ((array) ($status['vendors'] ?? []) as $v):
                        $on = $v['id'] === 'crs' ? (!empty($crsCfg['enabled']) && ($crs['available'] ?? false)) : (!empty($cfg['custom']['enabled']) && (int) ($status['custom_rules'] ?? 0) > 0); ?>
                        <tr>
                            <td><div class="fw-semibold"><?= e($v['name']) ?><?= $v['id'] === 'crs' && ($crs['version'] ?? '') !== '' ? ' <span class="small nim-muted">' . e($crs['version']) . '</span>' : '' ?></div><div class="small nim-muted"><?= e($v['description']) ?></div></td>
                            <td class="text-nowrap small font-monospace"><?= (int) $v['first_id'] ?>–<?= (int) $v['last_id'] ?></td>
                            <td><?php if ($v['id'] === 'crs' && !($crs['available'] ?? false)): ?><span class="nim-state is-off">Not installed</span><?php else: ?><span class="nim-state <?= $on ? 'is-ok' : 'is-off' ?>"><?= $on ? 'Enabled' : 'Disabled' ?></span><?php endif; ?></td>
                        </tr>
                    <?php endforeach; ?>
                    </tbody>
                </table>
            </div>
            <div class="small nim-muted">Each website can leave out a vendor or single rules (below). Commercial rule sets need their vendor's licence and are not bundled. The Core Rule Set is updated with its package (<em>Update rules</em>, or the server's package updates).</div>
</div>
</div>
<div class="tab-pane fade" id="custom" role="tabpanel">
<div class="nim-panel" data-nim-dt>
    <div class="nim-actionbar">
        <div class="fw-semibold">Custom rules <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($rules) ?></span>
            <span class="nim-state ms-1 <?= !empty($cfg['custom']['enabled']) ? 'is-ok' : 'is-off' ?>"><?= !empty($cfg['custom']['enabled']) ? 'in use' : 'not in use (Server-wide settings)' ?></span></div>
        <div class="nim-actionbar-end">
            <div class="nim-search"><input class="form-control form-control-sm" type="search" placeholder="Search rules" aria-label="Search rules" data-nim-dt-search><i class="bi bi-search" aria-hidden="true"></i></div>
            <?php if ($canManage): ?><button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#ms-rule-new"><i class="bi bi-plus-lg me-1"></i>New rule</button><?php endif; ?>
        </div>
    </div>
    <div class="nim-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>ID</th><th>Description</th><th>Phase</th><th>Action</th><th>Rule</th><th></th></tr></thead>
            <tbody>
            <?php foreach ($rules as $i => $r): $rm = 'ms-rule-' . $i; ?>
                <tr data-nim-row>
                    <td class="font-monospace"><?= $r['id'] !== null ? (int) $r['id'] : '—' ?></td>
                    <td class="small"><?= e($r['comment'] !== '' ? $r['comment'] : ($r['msg'] !== '' ? $r['msg'] : '—')) ?></td>
                    <td class="small"><?= e($r['phase'] !== '' ? $r['phase'] : '—') ?></td>
                    <td><?php if ($r['action'] !== ''): ?><span class="nim-state <?= in_array($r['action'], ['deny', 'block', 'drop'], true) ? 'is-error' : 'is-off' ?>"><?= e($r['action']) ?></span><?php else: ?>—<?php endif; ?></td>
                    <td class="small font-monospace text-break nim-rule-text"><?= e(mb_strimwidth(preg_replace('/\s+/', ' ', $r['text']), 0, 160, '…')) ?></td>
                    <td class="text-end text-nowrap"><?php if ($canManage): ?>
                        <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#<?= $rm ?>">Edit</button>
                        <form class="d-inline" method="post" action="<?= e($this->url('security.modsec.rule')) ?>" data-nim-busy data-nim-confirm="Delete this rule? The remaining rules are checked by Nginx again.">
                            <?= $this->csrfField() ?><input type="hidden" name="op" value="delete"><input type="hidden" name="index" value="<?= (int) $i ?>"><input type="hidden" name="sha" value="<?= hash('sha256', $r['text']) ?>">
                            <button class="btn btn-sm btn-outline-danger">Delete</button>
                        </form>
                    <?php endif; ?></td>
                </tr>
            <?php endforeach; ?>
            <tr class="<?= $rules === [] ? '' : 'd-none' ?>" data-nim-dt-empty><td colspan="6" class="text-center py-4 nim-muted">No custom rules yet.</td></tr>
            </tbody>
        </table>
    </div>
    <div class="nim-pager" data-nim-dt-pager></div>
    <p class="small nim-muted mt-2 mb-0">ModSecurity rule directives only (<code>SecRule</code>, <code>SecAction</code>, <code>SecMarker</code>, <code>SecRuleRemove…</code>, <code>SecRuleUpdate…</code>), each <code>SecRule</code>/<code>SecAction</code> with an <code>id</code> from 10000 to 99999. Programs (<code>exec</code>), file operators and includes are refused. Nginx checks the rules before they are used; a rule it rejects is not saved.</p>
    <?php if ($canManage): ?>
    <details class="mt-2"><summary class="small">Edit all rules as text</summary>
        <form class="mt-2" method="post" action="<?= e($this->url('security.modsec.custom')) ?>" data-nim-busy>
            <?= $this->csrfField() ?>
            <textarea class="form-control font-monospace small mb-2" name="rules" rows="10" spellcheck="false" aria-label="Custom rules"><?= e($custom) ?></textarea>
            <button class="btn btn-sm btn-outline-primary">Save and check</button>
        </form>
    </details>
    <?php endif; ?>
</div>
<?php if ($canManage): ?>
<?php
$ruleForm = function (string $mid, string $title, ?int $index, array $r) use ($rules): void { ?>
    <?= $this->modalStart($mid, $title, false, 'modal-lg') ?>
        <form method="post" action="<?= e($this->url('security.modsec.rule')) ?>" data-nim-busy>
            <?= $this->csrfField() ?>
            <?php if ($index !== null): ?><input type="hidden" name="index" value="<?= $index ?>"><input type="hidden" name="sha" value="<?= hash('sha256', $r['text']) ?>"><?php endif; ?>
            <label class="form-label small" for="<?= $mid ?>-c">Description <span class="nim-muted">(optional, kept as a comment)</span></label>
            <input class="form-control form-control-sm mb-2" id="<?= $mid ?>-c" name="comment" maxlength="500" value="<?= e($r['comment']) ?>">
            <label class="form-label small" for="<?= $mid ?>-r">Rule</label>
            <textarea class="form-control font-monospace small" id="<?= $mid ?>-r" name="rule" rows="5" spellcheck="false" required><?= e($r['text']) ?></textarea>
            <div class="form-text">One directive; long rules may continue on the next line after a backslash. Example: <code>SecRule REQUEST_URI "@beginsWith /xmlrpc.php" "id:<?= ModSecurityService::nextRuleId($rules) ?>,phase:1,deny,status:403,log,msg:'xmlrpc blocked'"</code></div>
            <div class="modal-footer px-0 pb-0"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-sm btn-primary">Save and check</button></div>
        </form>
    <?= $this->modalEnd() ?>
<?php };
$ruleForm('ms-rule-new', 'New custom rule', null, ['text' => 'SecRule REQUEST_URI "@beginsWith /example" "id:' . ModSecurityService::nextRuleId($rules) . ',phase:1,deny,status:403,log,msg:\'example\'"', 'comment' => '']);
foreach ($rules as $i => $r) {
    $ruleForm('ms-rule-' . $i, 'Custom rule' . ($r['id'] !== null ? ' ' . $r['id'] : ''), $i, $r);
}
?>
<?php endif; ?>
</div>
<div class="tab-pane fade" id="installs" role="tabpanel">
<div class="nim-panel">
    <div class="nim-actionbar">
        <div class="fw-semibold">Installation</div>
        <div class="nim-actionbar-end d-flex flex-wrap gap-2">
            <?php if ($connState !== 'functional' || empty($conn['package'])): ?>
                <button type="button" class="btn btn-sm <?= $connState === 'functional' ? 'btn-outline-secondary' : 'btn-primary' ?>" data-bs-toggle="modal" data-bs-target="#ms-install"><i class="bi bi-box-arrow-in-down me-1"></i><?= $connState === 'functional' ? 'Upgrade connector' : 'Install ModSecurity' ?></button>
            <?php endif; ?>
            <?php if (!($crs['available'] ?? false)): ?>
                <form method="post" action="<?= e($this->url('security.modsec.crs')) ?>" data-nim-busy data-nim-confirm="Install the OWASP Core Rule Set? It comes from the distribution's package, or from the official release (checksum-verified). It is turned on in detection only: websites set to block only log until you allow blocking."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-primary"><i class="bi bi-shield-plus me-1"></i>Install Core Rule Set</button></form>
            <?php endif; ?>
        </div>
    </div>
    <?php if ($installs !== []): ?>
    <div class="nim-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Started</th><th>What</th><th class="nim-col-result">Result</th><th>Sources</th><th></th></tr></thead>
            <tbody>
            <?php foreach (array_slice($installs, 0, 20) as $in): $st = (string) $in['status']; ?>
                <tr>
                    <td class="small text-nowrap"><?= e(str_replace('T', ' ', substr((string) $in['started_at'], 0, 16))) ?> UTC<div class="nim-muted font-monospace small"><?= e((string) $in['id']) ?></div></td>
                    <td class="small"><?= e((string) ($in['method'] ?? '')) ?><?php if (!empty($in['nginx_version'])): ?><div class="nim-muted">Nginx <?= e((string) $in['nginx_version']) ?><?= !empty($in['os']) ? ' · ' . e((string) $in['os']) : '' ?></div><?php endif; ?></td>
                    <td class="small">
                        <?php if ($st === 'committed'): ?><span class="nim-state is-ok">Done</span><?= !empty($in['can_roll_back']) ? ' <span class="nim-muted">in effect</span>' : '' ?>
                        <?php elseif ($st === 'rolled_back'): ?><span class="nim-state is-warn">Rolled back</span>
                        <?php elseif ($st === 'running'): ?><span class="nim-state is-warn">Running</span>
                        <?php else: ?><span class="nim-state is-error">Rollback incomplete</span><?php endif; ?>
                        <?php if (!empty($in['error'])): [$errShort, $errLong] = $errSummary((string) $in['error']); ?>
                            <div class="text-break mt-1"><?= e($errShort) ?></div>
                            <?php if ($errLong): ?><details class="mt-1"><summary class="small">Show the full output</summary><pre class="nim-db-create small mb-0 mt-1 nim-update-log"><?= e((string) $in['error']) ?></pre></details><?php endif; ?>
                        <?php endif; ?>
                        <?php if ($st === 'rollback_failed'): ?><div>Backup for manual recovery: <code><?= e((string) $in['folder']) ?>/snapshot</code></div><?php endif; ?>
                    </td>
                    <td class="small"><?php foreach ((array) ($in['sources'] ?? []) as $src): $src = (array) $src; ?><div><?= e(trim($src['name'] . ' ' . ($src['version'] ?? ''))) ?> <span class="nim-muted">— <?= e((string) ($src['origin'] ?? '')) ?></span></div><?php endforeach; ?><?php if (!empty($in['module'])): ?><div class="font-monospace nim-muted text-break"><?= e((string) $in['module']) ?></div><?php endif; ?></td>
                    <td class="text-end"><?php if (!empty($in['can_roll_back'])): ?>
                        <form method="post" action="<?= e($this->url('security.modsec.rollback', ['id' => (string) $in['id']])) ?>" data-nim-busy data-nim-confirm="Roll back this installation? Its files are put back as they were before it, packages it added are removed, then Nginx is checked and reloaded. If Nginx would not accept the result (for example while ModSecurity is on and the connector would go), nothing changes."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger">Roll back</button></form>
                    <?php endif; ?></td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
    <?php endif; ?>
    <p class="small nim-muted mb-0 mt-2">Every installation first saves a copy of the Nginx configuration, the ModSecurity and rule configuration and the linker configuration; records are kept <?= (int) ($retention ?? 30) ?> days (Settings), the one in effect always.</p>
</div>

<?= $this->modalStart('ms-install', $connState === 'functional' ? 'Upgrade the ModSecurity connector' : 'Install ModSecurity', false, 'modal-dialog-centered') ?>
    <form method="post" action="<?= e($this->url('security.modsec.install')) ?>" data-nim-busy>
        <?= $this->csrfField() ?>
        <?php if ($connState === 'functional'): ?><input type="hidden" name="upgrade" value="1"><p class="small">The connector in use stays in place until the new one works; if anything fails it stays in effect.</p><?php endif; ?>
        <label class="form-label small" for="ms-method">How</label>
        <select class="form-select form-select-sm mb-2" id="ms-method" name="method">
            <option value="auto">Package when it is compatible, otherwise build from source (recommended)</option>
            <option value="source">Always build from source</option>
        </select>
        <label class="form-label small" for="ms-sha">SHA-256 of Nginx's source <span class="nim-muted">(only when Nginx was not installed from a package)</span></label>
        <input class="form-control form-control-sm font-monospace mb-2" id="ms-sha" name="nginx_source_sha256" pattern="[0-9a-fA-F]{64}" placeholder="from nginx.org, for nginx-<?= e((string) ($conn['nginx_version'] ?? 'x.y.z')) ?>.tar.gz" autocomplete="off" spellcheck="false">
        <p class="small nim-muted">A build installs compilers and development packages (removed again if the installation fails), downloads the pinned ModSecurity releases (checksum-verified) and Nginx's own source, and builds as a temporary unprivileged account. It takes a few minutes; you can close this page.</p>
        <div class="modal-footer px-0 pb-0"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-sm btn-primary"><?= $connState === 'functional' ? 'Upgrade' : 'Install' ?></button></div>
    </form>
<?= $this->modalEnd() ?>
<?php endif; ?>

</div>
</div>

<?= $this->modalStart('ms-settings', 'ModSecurity: server-wide settings', false, 'modal-dialog-centered') ?>
            <form method="post" action="<?= e($this->url('security.modsec.configure')) ?>" data-nim-busy>
                <?= $this->csrfField() ?>
                <fieldset<?= $canManage ? '' : ' disabled' ?>>
                <div class="form-check form-switch mb-1">
                    <input type="hidden" name="enabled" value="0">
                    <input class="form-check-input" type="checkbox" role="switch" id="ms-on" name="enabled" value="1"<?= !empty($cfg['enabled']) ? ' checked' : '' ?><?= $loaded ? '' : ' disabled' ?>>
                    <label class="form-check-label" for="ms-on">ModSecurity on</label>
                </div>
                <div class="form-text mt-0 mb-3">Loads the rules once for all websites. Each website is then off, detection only or on (Websites tab); off here filters no website.</div>
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
                <div class="form-check form-switch mb-1">
                    <input type="hidden" name="detection_only" value="0">
                    <input class="form-check-input" type="checkbox" role="switch" id="ms-detect" name="detection_only" value="1"<?= !empty($cfg['detection_only']) ? ' checked' : '' ?>>
                    <label class="form-check-label" for="ms-detect">Detection only (do not block yet)</label>
                </div>
                <div class="form-text mt-0 mb-3">Websites set to “On” only log what they would block. Check the Recent events tab for false positives, then turn this off to start blocking.</div>
                <div class="form-check form-switch mb-3">
                    <input type="hidden" name="custom_enabled" value="0">
                    <input class="form-check-input" type="checkbox" role="switch" id="ms-custom" name="custom_enabled" value="1"<?= !empty($cfg['custom']['enabled']) ? ' checked' : '' ?>>
                    <label class="form-check-label" for="ms-custom">Custom rules (<?= (int) ($status['custom_rules'] ?? 0) ?>)</label>
                </div>
                <?php if ($canManage): ?><div class="modal-footer px-0 pb-0"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-sm btn-primary">Save and check</button></div><?php endif; ?>
                </fieldset>
            </form>
<?= $this->modalEnd() ?>
