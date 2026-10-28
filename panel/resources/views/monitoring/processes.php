<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed>|null $snap
 * @var \NimVor\Agent\AgentException|null $agentError
 * @var string $show
 */
use NimVor\Security\Permissions as P;

$canManage = $this->can(P::SERVER_MANAGE);
$procs = (array) ($snap['processes'] ?? []);
$users = (array) ($snap['users'] ?? []);
$duration = static function (int $s): string {
    return $s >= 86400 ? intdiv($s, 86400) . 'd ' . intdiv($s % 86400, 3600) . 'h' : ($s >= 3600 ? intdiv($s, 3600) . 'h ' . intdiv($s % 3600, 60) . 'm' : ($s >= 60 ? intdiv($s, 60) . 'm' : $s . 's'));
};
?>
<?php $this->start(); ?>Monitoring<?php $this->end('title'); ?>
<?php $this->start(); ?>Every process on this server<?php $this->end('subtitle'); ?>
<nav class="nim-pagetabs" aria-label="Monitoring"><a href="<?= e($this->url('monitoring')) ?>">History and alerts</a><a href="<?= e($this->url('monitoring.live')) ?>">Live</a><a class="active" href="<?= e($this->url('monitoring.processes')) ?>" aria-current="page">Processes</a></nav>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php return; ?><?php endif; ?>

<?php if (empty($snap['detailed'])): ?>
    <div class="alert alert-secondary small"><i class="bi bi-info-circle me-1"></i>Programs, folders and listening ports of other accounts' processes could not be read, so only the checks on names and command lines ran.<?= !empty($snap['scan_error']) ? ' <span class="nim-muted">(' . e(mb_substr((string) $snap['scan_error'], 0, 300)) . ')</span>' : '' ?></div>
<?php endif; ?>

<?php if ($canManage): ?>
<form class="nim-panel p-3 mb-3 d-flex flex-wrap align-items-end gap-2" method="post" action="<?= e($this->url('monitoring.processes.kill_user')) ?>" data-nim-noajax data-nim-confirm="End every process of the selected account? Websites, mail or services running as that account stop until they are started again.">
    <?= $this->csrfField() ?>
    <div><label class="form-label small mb-1" for="pk-user">End all processes of an account</label>
        <select class="form-select form-select-sm" id="pk-user" name="user" required>
            <option value="">Choose an account…</option>
            <?php foreach ($users as $u): if ($u === 'root' || $u === 'nimvor') { continue; } ?><option><?= e((string) $u) ?></option><?php endforeach; ?>
        </select></div>
    <div><label class="form-label small mb-1" for="pk-sig">How</label>
        <select class="form-select form-select-sm" id="pk-sig" name="signal"><option value="TERM">Ask to stop (TERM)</option><option value="KILL">Kill at once (KILL)</option></select></div>
    <button class="btn btn-sm btn-outline-danger">End processes</button>
    <span class="small nim-muted ms-auto">root and NimVor's own account are not listed.</span>
</form>
<?php endif; ?>

<div class="nim-panel" data-nim-dt>
    <div class="nim-actionbar">
        <div class="fw-semibold"><?= count($procs) ?> processes · <?php if ((int) ($snap['suspicious'] ?? 0) > 0): ?><span class="nim-state is-error"><?= (int) $snap['suspicious'] ?> suspicious</span><?php else: ?><span class="nim-state is-ok">nothing suspicious</span><?php endif; ?></div>
        <div class="nim-actionbar-end">
            <select class="form-select form-select-sm w-auto" aria-label="Show" data-nim-dt-select="kind">
                <option value="">All processes</option>
                <option value="suspicious"<?= $show === 'suspicious' ? ' selected' : '' ?>>Suspicious only</option>
                <option value="user">Without kernel threads</option>
            </select>
            <select class="form-select form-select-sm w-auto" aria-label="Owner" data-nim-dt-select="owner">
                <option value="">Every owner</option>
                <?php foreach ($users as $u): ?><option value="<?= e((string) $u) ?>"><?= e((string) $u) ?></option><?php endforeach; ?>
            </select>
            <div class="nim-search"><input class="form-control form-control-sm" type="search" placeholder="Search commands" aria-label="Search commands" data-nim-dt-search><i class="bi bi-search" aria-hidden="true"></i></div>
            <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('monitoring.processes')) ?>" title="Refresh"><i class="bi bi-arrow-clockwise"></i></a>
        </div>
    </div>
    <div class="nim-dtable table-responsive">
        <table class="table align-middle table-sm nim-proc-table">
            <thead><tr><th>PID</th><th>Owner</th><th title="Nice value: lower runs first">Priority</th><th class="text-end">CPU %</th><th class="text-end">Memory %</th><th>Running</th><th>Command</th><?php if ($canManage): ?><th class="text-end">Operate</th><?php endif; ?></tr></thead>
            <tbody>
            <?php foreach ($procs as $p): $sus = (array) ($p['suspicious'] ?? []); $kind = $sus !== [] ? 'suspicious user' : (!empty($p['kernel']) ? 'kernel' : 'user'); ?>
                <tr data-nim-row data-dt-kind="<?= e($kind) ?>" data-dt-owner="<?= e((string) $p['user']) ?>"<?= $sus !== [] ? ' class="table-danger"' : '' ?>>
                    <td class="font-monospace"><?= (int) $p['pid'] ?></td>
                    <td><?= e((string) $p['user']) ?><?php if (!empty($p['unit']) && ctype_digit((string) $p['user'])): ?><div class="small nim-muted" title="A temporary account systemd made for this service"><?= e(preg_replace('/\.service$/', '', (string) $p['unit'])) ?></div><?php endif; ?></td>
                    <td><?= (int) $p['nice'] ?></td>
                    <td class="text-end font-monospace"><?= number_format((float) $p['cpu_percent'], 1) ?></td>
                    <td class="text-end font-monospace"><?= number_format((float) $p['mem_percent'], 1) ?></td>
                    <td class="small text-nowrap"><?= e($duration((int) $p['seconds'])) ?></td>
                    <td class="small">
                        <div class="font-monospace text-break nim-proc-cmd"<?= !empty($p['exe']) ? ' title="' . e('Program: ' . $p['exe'] . (!empty($p['cwd']) ? "\nFolder: " . $p['cwd'] : '')) . '"' : '' ?>><?= e((string) $p['command']) ?></div>
                        <?php if (!empty($p['listens'])): ?><div class="nim-muted">listens on <?= e(implode(', ', (array) $p['listens'])) ?></div><?php endif; ?>
                        <?php foreach ((array) ($p['notes'] ?? []) as $note): ?><div class="text-body-secondary"><i class="bi bi-info-circle me-1" aria-hidden="true"></i><?= e((string) $note) ?><?= !empty($p['unit']) && str_ends_with((string) $p['unit'], '.service') ? ' (' . e((string) $p['unit']) . ')' : '' ?></div><?php endforeach; ?>
                        <?php foreach ($sus as $why): ?><div class="text-danger"><i class="bi bi-exclamation-triangle me-1" aria-hidden="true"></i><?= e((string) $why) ?></div><?php endforeach; ?>
                    </td>
                    <?php if ($canManage): ?>
                    <td class="text-end text-nowrap">
                        <?php if (empty($p['kernel']) && (int) $p['pid'] > 1): ?>
                            <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('monitoring.processes.trace', [], ['pid' => (int) $p['pid'], 'start' => (int) $p['start']])) ?>" title="Trace">Trace</a>
                            <?php foreach (['TERM' => ['Stop', 'btn-outline-warning', 'Ask process %d to stop (TERM)? Most programs finish cleanly.'], 'KILL' => ['Kill', 'btn-outline-danger', 'Kill process %d at once (KILL)? It cannot save anything first.']] as $sig => [$label, $cls, $q]): ?>
                                <form class="d-inline" method="post" action="<?= e($this->url('monitoring.processes.signal')) ?>" data-nim-noajax data-nim-confirm="<?= e(sprintf($q, (int) $p['pid']) . ' ' . mb_substr((string) $p['command'], 0, 120)) ?>">
                                    <?= $this->csrfField() ?><input type="hidden" name="pid" value="<?= (int) $p['pid'] ?>"><input type="hidden" name="start" value="<?= (int) $p['start'] ?>"><input type="hidden" name="signal" value="<?= $sig ?>"><input type="hidden" name="command" value="<?= e(mb_substr((string) $p['command'], 0, 200)) ?>"><input type="hidden" name="show" value="<?= e($show) ?>">
                                    <button class="btn btn-sm <?= $cls ?>"><?= $label ?></button>
                                </form>
                            <?php endforeach; ?>
                        <?php endif; ?>
                    </td>
                    <?php endif; ?>
                </tr>
            <?php endforeach; ?>
            <tr class="d-none" data-nim-dt-empty><td colspan="8" class="text-center py-4 nim-muted">No process matches.</td></tr>
            </tbody>
        </table>
    </div>
    <div class="nim-pager" data-nim-dt-pager></div>
</div>
<p class="small nim-muted mt-2">CPU % is the share of one CPU over half a second (like top). Suspicious: programs named like miners or known backdoors, programs deleted from disk (not merely replaced by an update) or run from /tmp, /dev/shm or hidden folders, scripts started from temporary folders, user programs pretending to be kernel threads, and shells or interpreters of website accounts listening on a port.</p>
