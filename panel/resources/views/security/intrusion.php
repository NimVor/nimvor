<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed>|null $status */
/** @var array<string, mixed>|null $executions */
/** @var string $sub */
/** @var string $filterUser */
$canManage = $this->can(\NimVor\Security\Permissions::SECURITY_MANAGE) && ($user['role_name'] ?? '') === 'admin';
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($status === null) { return; } ?>
<?php
$enabled = !empty($status['enabled']);
$accounts = (array) ($status['accounts'] ?? []);
$blacklist = (array) ($status['blacklist'] ?? []);
$suggested = array_map('strval', (array) ($status['suggested'] ?? []));
$auditd = (array) ($status['auditd'] ?? []);
$acl = (array) ($status['acl'] ?? []);
$tabs = ['overview' => 'Overview', 'blacklist' => 'Program blacklist', 'executions' => 'Program log', 'operations' => 'Operation logs'];
$when = static fn (string $t): string => str_replace(['T', 'Z'], [' ', ''], $t);
?>

<div class="nim-panel mb-3">
    <p class="mb-2">Websites and services run other people's code. Anti-intrusion watches the accounts you choose: it <strong>logs</strong> every program they start, and it can <strong>protect</strong> them by refusing a <strong>blacklist</strong> of programs that attackers bring and sites almost never need (port scanners, tunnelling and reverse-shell helpers, packet capture, debuggers). Every other program keeps working, so a protected website does not break.</p>
    <div class="d-flex flex-wrap align-items-center gap-3 small">
        <span class="fw-medium">Master switch:</span>
        <?php if ($enabled): ?><span class="nim-state is-ok">On</span><?php else: ?><span class="nim-state is-off">Off</span><?php endif; ?>
        <span>Programs refused: <strong><?= (int) ($status['interceptions'] ?? 0) ?></strong></span>
        <span>On the blacklist: <strong><?= count($blacklist) ?></strong></span>
    </div>
    <?php if (empty($auditd['installed'])): ?>
        <div class="alert alert-warning small mt-3 mb-0"><i class="bi bi-exclamation-triangle me-1"></i><strong>Logging needs the audit system.</strong> Install the <code>auditd</code> package (<a href="<?= e($this->url('components')) ?>">Server → Components</a>), then turn logging on for an account.</div>
    <?php elseif (!empty($auditd['note']) && $enabled): ?>
        <div class="small nim-muted mt-3"><?= e((string) $auditd['note']) ?></div>
    <?php endif; ?>
    <?php if (empty($acl['installed'])): ?>
        <div class="alert alert-warning small mt-3 mb-0"><i class="bi bi-exclamation-triangle me-1"></i><strong>Protection needs the ACL tools.</strong> Install the <code>acl</code> package (<a href="<?= e($this->url('components')) ?>">Server → Components</a>).</div>
    <?php endif; ?>
</div>

<ul class="nav nav-tabs mb-3" role="tablist">
    <?php foreach ($tabs as $key => $label): ?>
        <li class="nav-item"><a class="nav-link<?= $sub === $key ? ' active' : '' ?>"<?= $sub === $key ? ' aria-current="page"' : '' ?> href="<?= e($this->url('security.intrusion', [], ['tab' => $key])) ?>"><?= e($label) ?></a></li>
    <?php endforeach; ?>
</ul>

<?php if ($sub === 'overview'): ?>
<form method="post" action="<?= e($this->url('security.intrusion.accounts')) ?>" data-nim-busy>
    <?= $this->csrfField() ?>
    <div class="nim-panel mb-3">
        <div class="form-check form-switch mb-2">
            <input type="hidden" name="enabled" value="0">
            <input class="form-check-input" type="checkbox" role="switch" id="ai-enabled" name="enabled" value="1"<?= $enabled ? ' checked' : '' ?><?= $canManage ? '' : ' disabled' ?>>
            <label class="form-check-label fw-medium" for="ai-enabled">Anti-intrusion master switch</label>
        </div>
        <div class="small nim-muted">With the switch off nothing is logged or blocked, and every change NimVor made (audit rules, program restrictions) is taken away again.</div>
    </div>
    <div class="nim-panel">
        <div class="nim-dtable table-responsive">
            <table class="table align-middle" id="ai-accounts">
                <thead><tr><th>Account</th><th class="text-end">Total</th><th class="text-end">Today</th><th class="text-end">Refused</th><th>Protection</th><th>Log</th><th>Remark</th><th></th></tr></thead>
                <tbody>
                <?php foreach ($accounts as $i => $a): $a = (array) $a; ?>
                    <tr>
                        <td class="font-monospace"><?= e((string) $a['name']) ?><?php if (($a['kind'] ?? '') === 'website'): ?> <span class="badge text-bg-light border">website</span><?php endif; ?></td>
                        <td class="text-end"><?= (int) $a['total'] ?></td>
                        <td class="text-end"><?= (int) $a['today'] ?></td>
                        <td class="text-end"><?= (int) $a['blocked'] ?></td>
                        <td><div class="form-check form-switch mb-0"><input class="form-check-input" type="checkbox" role="switch" name="protect[]" value="<?= e((string) $a['name']) ?>" id="ai-p<?= (int) $i ?>" aria-label="Protect <?= e((string) $a['name']) ?>"<?= !empty($a['protect']) ? ' checked' : '' ?><?= $canManage ? '' : ' disabled' ?>></div></td>
                        <td><div class="form-check form-switch mb-0"><input class="form-check-input" type="checkbox" role="switch" name="log[]" value="<?= e((string) $a['name']) ?>" id="ai-l<?= (int) $i ?>" aria-label="Log <?= e((string) $a['name']) ?>"<?= !empty($a['log']) ? ' checked' : '' ?><?= $canManage ? '' : ' disabled' ?>></div></td>
                        <td class="small nim-muted"><?= e((string) $a['remark']) ?></td>
                        <td class="text-end"><a href="<?= e($this->url('security.intrusion', [], ['tab' => 'executions', 'user' => (string) $a['name']])) ?>">Logs</a></td>
                    </tr>
                <?php endforeach; ?>
                <?php if ($accounts === []): ?><tr><td colspan="8" class="text-center py-4 nim-muted">No service or website accounts were found.</td></tr><?php endif; ?>
                </tbody>
            </table>
        </div>
        <?php if ($canManage): ?><button class="btn btn-primary btn-sm mt-2">Save</button><?php endif; ?>
    </div>
</form>
<ul class="small nim-muted mt-3">
    <li><strong>Protection</strong> refuses the programs on the blacklist to that account (an ACL entry on each program file). Programs not on the list are never affected.</li>
    <li><strong>Log</strong> records every program the account starts (needs <code>auditd</code>); refused attempts are marked. Nothing is blocked by logging alone.</li>
    <li>Counts come from the audit log the server keeps, which is rotated; they are not lifetime totals.</li>
</ul>

<?php elseif ($sub === 'blacklist'): ?>
<?php if ($canManage): ?>
<form class="nim-panel mb-3" method="post" action="<?= e($this->url('security.intrusion.blacklist')) ?>" data-nim-busy>
    <?= $this->csrfField() ?>
    <div class="fw-medium mb-1">Add programs</div>
    <div class="small nim-muted mb-2">Names (<code>nc</code>) or full paths (<code>/usr/bin/nmap</code>), separated by spaces or commas. Only programs in the system's program directories (<code>/usr</code>, <code>/bin</code>, <code>/sbin</code>, <code>/opt</code>) can be listed.</div>
    <div class="input-group mb-3">
        <input class="form-control font-monospace" name="programs" id="ai-programs" placeholder="nc nmap /usr/bin/tcpdump" autocomplete="off">
        <button class="btn btn-primary">Add</button>
    </div>
    <?php if ($suggested !== []): ?>
        <div class="fw-medium mb-1">Suggested</div>
        <div class="small nim-muted mb-2">Present on this server and rarely needed by a website.</div>
        <div class="row row-cols-1 row-cols-md-3 g-1 mb-3">
            <?php foreach ($suggested as $i => $p): ?>
                <div class="col"><div class="form-check">
                    <input class="form-check-input" type="checkbox" name="suggested[]" value="<?= e($p) ?>" id="ai-s<?= (int) $i ?>">
                    <label class="form-check-label font-monospace" for="ai-s<?= (int) $i ?>"><?= e($p) ?></label>
                </div></div>
            <?php endforeach; ?>
        </div>
        <button class="btn btn-outline-primary btn-sm">Add the selected</button>
    <?php endif; ?>
</form>
<?php endif; ?>
<div class="nim-panel">
    <div class="nim-dtable table-responsive">
        <table class="table align-middle" id="ai-blacklist">
            <thead><tr><th>Program</th><th>Refused to</th><th></th></tr></thead>
            <tbody>
            <?php foreach ($blacklist as $b): $b = (array) $b; ?>
                <tr>
                    <td class="font-monospace"><?= e((string) $b['path']) ?><?php if (empty($b['exists'])): ?> <span class="badge text-bg-warning">missing</span><?php endif; ?></td>
                    <td class="small"><?= (array) ($b['denied'] ?? []) === [] ? '<span class="nim-muted">no protected account yet</span>' : e(implode(', ', array_map('strval', (array) $b['denied']))) ?></td>
                    <td class="text-end">
                        <?php if ($canManage): ?>
                        <form method="post" action="<?= e($this->url('security.intrusion.blacklist.remove')) ?>" data-nim-busy>
                            <?= $this->csrfField() ?><input type="hidden" name="path" value="<?= e((string) $b['path']) ?>">
                            <button class="btn btn-sm btn-outline-secondary">Remove</button>
                        </form>
                        <?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            <?php if ($blacklist === []): ?><tr><td colspan="3" class="text-center py-4 nim-muted">The blacklist is empty: protection refuses nothing yet.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
    <div class="small nim-muted mt-2">A blacklist raises the bar but is not a wall: someone who can write a file can still bring and run a program of their own. Use it next to the firewall, the web application firewall and malware scans. Package updates replace program files; NimVor sets the restrictions again after them.</div>
</div>

<?php elseif ($sub === 'executions'): ?>
<div class="nim-panel">
    <form class="d-flex flex-wrap align-items-center gap-2 mb-3" method="get" action="<?= e($this->url('security.intrusion')) ?>">
        <input type="hidden" name="tab" value="executions">
        <label class="small" for="ai-user">Account</label>
        <select class="form-select form-select-sm w-auto" id="ai-user" name="user">
            <option value="">All logged accounts</option>
            <?php foreach ($accounts as $a): $a = (array) $a; ?><option value="<?= e((string) $a['name']) ?>"<?= $filterUser === $a['name'] ? ' selected' : '' ?>><?= e((string) $a['name']) ?></option><?php endforeach; ?>
        </select>
        <button class="btn btn-sm btn-outline-secondary">Show</button>
    </form>
    <?php $list = (array) ($executions['executions'] ?? []); ?>
    <?php if (!empty($executions['note'])): ?><div class="alert alert-secondary small"><?= e((string) $executions['note']) ?></div><?php endif; ?>
    <div class="nim-dtable table-responsive">
        <table class="table align-middle" id="ai-executions">
            <thead><tr><th>Time (UTC)</th><th>Account</th><th>Program</th><th>Command</th><th>Result</th></tr></thead>
            <tbody>
            <?php foreach ($list as $e): $e = (array) $e; ?>
                <tr>
                    <td class="text-nowrap"><?= e($when((string) $e['time'])) ?></td>
                    <td class="font-monospace"><?= e((string) $e['user']) ?></td>
                    <td class="font-monospace"><?= e((string) $e['program']) ?></td>
                    <td class="font-monospace small text-break"><?= e((string) $e['command']) ?></td>
                    <td><?= !empty($e['blocked']) ? '<span class="nim-state is-error">Refused</span>' : '<span class="nim-state is-ok">Started</span>' ?></td>
                </tr>
            <?php endforeach; ?>
            <?php if ($list === []): ?><tr><td colspan="5" class="text-center py-4 nim-muted">Nothing recorded yet. Turn logging on for an account on the Overview tab.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
    <div class="small nim-muted mt-2">Command lines are shortened and anything that looks like a password or key is hidden.</div>
</div>

<?php else: ?>
<div class="nim-panel">
    <div class="nim-dtable table-responsive">
        <table class="table align-middle" id="ai-operations">
            <thead><tr><th>Operation</th><th class="text-end">Date (UTC)</th></tr></thead>
            <tbody>
            <?php foreach ((array) ($status['operations'] ?? []) as $o): $o = (array) $o; ?>
                <tr><td><?= e((string) $o['text']) ?></td><td class="text-end text-nowrap"><?= e($when((string) $o['time'])) ?></td></tr>
            <?php endforeach; ?>
            <?php if (($status['operations'] ?? []) === []): ?><tr><td colspan="2" class="text-center py-4 nim-muted">No changes yet.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
    <div class="small nim-muted mt-2">The agent's own record of what was switched; the panel's audit log also names who did it.</div>
</div>
<?php endif; ?>
