<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $fw */
$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE);
$rules = (array) ($fw['rules'] ?? []);
$errors ??= [];
// Listening ports: "tcp/22" => reachable from outside (false: loopback only).
$listening = [];
foreach ((array) ($fw['listening'] ?? []) as $l) {
    $key = ($l['proto'] ?? '') . '/' . (int) ($l['port'] ?? 0);
    $listening[$key] = ($listening[$key] ?? false) || empty($l['local']);
}
// Well-known service names used in rules.
$named = ['openssh' => [22], 'ssh' => [22], 'http' => [80], 'https' => [443], 'nginx full' => [80, 443], 'nginx http' => [80], 'nginx https' => [443], 'smtp' => [25], 'submission' => [587], 'smtps' => [465], 'imap' => [143], 'imaps' => [993], 'pop3' => [110], 'pop3s' => [995], 'dns' => [53], 'ftp' => [21], 'mysql' => [3306]];
/** @return list<int>|null ports of a rule; null = not a port (or too many to check) */
$ports = static function (string $p) use ($named): ?array {
    if ($p === '') {
        return null;
    }
    if (isset($named[strtolower($p)])) {
        return $named[strtolower($p)];
    }
    $out = [];
    foreach (explode(',', $p) as $part) {
        if (preg_match('/^(\d+)[:-](\d+)$/', $part, $m)) {
            if ((int) $m[2] - (int) $m[1] > 1000) {
                return null;
            }
            $out = array_merge($out, range((int) $m[1], (int) $m[2]));
        } elseif (ctype_digit($part)) {
            $out[] = (int) $part;
        } else {
            return null;
        }
    }
    return $out;
};
$state = static function (array $r) use ($ports, $listening): ?string {
    $list = $ports((string) ($r['port'] ?? ''));
    if ($list === null) {
        return null;
    }
    $protos = ($r['proto'] ?? '') === '' ? ['tcp', 'udp'] : [(string) $r['proto']];
    $local = false;
    foreach ($list as $p) {
        foreach ($protos as $pr) {
            if (isset($listening[$pr . '/' . $p])) {
                if ($listening[$pr . '/' . $p]) {
                    return 'listening';
                }
                $local = true;
            }
        }
    }
    return $local ? 'local' : 'idle';
};
$portRules = count(array_filter($rules, static fn ($r) => ($r['port'] ?? '') !== ''));
$ipRules = count($rules) - $portRules;
$strategy = ['allow' => ['Allow', 'wcp-good'], 'limit' => ['Allow (rate-limited)', 'wcp-good'], 'deny' => ['Deny', 'wcp-bad'], 'reject' => ['Reject', 'wcp-bad']];
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($fw !== [] && empty($fw['installed'])): ?>
    <div class="wcp-infobar"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>The firewall (<?= ($fw['backend'] ?? '') === 'firewalld' ? 'firewalld' : 'UFW' ?>) is not installed. Install it from <a href="<?= e($this->url('packages')) ?>">Packages</a>.</span></div>
<?php elseif ($fw !== []): ?>
<?php $required = (array) ($fw['required'] ?? []); $myIp ??= ''; $active = !empty($fw['active']); ?>
<div class="wcp-panel mb-3">
    <div class="wcp-statusbar">
        <?php if ($canManage && $active): ?>
            <form method="post" action="<?= e($this->url('security.firewall.toggle')) ?>" data-wcp-confirm="Turn off the firewall? Every listening service becomes reachable.">
                <?= $this->csrfField() ?><input type="hidden" name="enabled" value="0">
                <div class="form-check form-switch">
                    <label class="form-check-label fw-medium" for="fw-on">Firewall</label>
                    <input class="form-check-input" type="checkbox" role="switch" id="fw-on" data-wcp-switch-submit checked>
                </div>
            </form>
        <?php else: ?>
            <span class="fw-medium">Firewall</span>
        <?php endif; ?>
        <span><?= $active ? '<span class="wcp-state is-ok">On</span>' : '<span class="wcp-state is-error">Off</span>' ?></span>
        <span class="small"><span class="wcp-muted">Backend</span> <?= e(($fw['backend'] ?? 'ufw') === 'firewalld' ? 'firewalld' : 'UFW') ?></span>
        <?php if (!empty($fw['default_incoming'])): ?><span class="small"><span class="wcp-muted">Incoming</span> <?= e((string) $fw['default_incoming']) ?> · <span class="wcp-muted">Outgoing</span> <?= e((string) ($fw['default_outgoing'] ?? '?')) ?></span><?php endif; ?>
        <span class="small"><span class="wcp-muted">SSH port</span> <?= (int) ($fw['ssh_port'] ?? 22) ?> <?= !empty($fw['ssh_allowed']) ? '<i class="bi bi-check-circle-fill wcp-good" title="Allowed"></i>' : '<i class="bi bi-exclamation-triangle-fill wcp-warn" title="No rule allows SSH yet"></i>' ?></span>
    </div>
</div>

<?php if ($required !== []): ?>
<div class="wcp-panel mb-3" id="fw-required">
    <div class="fw-semibold mb-1">Ports WhaCP keeps open</div>
    <div class="small text-body-secondary mb-2">SSH and the panel are always opened before the firewall is turned on, and their last rule cannot be deleted while it is on. Service ports are those of services listening on this server.</div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Port</th><th>Protocol</th><th>Used by</th><th>Status</th><?php if ($canManage && $active): ?><th></th><?php endif; ?></tr></thead>
            <tbody>
            <?php foreach ($required as $p): ?>
                <tr>
                    <td class="font-monospace"><?= e((string) $p['port']) ?><?php if (!empty($p['from'])): ?> <span class="small text-body-secondary">from <?= e((string) $p['from']) ?></span><?php endif; ?></td>
                    <td class="small"><?= e(strtoupper((string) $p['proto'])) ?></td>
                    <td><?= e((string) $p['service']) ?><?php if (!empty($p['critical'])): ?> <span class="badge text-bg-light border fw-normal">access</span><?php endif; ?></td>
                    <td><?= !empty($p['open']) ? '<span class="wcp-state is-ok">Allowed</span>' : '<span class="wcp-state is-warn">' . ($active ? 'Not allowed' : 'Opened when turned on') . '</span>' ?></td>
                    <?php if ($canManage && $active): ?>
                        <td class="text-end">
                            <?php if (empty($p['open'])): ?>
                                <form method="post" action="<?= e($this->url('security.firewall.add')) ?>" data-wcp-busy>
                                    <?= $this->csrfField() ?>
                                    <input type="hidden" name="action" value="allow"><input type="hidden" name="port" value="<?= e((string) $p['port']) ?>"><input type="hidden" name="proto" value="<?= e((string) $p['proto']) ?>"><input type="hidden" name="from" value="<?= e((string) ($p['from'] ?? '')) ?>"><input type="hidden" name="comment" value="<?= e(substr(trim((string) preg_replace(['/[^A-Za-z0-9 ._:\/-]+/', '/ {2,}/'], ' ', (string) $p['service'])), 0, 60)) ?>">
                                    <button class="btn btn-sm btn-outline-primary" aria-label="Allow <?= e($p['port'] . '/' . $p['proto']) ?>">Allow</button>
                                </form>
                            <?php endif; ?>
                        </td>
                    <?php endif; ?>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
</div>
<?php endif; ?>

<?php if ($canManage && !$active): ?>
<form class="wcp-panel mb-3" method="post" action="<?= e($this->url('security.firewall.toggle')) ?>" data-wcp-busy id="fw-enable">
    <?= $this->csrfField() ?><input type="hidden" name="enabled" value="1">
    <div class="fw-semibold mb-2">Turn on the firewall</div>
    <div class="form-check mb-1">
        <input type="hidden" name="allow_services" value="0">
        <input class="form-check-input" type="checkbox" id="fw-services" name="allow_services" value="1" checked>
        <label class="form-check-label" for="fw-services">Also open the ports of running services (websites, mail, FTP, DNS) listed above</label>
    </div>
    <?php if ($myIp !== ''): ?>
        <div class="form-check mb-2">
            <input class="form-check-input" type="checkbox" id="fw-me" name="allow_me" value="1" checked>
            <label class="form-check-label" for="fw-me">Always allow my address <code><?= e($myIp) ?></code> on every port, and whitelist it in cPHulk</label>
        </div>
    <?php endif; ?>
    <div class="small text-body-secondary mb-3">After turning it on you have 2 minutes to click <strong>Keep change</strong>. If the panel stops answering, you do nothing and the firewall is turned off again, with the rules it added removed.</div>
    <button class="btn btn-primary btn-sm"><i class="bi bi-shield-check me-1"></i>Turn on firewall</button>
</form>
<?php endif; ?>

<div class="wcp-panel" data-wcp-dt id="fw-rules">
    <div class="wcp-subtabs" role="group" aria-label="Rule kind">
        <button class="active" type="button" data-wcp-dt-seg data-key="kind" data-value="" aria-pressed="true">All rules: <?= count($rules) ?></button>
        <button type="button" data-wcp-dt-seg data-key="kind" data-value="port" aria-pressed="false">Port rules: <?= $portRules ?></button>
        <button type="button" data-wcp-dt-seg data-key="kind" data-value="ip" aria-pressed="false">IP rules: <?= $ipRules ?></button>
    </div>
    <div class="wcp-actionbar">
        <?php if ($canManage): ?>
            <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#new-rule"><i class="bi bi-plus-lg me-1"></i>Add rule</button>
        <?php endif; ?>
        <div class="wcp-actionbar-end">
            <div class="wcp-seg" role="group" aria-label="Direction">
                <button class="active" type="button" data-wcp-dt-seg data-key="dir" data-value="" aria-pressed="true">All directions</button>
                <button type="button" data-wcp-dt-seg data-key="dir" data-value="in" aria-pressed="false">Inbound</button>
                <button type="button" data-wcp-dt-seg data-key="dir" data-value="out" aria-pressed="false">Outbound</button>
            </div>
            <div class="wcp-search"><input class="form-control form-control-sm" type="search" placeholder="Port, address or remark" aria-label="Search rules" data-wcp-dt-search><i class="bi bi-search" aria-hidden="true"></i></div>
        </div>
    </div>
    <?php if ($canManage): ?>
        <?= $this->modalStart('new-rule', 'Add firewall rule', $errors !== []) ?>
            <form class="wcp-form-panel border rounded-3 p-3 mb-3" method="post" action="<?= e($this->url('security.firewall.add')) ?>" data-wcp-busy>
                <?= $this->csrfField() ?>
                <div class="row g-2 align-items-end">
                    <div class="col-6 col-md-2"><label class="form-label small" for="r-action">Strategy</label>
                        <select class="form-select form-select-sm" id="r-action" name="action"><option value="allow">Allow</option><option value="limit">Allow, rate-limited</option><option value="deny">Deny</option><option value="reject">Reject</option></select></div>
                    <div class="col-6 col-md-2"><label class="form-label small" for="r-proto">Protocol</label>
                        <select class="form-select form-select-sm" id="r-proto" name="proto"><option value="tcp">TCP</option><option value="udp">UDP</option><option value="">TCP and UDP</option></select></div>
                    <div class="col-md-2"><label class="form-label small" for="r-port">Port or range</label>
                        <input class="form-control form-control-sm" id="r-port" name="port" placeholder="443 or 8000:8100" required></div>
                    <div class="col-md-3"><label class="form-label small" for="r-from">Source IP (optional)</label>
                        <input class="form-control form-control-sm" id="r-from" name="from" placeholder="All, or 203.0.113.4 / 10.0.0.0/8"></div>
                    <div class="col-md-2"><label class="form-label small" for="r-comment">Remarks</label>
                        <input class="form-control form-control-sm" id="r-comment" name="comment" maxlength="60" placeholder="Monitoring"></div>
                    <div class="col-md-1"><button class="btn btn-sm btn-primary w-100" type="submit">Add</button></div>
                </div>
                <div class="form-text">Common ports: 22 SSH, 80 HTTP, 443 HTTPS, 3306 MySQL. Changes are undone after 2 minutes unless you confirm them.</div>
            </form>
        <?= $this->modalEnd() ?>
    <?php endif; ?>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Protocol</th><th>Port</th><th>Status</th><th>Strategy</th><th>Direction</th><th>Source IP</th><th>Remarks</th><th class="text-end">Operate</th></tr></thead>
            <tbody>
            <?php foreach ($rules as $r):
                $port = (string) ($r['port'] ?? '');
                $dir = (string) ($r['direction'] ?? 'in');
                $st = $state($r);
                [$act, $actClass] = $strategy[$r['action']] ?? [ucfirst((string) $r['action']), ''];
                ?>
                <tr data-wcp-row data-dt-kind="<?= $port !== '' ? 'port' : 'ip' ?>" data-dt-dir="<?= e($dir === 'route' ? 'in out' : $dir) ?>" title="<?= e((string) $r['spec']) ?>">
                    <td><?= e(($r['proto'] ?? '') === '' ? 'tcp/udp' : (string) $r['proto']) ?></td>
                    <td class="fw-semibold"><?= $port !== '' ? e(str_replace(',', ', ', $port)) : '<span class="wcp-muted">All</span>' ?><?php if (!empty($r['ssh'])): ?> <span class="badge text-bg-light border fw-normal">SSH</span><?php endif; ?></td>
                    <td class="small"><?= match ($st) { 'listening' => '<span class="wcp-good">Listening</span>', 'local' => '<span class="wcp-warn" title="Only on this server (127.0.0.1)">Local only</span>', 'idle' => '<span class="wcp-muted">Not listening</span>', default => '<span class="wcp-muted">—</span>' } ?></td>
                    <td><span class="<?= e($actClass) ?>"><?= e($act) ?></span></td>
                    <td><?= $dir === 'out' ? 'Outbound' : ($dir === 'route' ? 'Routed' : 'Inbound') ?></td>
                    <td class="small"><?= ($r['from'] ?? '') !== '' ? '<code>' . e((string) $r['from']) . '</code>' : 'All' ?></td>
                    <td class="small"><?= e((string) ($r['comment'] ?? '')) ?></td>
                    <td class="text-end">
                        <?php if ($canManage): ?>
                            <span class="wcp-ops"><form method="post" action="<?= e($this->url('security.firewall.delete')) ?>" data-wcp-confirm="Delete the rule “<?= e($r['spec']) ?>”?"><?= $this->csrfField() ?><input type="hidden" name="spec" value="<?= e($r['spec']) ?>"><button class="wcp-op is-danger" aria-label="Delete the rule <?= e($r['spec']) ?>">Delete</button></form></span>
                        <?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            <tr class="<?= $rules === [] ? '' : 'd-none' ?>" data-wcp-dt-empty><td colspan="8" class="text-center py-4 wcp-muted"><?= $rules === [] ? 'No rules yet.' : 'No rule matches.' ?></td></tr>
            </tbody>
        </table>
    </div>
    <div class="wcp-pager" data-wcp-dt-pager></div>
</div>
<?php if (is_array($protection ?? null)):
    $pc = (array) ($protection['config'] ?? []);
    $fresh = empty($pc['enabled']) && empty($pc['syn_rate']) && empty($pc['conn_limits']) && empty($pc['port_flood']) && empty($pc['icmp_rate']) && empty($pc['invalid']);
    $limits = static fn (array $l, bool $proto): string => implode(', ', array_map(static fn (array $x): string => ($proto ? $x['proto'] . '/' : '') . $x['port'] . ':' . $x['max'], $l));
    $dropped = (array) ($protection['dropped'] ?? []);
    $pon = !empty($pc['enabled']);
    $dis = $canManage ? '' : ' disabled';
    ?>
<div class="wcp-panel mt-3" id="fw-protect">
    <div class="wcp-statusbar">
        <span class="fw-medium">Protection</span>
        <span><?= !empty($protection['active']) ? '<span class="wcp-state is-ok">On</span>' : '<span class="wcp-state">Off</span>' ?></span>
        <span class="small wcp-muted">Flood and connection limits in the manner of CSF, next to the firewall: they only drop or refuse traffic, never open a port.</span>
    </div>
    <?php if (empty($protection['available'])): ?>
        <div class="wcp-infobar m-3"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>nftables is not installed. Install <strong>nftables</strong> from <a href="<?= e($this->url('packages')) ?>">Packages</a>.</span></div>
    <?php else: ?>
    <?php if (!empty($protection['active']) && $dropped !== []): ?>
        <div class="px-3 pt-3 small">
            <span class="fw-medium me-1">Dropped since loaded:</span>
            <?php $names = ['invalid' => 'invalid packets', 'syn' => 'SYN flood', 'icmp' => 'ping flood'];
            foreach ($dropped as $k => $n): $k = (string) $k;
                $label = $names[$k] ?? (str_starts_with($k, 'conn:') ? 'connections to port ' . substr($k, 5) : (str_starts_with($k, 'rate:') ? 'flood on ' . substr($k, 5) : $k)); ?>
                <span class="badge text-bg-light border fw-normal me-1"><?= e($label) ?>: <?= number_format((int) $n) ?></span>
            <?php endforeach; ?>
        </div>
    <?php endif; ?>
    <form class="p-3" method="post" action="<?= e($this->url('security.firewall.protection')) ?>" data-wcp-busy>
        <?= $this->csrfField() ?>
        <div class="form-check form-switch mb-3">
            <input type="hidden" name="enabled" value="0">
            <input class="form-check-input" type="checkbox" role="switch" id="pr-on" name="enabled" value="1"<?= $pon ? ' checked' : '' ?><?= $dis ?>>
            <label class="form-check-label fw-medium" for="pr-on">Turn on the protections below</label>
        </div>
        <div class="form-check mb-3">
            <input type="hidden" name="invalid" value="0">
            <input class="form-check-input" type="checkbox" id="pr-invalid" name="invalid" value="1"<?= ($fresh || !empty($pc['invalid'])) ? ' checked' : '' ?><?= $dis ?>>
            <label class="form-check-label" for="pr-invalid">Drop invalid packets <span class="small wcp-muted">(of no known connection, and impossible TCP flags used by port scans: NULL, Xmas, SYN+FIN; CSF's PACKET_FILTER)</span></label>
        </div>
        <div class="row g-3">
            <div class="col-md-6">
                <label class="form-label small fw-medium" for="pr-syn">SYN flood: new connections per second per address</label>
                <input class="form-control form-control-sm" id="pr-syn" name="syn_rate" inputmode="numeric" value="<?= e((string) ($fresh ? 100 : (int) ($pc['syn_rate'] ?? 0))) ?>"<?= $dis ?>>
                <div class="form-text">0 turns it off. Above it, new connections from that address are dropped (CSF's SYNFLOOD).</div>
            </div>
            <div class="col-md-6">
                <label class="form-label small fw-medium" for="pr-icmp">Ping: echo requests per second per address</label>
                <input class="form-control form-control-sm" id="pr-icmp" name="icmp_rate" inputmode="numeric" value="<?= e((string) ($fresh ? 5 : (int) ($pc['icmp_rate'] ?? 0))) ?>"<?= $dis ?>>
                <div class="form-text">0 turns it off (CSF's ICMP_IN_RATE).</div>
            </div>
            <div class="col-md-6">
                <label class="form-label small fw-medium" for="pr-conn">Connection limits: port:connections at once per address</label>
                <input class="form-control form-control-sm font-monospace" id="pr-conn" name="conn_limits" placeholder="22:10, 80:150, 443:150" value="<?= e($limits((array) ($pc['conn_limits'] ?? []), false)) ?>"<?= $dis ?>>
                <div class="form-text">TCP. Above the limit, new connections are refused (CSF's CONNLIMIT). Keep web ports generous: browsers open several connections.</div>
            </div>
            <div class="col-md-6">
                <label class="form-label small fw-medium" for="pr-flood">Port flood: protocol/port:new connections per minute per address</label>
                <input class="form-control form-control-sm font-monospace" id="pr-flood" name="port_flood" placeholder="tcp/22:20, tcp/25:60, udp/53:600" value="<?= e($limits((array) ($pc['port_flood'] ?? []), true)) ?>"<?= $dis ?>>
                <div class="form-text">Above the rate, new connections are dropped until it falls (CSF's PORTFLOOD).</div>
            </div>
            <div class="col-12">
                <label class="form-label small fw-medium" for="pr-exempt">Exempt addresses and networks</label>
                <textarea class="form-control form-control-sm font-monospace" id="pr-exempt" name="exempt" rows="2" placeholder="203.0.113.4, 10.0.0.0/8"<?= $dis ?>><?= e(implode(', ', (array) ($pc['exempt'] ?? []))) ?></textarea>
                <div class="form-text">These skip the protections (monitoring, your office, a load balancer). The firewall's own rules still apply to them.</div>
                <?php if ($myIp !== ''): ?>
                    <div class="form-check mt-1">
                        <input class="form-check-input" type="checkbox" id="pr-me" name="exempt_me" value="1" checked<?= $dis ?>>
                        <label class="form-check-label small" for="pr-me">Exempt my address <code><?= e($myIp) ?></code></label>
                    </div>
                <?php endif; ?>
            </div>
        </div>
        <?php if ($canManage): ?>
            <div class="d-flex align-items-center gap-3 mt-3">
                <button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Apply</button>
                <span class="small wcp-muted">Applied at once; undone after 2 minutes unless you click <strong>Keep change</strong>.</span>
            </div>
        <?php endif; ?>
    </form>
    <?php endif; ?>
</div>
<?php endif; ?>
<?php endif; ?>
