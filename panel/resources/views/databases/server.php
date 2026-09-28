<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed>|null $status
 * @var list<array<string, mixed>> $connections
 * @var \WhaCP\Agent\AgentException|null $agentError
 */
use WhaCP\Security\Permissions as P;

$canManage = $this->can(P::SERVER_MANAGE);
$canRestart = $this->can(P::SERVICES_MANAGE);
$vars = (array) ($status['variables'] ?? []);
$st = (array) ($status['status'] ?? []);
$managed = (array) ($status['managed'] ?? []);
$num = static fn (string $k): float => (float) ($st[$k] ?? 0);
$uptime = max(1.0, $num('Uptime'));
$pages = $num('Innodb_buffer_pool_pages_total');
$poolUsed = $pages > 0 ? (int) round(100 * ($pages - $num('Innodb_buffer_pool_pages_free')) / $pages) : null;
$hit = $num('Innodb_buffer_pool_read_requests') > 0 ? 100 * (1 - $num('Innodb_buffer_pool_reads') / $num('Innodb_buffer_pool_read_requests')) : null;
$tmpDisk = $num('Created_tmp_tables') > 0 ? 100 * $num('Created_tmp_disk_tables') / $num('Created_tmp_tables') : null;
$maxConn = (int) ($vars['max_connections'] ?? 0);
$connPct = $maxConn > 0 ? (int) min(100, round(100 * $num('Threads_connected') / $maxConn)) : 0;
// Effective values as people read them.
$shown = static function (array $s, string $v): string {
    if ($s['kind'] === 'size' && ctype_digit($v)) {
        return format_bytes((int) $v, 0);
    }
    return $v;
};
$groups = [];
foreach ((array) ($status['settings'] ?? []) as $s) {
    $groups[(string) $s['group']][] = $s;
}
$collations = (array) ($status['collations'] ?? []);
$charset = (string) ($managed['character_set_server'] ?? $vars['character_set_server'] ?? 'utf8mb4');
?>
<?php $this->start(); ?>Database server<?php $this->end('title'); ?>
<?php $this->start(); ?><?= e(trim((string) ($vars['version_comment'] ?? 'MariaDB / MySQL'))) ?><?php $this->end('subtitle'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('databases')) ?>"><i class="bi bi-arrow-left"></i> Databases</a><?php $this->end('actions'); ?>

<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>
<?php if ($status === null): return; endif; ?>

<?php if (empty($status['running'])): ?>
    <div class="alert alert-danger"><i class="bi bi-x-octagon me-1"></i>The database server does not answer<?= !empty($status['error']) ? ': <span class="font-monospace small">' . e(mb_strimwidth((string) $status['error'], 0, 300, '…')) . '</span>' : '.' ?>
        <?php if ($this->hasRoute('services')): ?> <a href="<?= e($this->url('services')) ?>">Services</a><?php endif; ?></div>
<?php endif; ?>
<?php if (!empty($status['config_error'])): ?>
    <div class="alert alert-warning small"><div class="fw-semibold"><i class="bi bi-exclamation-triangle me-1"></i>The server's option files have an error; a restart would fail, so restarts are refused until it is fixed.</div><pre class="mb-0 mt-1 small text-wrap"><?= e((string) $status['config_error']) ?></pre></div>
<?php endif; ?>

<?php if (!empty($status['running'])): ?>
<div class="row g-3 mb-3">
    <?php foreach ([
        ['Version', (string) ($vars['version'] ?? ''), 'Up ' . format_duration((int) $uptime)],
        ['Connections', (int) $num('Threads_connected') . ' / ' . $maxConn, 'Peak ' . (int) $num('Max_used_connections') . ' · ' . (int) $num('Threads_running') . ' running'],
        ['Queries', number_format($num('Questions') / $uptime, 1) . ' /s', number_format($num('Slow_queries')) . ' slow · ' . number_format($num('Aborted_connects')) . ' failed logins'],
        ['Data', format_bytes((int) ($status['bytes'] ?? 0)), (int) ($status['databases'] ?? 0) . ' databases · ' . (int) ($status['users'] ?? 0) . ' users'],
    ] as [$label, $value, $sub]): ?>
        <div class="col-sm-6 col-xl-3">
            <div class="wcp-panel h-100 mb-0">
                <div class="small wcp-muted"><?= e($label) ?></div>
                <div class="fs-5 fw-semibold text-break"><?= e($value) ?></div>
                <div class="small wcp-muted"><?= e($sub) ?></div>
            </div>
        </div>
    <?php endforeach; ?>
</div>

<div class="row g-3 mb-3">
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-speedometer2 me-1"></i>Resources</div>
            <div class="card-body small">
                <div class="d-flex justify-content-between"><span>Connections in use</span><span><?= $connPct ?>%</span></div>
                <progress class="wcp-meter w-100 mb-2" max="100" value="<?= $connPct ?>" aria-label="Connections in use"><?= $connPct ?>%</progress>
                <?php if ($poolUsed !== null): ?>
                    <div class="d-flex justify-content-between"><span>InnoDB buffer pool used (<?= e(format_bytes((int) ($vars['innodb_buffer_pool_size'] ?? 0), 0)) ?>)</span><span><?= $poolUsed ?>%</span></div>
                    <progress class="wcp-meter w-100 mb-2" max="100" value="<?= $poolUsed ?>" aria-label="Buffer pool used"><?= $poolUsed ?>%</progress>
                <?php endif; ?>
                <dl class="row mb-0">
                    <?php if ($hit !== null): ?><dt class="col-7 fw-normal wcp-muted">Buffer pool hit rate</dt><dd class="col-5 mb-1"><?= e(number_format($hit, 2)) ?>%<?= $hit < 99 ? ' <span class="wcp-state is-warn">low</span>' : '' ?></dd><?php endif; ?>
                    <?php if ($tmpDisk !== null): ?><dt class="col-7 fw-normal wcp-muted">Temporary tables on disk</dt><dd class="col-5 mb-1"><?= e(number_format($tmpDisk, 1)) ?>%<?= $tmpDisk > 25 ? ' <span class="wcp-state is-warn">high</span>' : '' ?></dd><?php endif; ?>
                    <dt class="col-7 fw-normal wcp-muted">Open tables</dt><dd class="col-5 mb-1"><?= number_format($num('Open_tables')) ?> of <?= e((string) ($vars['table_open_cache'] ?? '?')) ?></dd>
                    <dt class="col-7 fw-normal wcp-muted">Received / sent</dt><dd class="col-5 mb-1"><?= e(format_bytes((int) $num('Bytes_received'))) ?> / <?= e(format_bytes((int) $num('Bytes_sent'))) ?></dd>
                    <dt class="col-7 fw-normal wcp-muted">Selects / inserts / updates / deletes</dt><dd class="col-5 mb-1"><?= number_format($num('Com_select')) ?> / <?= number_format($num('Com_insert')) ?> / <?= number_format($num('Com_update')) ?> / <?= number_format($num('Com_delete')) ?></dd>
                    <dt class="col-7 fw-normal wcp-muted">Waits for table locks</dt><dd class="col-5 mb-1"><?= number_format($num('Table_locks_waited')) ?></dd>
                </dl>
            </div>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-plug me-1"></i>Connection information</div>
            <div class="card-body small">
                <dl class="row mb-2">
                    <dt class="col-5 fw-normal wcp-muted">Socket</dt><dd class="col-7 mb-1 font-monospace text-break"><?= e((string) ($vars['socket'] ?? '')) ?></dd>
                    <dt class="col-5 fw-normal wcp-muted">Port</dt><dd class="col-7 mb-1"><?= ($vars['skip_networking'] ?? 'OFF') === 'ON' ? 'none (network off)' : e((string) ($vars['port'] ?? '3306')) ?></dd>
                    <dt class="col-5 fw-normal wcp-muted">Listens on</dt><dd class="col-7 mb-1 font-monospace"><?= e((string) (($vars['bind_address'] ?? '') !== '' ? $vars['bind_address'] : 'all addresses')) ?></dd>
                    <dt class="col-5 fw-normal wcp-muted">Data directory</dt><dd class="col-7 mb-1 font-monospace text-break"><?= e((string) ($vars['datadir'] ?? '')) ?></dd>
                    <dt class="col-5 fw-normal wcp-muted">Error log</dt><dd class="col-7 mb-1 font-monospace text-break"><?= e((string) (($vars['log_error'] ?? '') !== '' ? $vars['log_error'] : 'system journal')) ?></dd>
                    <dt class="col-5 fw-normal wcp-muted">TLS</dt><dd class="col-7 mb-1"><?= e((string) ($vars['have_ssl'] ?? '')) ?></dd>
                </dl>
                <p class="mb-2 wcp-muted">Websites on this server connect to <code>localhost</code>. Where the server listens is set in the distribution's option files; WhaCP does not change it.</p>
                <div class="d-flex flex-wrap gap-2">
                    <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('databases')) ?>"><i class="bi bi-database me-1"></i>Databases and users</a>
                    <?php if ($this->hasRoute('logs')): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('logs')) ?>"><i class="bi bi-journal-text me-1"></i>Logs</a><?php endif; ?>
                </div>
            </div>
        </div>
    </div>
</div>
<?php endif; ?>

<div class="wcp-panel mb-3">
    <div class="d-flex flex-wrap align-items-center gap-2">
        <div class="me-auto">
            <div class="fw-semibold">Service <span class="font-monospace small wcp-muted"><?= e((string) ($status['unit'] ?? '')) ?></span></div>
            <div class="small wcp-muted">A restart checks the option files first and waits until the server answers again; connections in progress are dropped.</div>
        </div>
        <div class="d-flex flex-wrap gap-2">
        <?php if ($canManage && !empty($status['running'])): foreach (['privileges' => 'Reload privileges', 'logs' => 'Reopen logs', 'hosts' => 'Unblock hosts'] as $w => $label): ?>
            <form method="post" action="<?= e($this->url('databases.server.flush')) ?>" data-wcp-busy><?= $this->csrfField() ?><input type="hidden" name="what" value="<?= e($w) ?>"><button class="btn btn-sm btn-outline-secondary"><?= e($label) ?></button></form>
        <?php endforeach; endif; ?>
        <?php if ($canRestart): ?>
            <form method="post" action="<?= e($this->url('databases.server.restart')) ?>" data-wcp-busy data-wcp-confirm="Restart the database server? Websites cannot reach their databases for a few seconds."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger"<?= !empty($status['config_error']) ? ' disabled' : '' ?>><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><i class="bi bi-arrow-repeat me-1"></i>Restart</button></form>
        <?php endif; ?>
        </div>
    </div>
</div>

<form method="post" action="<?= e($this->url('databases.server.settings')) ?>" data-wcp-busy class="mb-3">
    <?= $this->csrfField() ?>
    <div class="card">
        <div class="card-header d-flex flex-wrap align-items-center gap-2"><span><i class="bi bi-sliders me-1"></i>Settings</span><span class="small wcp-muted ms-auto">Saved in <code><?= e((string) ($status['drop_in'] ?? '')) ?></code>, read after the distribution's files</span></div>
        <div class="card-body">
            <p class="small wcp-muted">Empty fields keep the server's own value (shown in grey). Values are checked, then the server program checks the option files; settings marked <span class="badge text-bg-light border fw-normal">now</span> are applied at once, the others at the next restart. If anything fails, the previous settings come back.</p>
            <div class="row g-4">
            <?php foreach ($groups as $group => $list): ?>
                <div class="col-xl-6">
                    <h2 class="h6"><?= e($group) ?></h2>
                    <?php foreach ($list as $s): $k = (string) $s['key']; $cur = (string) ($managed[$k] ?? ''); $eff = $shown($s, (string) ($vars[$k] ?? '')); $id = 'dbs-' . $k; ?>
                        <div class="row g-2 align-items-start mb-2">
                            <label class="col-sm-6 col-form-label col-form-label-sm" for="<?= e($id) ?>"><?= e((string) $s['label']) ?> <?= !empty($s['dynamic']) ? '<span class="badge text-bg-light border fw-normal">now</span>' : '<span class="badge text-bg-light border fw-normal">restart</span>' ?><div class="small wcp-muted font-monospace"><?= e($k) ?></div></label>
                            <div class="col-sm-6">
                                <input type="hidden" name="managed[<?= e($k) ?>]" value="<?= e($cur) ?>">
                                <?php if ($s['kind'] === 'bool' || $s['kind'] === 'choice'):
                                    $opts = $s['kind'] === 'bool' ? ['ON', 'OFF'] : ($k === 'collation_server' ? (array) ($collations[$charset] ?? []) : (array) $s['choices']); ?>
                                    <select class="form-select form-select-sm" id="<?= e($id) ?>" name="settings[<?= e($k) ?>]"<?= $canManage ? '' : ' disabled' ?>>
                                        <option value="">Server's value (<?= e($eff !== '' ? $eff : '—') ?>)</option>
                                        <?php foreach ($opts as $o): ?><option<?= $cur === (string) $o ? ' selected' : '' ?>><?= e((string) $o) ?></option><?php endforeach; ?>
                                    </select>
                                <?php else: ?>
                                    <input class="form-control form-control-sm font-monospace" id="<?= e($id) ?>" name="settings[<?= e($k) ?>]" value="<?= e($cur) ?>" placeholder="<?= e($eff) ?>"<?= $canManage ? '' : ' disabled' ?>>
                                <?php endif; ?>
                                <div class="form-text"><?= e((string) $s['help']) ?></div>
                            </div>
                        </div>
                    <?php endforeach; ?>
                </div>
            <?php endforeach; ?>
            </div>
        </div>
        <?php if ($canManage): ?>
        <div class="card-footer d-flex flex-wrap align-items-center gap-3">
            <?php if ($canRestart): ?><div class="form-check mb-0"><input class="form-check-input" type="checkbox" name="restart" value="1" id="dbs-restart"><label class="form-check-label small" for="dbs-restart">Restart the database server now to apply everything (checked first; rolled back if it does not come back)</label></div><?php endif; ?>
            <button class="btn btn-primary ms-auto"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save settings</button>
        </div>
        <?php endif; ?>
    </div>
</form>

<?php if (!empty($status['running'])): ?>
<div class="wcp-panel" id="connections" data-wcp-dt>
    <div class="wcp-actionbar">
        <div class="fw-semibold"><i class="bi bi-diagram-3 me-1"></i>Connections <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($connections) ?></span></div>
        <div class="wcp-actionbar-end">
            <div class="wcp-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search connections" data-wcp-dt-search><i class="bi bi-search" aria-hidden="true"></i></div>
            <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('databases.server')) ?>#connections"><i class="bi bi-arrow-clockwise me-1"></i>Refresh</a>
        </div>
    </div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle small">
            <thead><tr><th>Id</th><th>User</th><th>From</th><th>Database</th><th>Command</th><th class="text-end">Time</th><th>Statement</th><th class="text-end">Operate</th></tr></thead>
            <tbody>
            <?php foreach ($connections as $cn): ?>
                <tr data-wcp-row>
                    <td><?= (int) $cn['id'] ?></td>
                    <td class="font-monospace"><?= e((string) $cn['user']) ?></td>
                    <td class="font-monospace"><?= e((string) $cn['host']) ?></td>
                    <td class="font-monospace"><?= e((string) $cn['db']) ?></td>
                    <td><?= e((string) $cn['command']) ?><?= (string) $cn['state'] !== '' ? '<div class="wcp-muted">' . e((string) $cn['state']) . '</div>' : '' ?></td>
                    <td class="text-end"><?= e(format_duration((int) $cn['time'])) ?></td>
                    <td class="font-monospace text-break"><?= e(mb_strimwidth((string) $cn['info'], 0, 300, '…')) ?></td>
                    <td class="text-end text-nowrap">
                        <?php if ($canManage && empty($cn['system'])): ?>
                            <?php if ((string) $cn['info'] !== ''): ?><form class="d-inline" method="post" action="<?= e($this->url('databases.server.kill')) ?>" data-wcp-confirm="Stop the statement of connection <?= (int) $cn['id'] ?>? The connection stays open."><?= $this->csrfField() ?><input type="hidden" name="id" value="<?= (int) $cn['id'] ?>"><input type="hidden" name="query_only" value="1"><button class="btn btn-sm btn-outline-secondary">Stop query</button></form><?php endif; ?>
                            <form class="d-inline" method="post" action="<?= e($this->url('databases.server.kill')) ?>" data-wcp-confirm="End connection <?= (int) $cn['id'] ?> (<?= e((string) $cn['user']) ?>)? A transaction in progress is rolled back."><?= $this->csrfField() ?><input type="hidden" name="id" value="<?= (int) $cn['id'] ?>"><button class="btn btn-sm btn-outline-danger">End</button></form>
                        <?php elseif (!empty($cn['system'])): ?><span class="wcp-muted">server thread</span><?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            <tr class="<?= $connections === [] ? '' : 'd-none' ?>" data-wcp-dt-empty><td colspan="8" class="text-center py-4 wcp-muted">No other connections.</td></tr>
            </tbody>
        </table>
    </div>
    <div class="wcp-pager" data-wcp-dt-pager></div>
</div>
<?php endif; ?>
