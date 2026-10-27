<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $site */
/** @var array<string, mixed> $job */
/** @var array{runs: list<array<string, mixed>>, truncated: bool, size: int} $log */
use NimVor\Services\CronService;

$id = (int) $site['id'];
$duration = static function (?string $a, ?string $b): string {
    if ($a === null || $b === null) {
        return '—';
    }
    $s = max(0, (int) strtotime($b) - (int) strtotime($a));
    return $s < 60 ? $s . ' s' : ($s < 3600 ? intdiv($s, 60) . ' min ' . ($s % 60) . ' s' : intdiv($s, 3600) . ' h ' . intdiv($s % 3600, 60) . ' min');
};
$when = static fn (?string $t): string => $t === null ? '' : gmdate('Y-m-d H:i:s', (int) strtotime($t)) . ' UTC';
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'cron']) ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>

<div class="card mb-3">
    <div class="card-body d-flex flex-wrap align-items-center gap-3">
        <div class="nim-minw-0 flex-grow-1">
            <div class="small text-body-secondary"><code><?= e($job['schedule']) ?></code> <?= e(CronService::PRESETS[$job['schedule']] ?? '') ?><?= $job['enabled'] ? '' : ' · paused' ?><?= ($job['email'] ?? '') !== '' ? ' · output mailed to ' . e((string) $job['email']) : '' ?></div>
            <code class="d-block text-break"><?= e($job['command']) ?></code>
            <?php if ($job['description'] !== ''): ?><div class="small text-body-secondary"><?= e($job['description']) ?></div><?php endif; ?>
        </div>
        <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.cron.log', ['id' => $id, 'job' => (int) $job['id']])) ?>"><i class="bi bi-arrow-clockwise me-1"></i>Refresh</a>
        <?php if ($this->can(\NimVor\Security\Permissions::CRON_MANAGE)): ?>
            <form method="post" action="<?= e($this->url('sites.cron.run', ['id' => $id, 'job' => (int) $job['id']])) ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-primary"><i class="bi bi-play me-1"></i>Run now</button></form>
        <?php endif; ?>
        <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.cron', ['id' => $id])) ?>"><i class="bi bi-arrow-left me-1"></i>All cron jobs</a>
    </div>
</div>

<div class="card">
    <div class="card-header nim-toolbar">
        <h2 class="nim-toolbar-title"><i class="bi bi-journal-text me-1"></i>Runs <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($log['runs']) ?></span></h2>
        <span class="small text-body-secondary">logs/cron-<?= (int) $job['id'] ?>.log · <?= e(format_bytes($log['size'])) ?><?= $log['truncated'] ? ' · showing the latest 512 KB' : '' ?></span>
    </div>
    <?php if ($log['runs'] === []): ?>
        <div class="nim-empty-state"><i class="bi bi-journal-text" aria-hidden="true"></i><div class="fw-medium">No runs recorded yet</div><div class="small">Runs appear here after the job's next scheduled time, or use “Run now”.</div></div>
    <?php else: ?>
        <div class="table-responsive">
            <table class="table align-middle mb-0 nim-table" id="cron-runs">
                <thead><tr><th>Started</th><th>Duration</th><th>Result</th><th>Trigger</th><th>Output</th></tr></thead>
                <tbody>
                <?php foreach ($log['runs'] as $r): ?>
                    <?php $lines = $r['output'] === '' ? [] : explode("\n", (string) $r['output']); ?>
                    <tr>
                        <td class="small text-nowrap"><?= $r['start'] !== null ? e($when($r['start'])) : '<span class="text-body-secondary">earlier output</span>' ?></td>
                        <td class="small text-nowrap"><?= e($duration($r['start'], $r['end'])) ?></td>
                        <td class="small text-nowrap">
                            <?php if ($r['start'] === null): ?><span class="text-body-secondary">—</span>
                            <?php elseif ($r['exit'] === null): ?><span class="nim-state is-warn">Running or stopped</span>
                            <?php elseif ($r['exit'] === 0): ?><span class="nim-state is-ok">Success</span>
                            <?php else: ?><span class="nim-state is-error">Failed (exit <?= (int) $r['exit'] ?>)</span><?php endif; ?>
                        </td>
                        <td class="small"><?= $r['start'] === null ? '' : ($r['manual'] ? 'Manual' : 'Scheduled') ?></td>
                        <td class="small nim-minw-0">
                            <?php if ($lines === []): ?><span class="text-body-secondary">no output</span>
                            <?php elseif (count($lines) === 1 && strlen($lines[0]) <= 120): ?><code class="text-break"><?= e($lines[0]) ?></code>
                            <?php else: ?>
                                <details><summary class="text-truncate"><code><?= e(mb_substr($lines[0], 0, 100)) ?></code> <span class="text-body-secondary">(<?= count($lines) ?> lines)</span></summary>
                                    <pre class="nim-pre mt-2 mb-0"><?= e((string) $r['output']) ?></pre></details>
                            <?php endif; ?>
                        </td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
</div>
