<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed>|null $status
 * @var bool $isAdmin
 */
$enabled = (bool) ($status['enabled'] ?? false);
$sessions = (array) ($status['sessions'] ?? []);
$modes = ['jailed' => 'Jailed', 'normal' => 'Normal', 'root' => 'Root', 'simulation' => 'Simulation'];
?>
<?php $this->start(); ?>Web terminal<?php $this->end('title'); ?>
<?php $this->start(); ?>Shells in the browser: open sessions and the root shell<?php $this->end('subtitle'); ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php return; endif; ?>

<?php if (!$enabled): ?>
<div class="alert alert-warning">
    <i class="bi bi-exclamation-triangle me-1" aria-hidden="true"></i>
    The web terminal is off on this server. It needs the panel served over HTTPS by Nginx and write mode: run <code>sudo ./installer/install.sh --reinstall --public-https</code>, which sets <code>terminal_listen</code> in <code>/etc/whacp/agent.json</code> and proxies <code>/terminal/ws</code> to it.
</div>
<?php endif; ?>

<div class="wcp-infobar">
    <i class="bi bi-info-circle" aria-hidden="true"></i>
    <span>Website owners open a terminal from their website's <strong>Terminal</strong> page when the account has shell access (Security → Shell access) and the plan includes SSH. Each session runs as the account, in its shell mode and resource limits; at most <?= (int) ($status['max_per_account'] ?? 3) ?> per account and <?= (int) ($status['max_sessions'] ?? 20) ?> in all. Starts and ends are logged, keystrokes are not (they include passwords typed at prompts).</span>
</div>

<div class="wcp-panel mb-3" data-wcp-dt>
    <div class="wcp-actionbar"><div class="fw-semibold">Open sessions <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($sessions) ?></span></div></div>
    <?php if ($sessions === []): ?>
        <div class="wcp-empty-state"><i class="bi bi-terminal" aria-hidden="true"></i><div>No terminal is open.</div></div>
    <?php else: ?>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Account</th><th>Shell</th><th>Opened by</th><th>From</th><th>Started</th><th>Last input</th><th class="text-end">Traffic</th><th></th></tr></thead>
            <tbody>
            <?php foreach ($sessions as $s): ?>
                <tr data-wcp-row>
                    <td><code><?= e((string) $s['user']) ?></code></td>
                    <td><?= e($modes[$s['mode']] ?? (string) $s['mode']) ?></td>
                    <td><?= e((string) $s['label']) ?></td>
                    <td><?= e((string) $s['client_ip']) ?></td>
                    <td><?= e(date('Y-m-d H:i', (int) strtotime((string) $s['started']))) ?></td>
                    <td><?= e(date('H:i:s', (int) strtotime((string) $s['last_input']))) ?></td>
                    <td class="text-end small"><?= e(format_bytes((int) $s['bytes_in'])) ?> in · <?= e(format_bytes((int) $s['bytes_out'])) ?> out</td>
                    <td class="text-end">
                        <form class="d-inline" method="post" action="<?= e($this->url('terminal.close', ['id' => (string) $s['id']])) ?>" data-wcp-confirm="Close the terminal of <?= e((string) $s['user']) ?> opened by <?= e((string) $s['label']) ?>? Programs running in it are stopped."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger">Close</button></form>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
    <?php endif; ?>
</div>

<h2 class="h6 mt-4">Root shell</h2>
<?php if (!$enabled): ?>
<?php elseif (!($status['root_allowed'] ?? false)): ?>
<div class="card"><div class="card-body small">
    <p class="mb-2"><i class="bi bi-shield-lock me-1" aria-hidden="true"></i>Root shells are <strong>off</strong>. A root shell in the browser means anyone who takes over an administrator's panel sign-in controls the whole server, so only the server's root user can allow it: set <code>"terminal_root": true</code> in <code>/etc/whacp/agent.json</code> and run <code>systemctl restart whacp-agent</code>.</p>
    <p class="mb-0 text-body-secondary">Without it, use SSH as root, or open a website account's terminal from its website page.</p>
</div></div>
<?php elseif (!$isAdmin): ?>
<div class="card"><div class="card-body small">Only administrators open a root shell.</div></div>
<?php else: ?>
<div class="alert alert-danger small"><i class="bi bi-exclamation-octagon me-1" aria-hidden="true"></i>This is a root shell on the server, without the sandbox website accounts get. Everything is possible here, including breaking the server.</div>
<?= $this->partial('terminal/_console', ['styleNonce' => $styleNonce, 'openUrl' => $openUrl, 'unlocked' => $unlocked, 'needsCode' => $needsCode]) ?>
<?php endif; ?>
