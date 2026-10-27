<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $status */
/** @var array<string, mixed> $values */
/** @var string $whitelist */
/** @var array<string, string> $countries */
/** @var bool $geoLoaded */
/** @var list<array<string, mixed>> $report */
/** @var list<array<string, mixed>> $blocks */
/** @var array<string, string> $variables */
$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE) && ($user['role_name'] ?? '') === 'admin';
$dis = $canManage ? '' : ' disabled';
$v = $values;
$switch = function (string $key, string $label, string $help = '') use ($v, $dis): string {
    return '<div class="form-check form-switch mb-1"><input type="hidden" name="' . e($key) . '" value="0"><input class="form-check-input" type="checkbox" role="switch" id="h-' . e($key) . '" name="' . e($key) . '" value="1"' . (!empty($v[$key]) ? ' checked' : '') . $dis . '><label class="form-check-label fw-medium" for="h-' . e($key) . '">' . e($label) . '</label></div>'
        . ($help !== '' ? '<div class="small text-body-secondary mb-3">' . e($help) . '</div>' : '<div class="mb-3"></div>');
};
$number = function (string $key, string $label, int $min, int $max) use ($v, $dis): string {
    return '<div class="col-md-6 col-xl-4"><label class="form-label small fw-medium" for="h-' . e($key) . '">' . e($label) . '</label><input class="form-control form-control-sm" type="number" id="h-' . e($key) . '" name="' . e($key) . '" min="' . $min . '" max="' . $max . '" value="' . (int) $v[$key] . '" required' . $dis . '></div>';
};
$varList = '<ul class="small mb-0 wcp-hulk-vars">';
foreach ($variables as $name => $desc) {
    $varList .= '<li><code>%' . e($name) . '%</code> - ' . e($desc) . '</li>';
}
$varList .= '</ul>';
$marks = ['whitelist' => 'Whitelisted', 'none' => 'Not Specified', 'blacklist' => 'Blacklisted'];
$retention = (int) $v['hulk_retention'];
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>

<div class="wcp-panel mb-3">
    <p class="mb-2">cPHulk provides protection from brute force attacks against your web services.</p>
    <div class="d-flex flex-wrap align-items-center gap-2">
        <span class="fw-medium">Status:</span>
        <?php if (empty($v['hulk_enabled'])): ?><span class="wcp-state is-off">Disabled</span>
        <?php elseif ($status !== null && empty($status['available'])): ?><span class="wcp-state is-warn">Enabled (WhaCP sign-ins only: nftables is not installed)</span>
        <?php else: ?><span class="wcp-state is-ok">Enabled</span><?php endif; ?>
        <span class="small text-body-secondary">Watches WhaCP, webmail, SSH, IMAP/POP3, SMTP AUTH and FTP logins. <?= count($blocks) ?> block<?= count($blocks) === 1 ? '' : 's' ?> in force.</span>
    </div>
</div>

<div class="wcp-subtabs" role="tablist">
    <button class="active" type="button" role="tab" data-bs-toggle="tab" data-bs-target="#settings" aria-controls="settings" aria-selected="true">cPHulk Settings</button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#countries" aria-controls="countries" aria-selected="false">Countries Management</button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#report" aria-controls="report" aria-selected="false">Failed Logins Report <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($report) ?></span></button>
</div>

<div class="tab-content">
<div class="tab-pane fade show active" id="settings" role="tabpanel">
<form method="post" action="<?= e($this->url('security.hulk.save')) ?>" data-wcp-busy>
    <?= $this->csrfField() ?>
    <div class="wcp-panel mb-3">
        <?= $switch('hulk_enabled', 'Enable cPHulk Brute Force Protection') ?>
    </div>

    <div class="wcp-panel mb-3">
        <div class="fw-semibold mb-2">Username-based Protection</div>
        <?= $switch('hulk_user_enabled', 'Username-based Protection', 'Username-based protection tracks login attempts for user accounts. When disabled, cPHulk will not lock user accounts, but existing account locks will remain. Locked accounts cannot sign in to WhaCP until the lock ends.') ?>
        <div class="row g-3 mb-3">
            <?= $number('hulk_user_period', 'Brute Force Protection Period (in minutes)', 1, 1440) ?>
            <?= $number('hulk_user_max', 'Maximum Failures by Account', 1, 1000) ?>
        </div>
        <div class="mb-3">
            <div class="small fw-medium mb-1">Protection type</div>
            <div class="form-check"><input class="form-check-input" type="radio" name="hulk_user_scope" id="h-scope-local" value="local"<?= $v['hulk_user_scope'] === 'local' ? ' checked' : '' ?><?= $dis ?>><label class="form-check-label" for="h-scope-local">Apply protection to local addresses only</label></div>
            <div class="form-check"><input class="form-check-input" type="radio" name="hulk_user_scope" id="h-scope-all" value="all"<?= $v['hulk_user_scope'] !== 'local' ? ' checked' : '' ?><?= $dis ?>><label class="form-check-label" for="h-scope-all">Apply protection to local and remote addresses</label></div>
        </div>
        <?= $switch('hulk_user_lock_root', 'Allow username protection to lock the “root” user', 'Also applies to WhaCP administrator accounts. When off, failures for them count toward IP address blocks only.') ?>
    </div>

    <div class="wcp-panel mb-3">
        <div class="fw-semibold mb-2">IP Address-based Protection</div>
        <?= $switch('hulk_ip_enabled', 'IP Address-based Protection', 'IP Address-based protection tracks login attempts from specific IP addresses. When disabled, cPHulk will not block IP addresses, but existing blocks will remain.') ?>
        <div class="row g-3 mb-3">
            <?= $number('hulk_ip_period', 'IP Address-based Brute Force Protection Period (in minutes)', 1, 1440) ?>
            <?= $number('hulk_ip_max', 'Maximum Failures per IP Address', 1, 1000) ?>
        </div>
        <label class="form-label small fw-medium" for="h-hulk_ip_command">Command to Run When an IP Address Triggers Brute Force Protection</label>
        <input class="form-control form-control-sm font-monospace mb-1" id="h-hulk_ip_command" name="hulk_ip_command" maxlength="500" value="<?= e((string) $v['hulk_ip_command']) ?>"<?= $dis ?>>
        <div class="wcp-infobar my-2"><i class="bi bi-exclamation-triangle-fill" aria-hidden="true"></i><span>Warning: The command must complete within 15 seconds to avoid a timeout. It runs as the panel's own account (not root); values are passed shell-quoted.</span></div>
        <div class="small mb-1">The following variables may be used in commands:</div>
        <?= $varList ?>
        <div class="mt-3"><?= $switch('hulk_ip_firewall', 'Block IP addresses at the firewall level if they trigger brute force protection', 'At the firewall level the address cannot reach any service of the server (websites included). Otherwise it is blocked from WhaCP and the login services (SSH, FTP, IMAP/POP3, SMTP submission).') ?></div>
    </div>

    <div class="wcp-panel mb-3">
        <div class="fw-semibold mb-2">One-day Blocks</div>
        <div class="row g-3 mb-3">
            <?= $number('hulk_oneday_max', 'Maximum Failures per IP Address before the IP Address is Blocked for One Day', 1, 10000) ?>
        </div>
        <label class="form-label small fw-medium" for="h-hulk_oneday_command">Command to Run When an IP Address Triggers a One-Day Block</label>
        <input class="form-control form-control-sm font-monospace mb-2" id="h-hulk_oneday_command" name="hulk_oneday_command" maxlength="500" value="<?= e((string) $v['hulk_oneday_command']) ?>"<?= $dis ?>>
        <div class="small mb-1">The following variables may be used in commands:</div>
        <?= $varList ?>
        <div class="mt-3"><?= $switch('hulk_oneday_firewall', 'Block IP addresses at the firewall level if they trigger a one-day block') ?></div>
    </div>

    <div class="wcp-panel mb-3">
        <div class="fw-semibold mb-2">Login History</div>
        <div class="row g-3">
            <?= $number('hulk_retention', 'Duration for Retaining Failed Logins (in minutes)', 5, 10080) ?>
        </div>
    </div>

    <div class="wcp-panel mb-3">
        <div class="fw-semibold mb-2">Notifications</div>
        <?php foreach (['hulk_notify_root' => 'Send a notification upon successful root login when the IP address is not on the whitelist', 'hulk_notify_root_netblock' => 'Send a notification upon successful root login when the IP address is not on the whitelist, but from a known netblock', 'hulk_notify_brute' => 'Send a notification when the system detects a brute force user'] as $k => $l): ?>
            <div class="form-check mb-1"><input type="hidden" name="<?= e($k) ?>" value="0"><input class="form-check-input" type="checkbox" id="h-<?= e($k) ?>" name="<?= e($k) ?>" value="1"<?= !empty($v[$k]) ? ' checked' : '' ?><?= $dis ?>><label class="form-check-label" for="h-<?= e($k) ?>"><?= e($l) ?></label></div>
        <?php endforeach; ?>
    </div>

    <div class="wcp-panel mb-3">
        <label class="form-label fw-semibold" for="h-whitelist">Whitelisted IP addresses</label>
        <textarea class="form-control form-control-sm font-monospace" id="h-whitelist" name="hulk_whitelist" rows="4" placeholder="203.0.113.10&#10;198.51.100.0/24"<?= $dis ?>><?= e($whitelist) ?></textarea>
        <div class="small text-body-secondary mt-1">One address or network per line. These are never blocked or locked, and root logins from them are not reported. Add your own address.</div>
    </div>
    <?php if ($canManage): ?><button class="btn btn-primary">Save</button><?php endif; ?>
</form>
</div>

<div class="tab-pane fade" id="countries" role="tabpanel">
    <div class="wcp-panel mb-3">
        <p class="small mb-2">Whitelisted countries are never blocked. Blacklisted countries cannot sign in to WhaCP or reach the login services (SSH, FTP, IMAP/POP3, SMTP submission), or, if you choose so below, any service of the server.</p>
        <?php if (!$geoLoaded): ?>
            <div class="wcp-infobar mb-2"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>The country database is not loaded yet. It is downloaded automatically each month from DB-IP (IP to Country Lite, CC BY 4.0) while brute force protection is on. If the server cannot reach download.db-ip.com, download the CSV there and upload it here, or run <code>sudo -u whacp -E php /opt/whacp/panel/bin/whacp hulk:geo-import FILE</code>.</span></div>
        <?php else: ?>
            <div class="small text-body-secondary mb-2">Country data: <a href="https://db-ip.com" rel="noopener" target="_blank">IP Geolocation by DB-IP</a> (<?= e((string) $v['hulk_geo_updated']) ?>).</div>
        <?php endif; ?>
        <?php if ($canManage): ?>
            <form class="d-flex flex-wrap gap-2 align-items-center" method="post" enctype="multipart/form-data" action="<?= e($this->url('security.hulk.geo')) ?>" data-wcp-busy>
                <?= $this->csrfField() ?>
                <input class="form-control form-control-sm wcp-w-30" type="file" name="database" accept=".csv,.gz" aria-label="Country database file">
                <button class="btn btn-sm btn-outline-secondary">Upload country database</button>
            </form>
        <?php endif; ?>
    </div>
    <form method="post" action="<?= e($this->url('security.hulk.countries')) ?>" data-wcp-busy>
        <?= $this->csrfField() ?>
        <div class="wcp-panel" data-wcp-dt>
            <div class="wcp-actionbar">
                <?php if ($canManage): ?>
                    <label class="small" for="h-bulk">Mark selected as</label>
                    <select class="form-select form-select-sm w-auto" id="h-bulk" name="bulk">
                        <option value="">—</option><?php foreach ($marks as $k => $l): ?><option value="<?= e($k) ?>"><?= e($l) ?></option><?php endforeach; ?>
                    </select>
                    <button class="btn btn-sm btn-primary">Save</button>
                <?php endif; ?>
                <input class="form-control form-control-sm ms-auto wcp-w-30" type="search" placeholder="Search countries" aria-label="Search countries" data-wcp-dt-search>
            </div>
            <div class="px-3 py-2 border-bottom small">
                <span class="fw-medium me-2">Block blacklisted countries on</span>
                <div class="form-check form-check-inline"><input class="form-check-input" type="radio" name="scope" id="h-cc-login" value="login"<?= empty($v['hulk_countries_all']) ? ' checked' : '' ?><?= $dis ?>><label class="form-check-label" for="h-cc-login">WhaCP and the login services</label></div>
                <div class="form-check form-check-inline"><input class="form-check-input" type="radio" name="scope" id="h-cc-all" value="all"<?= !empty($v['hulk_countries_all']) ? ' checked' : '' ?><?= $dis ?>><label class="form-check-label" for="h-cc-all">every port, websites included (like CSF's CC_DENY)</label></div>
            </div>
            <div class="wcp-dtable table-responsive">
                <table class="table align-middle">
                    <thead><tr><th class="wcp-col-check"><input class="form-check-input" type="checkbox" aria-label="Select all visible countries" data-wcp-select-all="selected[]"<?= $dis ?>></th><th>Country</th><th>Code</th><?php foreach ($marks as $l): ?><th class="text-center"><?= e($l) ?></th><?php endforeach; ?></tr></thead>
                    <tbody>
                    <?php foreach (\WhaCP\Security\Countries::ALL as $code => $name): $cur = $countries[$code] ?? 'none'; ?>
                        <tr data-wcp-row>
                            <td><input class="form-check-input" type="checkbox" name="selected[]" value="<?= e($code) ?>" aria-label="Select <?= e($name) ?>"<?= $dis ?>></td>
                            <td><?= e($name) ?></td>
                            <td class="font-monospace small"><?= e($code) ?></td>
                            <?php foreach ($marks as $k => $l): ?>
                                <td class="text-center"><input class="form-check-input" type="radio" name="country[<?= e($code) ?>]" value="<?= e($k) ?>" aria-label="<?= e($l . ': ' . $name) ?>"<?= $cur === $k ? ' checked' : '' ?><?= $dis ?>></td>
                            <?php endforeach; ?>
                        </tr>
                    <?php endforeach; ?>
                    <tr class="d-none" data-wcp-dt-empty><td colspan="<?= 3 + count($marks) ?>" class="text-center py-4 wcp-muted">No country matches.</td></tr>
                    </tbody>
                </table>
            </div>
            <div class="wcp-pager" data-wcp-dt-pager></div>
        </div>
    </form>
</div>

<div class="tab-pane fade" id="report" role="tabpanel">
    <?php if ($blocks !== []): ?>
    <div class="wcp-panel mb-3">
        <div class="fw-semibold mb-2">Blocks in force</div>
        <div class="wcp-dtable table-responsive">
            <table class="table align-middle">
                <thead><tr><th>Blocked</th><th>Type</th><th>Reason</th><th>Failures</th><th>Until</th><th></th></tr></thead>
                <tbody>
                <?php foreach ($blocks as $b): ?>
                    <tr>
                        <td class="font-monospace"><?= e((string) ($b['ip'] ?? $b['user'])) ?></td>
                        <td class="small"><?= e(['ip' => 'IP address', 'oneday' => 'IP address (one day)', 'user' => 'Account'][$b['kind']] ?? (string) $b['kind']) ?><?= !empty($b['firewall']) ? ' · firewall level' : '' ?></td>
                        <td class="small"><?= e((string) $b['reason']) ?> (<?= e((string) $b['authservice']) ?>)</td>
                        <td><?= (int) $b['failures'] ?></td>
                        <td class="small"><?= e(gmdate('Y-m-d H:i', (int) $b['expires_at'])) ?> UTC</td>
                        <td class="text-end"><?php if ($canManage): ?><form method="post" action="<?= e($this->url('security.hulk.unblock')) ?>"><?= $this->csrfField() ?><input type="hidden" name="id" value="<?= (int) $b['id'] ?>"><button class="btn btn-sm btn-outline-secondary">Unblock</button></form><?php endif; ?></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    </div>
    <?php endif; ?>
    <div class="wcp-panel" data-wcp-dt>
        <div class="wcp-actionbar">
            <div>
                <div class="fw-semibold">Failed Logins Report</div>
                <div class="small text-body-secondary">The system counts Failed Logins for the duration of the specified period, which is currently set to <?= $retention ?> minutes.</div>
            </div>
            <input class="form-control form-control-sm ms-auto wcp-w-30" type="search" placeholder="Search" aria-label="Search failed logins" data-wcp-dt-search>
            <?php if ($canManage): ?>
                <form method="post" action="<?= e($this->url('security.hulk.clear')) ?>" data-wcp-confirm="Remove every block and clear the report?"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger">Remove Blocks and clear report</button></form>
            <?php endif; ?>
        </div>
        <div class="wcp-dtable table-responsive">
            <table class="table align-middle">
                <thead><tr><th>User</th><th>IP Address</th><th>Country</th><th>Service</th><th>Authentication Service</th><th>Login Time</th><th>Expiration Time</th><th>Minutes Remaining</th></tr></thead>
                <tbody>
                <?php foreach ($report as $r): ?>
                    <tr data-wcp-row>
                        <td class="font-monospace small"><?= e((string) $r['user']) ?></td>
                        <td class="font-monospace small"><?= e((string) $r['ip']) ?></td>
                        <td class="small"><?= e((string) ($r['country_name'] ?: '—')) ?></td>
                        <td class="small"><?= e((string) $r['service']) ?></td>
                        <td class="small"><?= e((string) $r['authservice']) ?></td>
                        <td class="small"><?= e(gmdate('Y-m-d H:i:s', (int) $r['time'])) ?></td>
                        <td class="small"><?= e(gmdate('Y-m-d H:i:s', (int) $r['expires'])) ?></td>
                        <td><?= (int) $r['minutes_left'] ?></td>
                    </tr>
                <?php endforeach; ?>
                <tr class="<?= $report === [] ? '' : 'd-none' ?>" data-wcp-dt-empty><td colspan="8" class="text-center py-4 wcp-muted"><?= $report === [] ? 'No failed logins in this period.' : 'No failed login matches.' ?></td></tr>
                </tbody>
            </table>
        </div>
        <div class="wcp-pager" data-wcp-dt-pager></div>
    </div>
</div>
</div>
