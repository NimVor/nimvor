<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $trace
 * @var int $start
 */
$d = (array) ($trace['detail'] ?? []);
?>
<?php $this->start(); ?>Trace of process <?= (int) $trace['pid'] ?><?php $this->end('title'); ?>
<?php $this->start(); ?><?= e((string) $trace['name']) ?><?php $this->end('subtitle'); ?>
<nav class="nim-pagetabs" aria-label="Monitoring"><a href="<?= e($this->url('monitoring')) ?>">History and alerts</a><a href="<?= e($this->url('monitoring.live')) ?>">Live</a><a class="active" href="<?= e($this->url('monitoring.processes')) ?>">Processes</a></nav>
<div class="d-flex gap-2 mb-3">
    <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('monitoring.processes')) ?>"><i class="bi bi-arrow-left me-1"></i>All processes</a>
    <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('monitoring.processes.trace', [], ['pid' => (int) $trace['pid'], 'start' => $start])) ?>"><i class="bi bi-arrow-clockwise me-1"></i>Trace again</a>
</div>
<div class="row g-3">
    <div class="col-xl-5">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-info-circle me-1"></i>Process</div>
            <div class="card-body small">
                <?php if ($d === []): ?>
                    <p class="nim-muted mb-0">The details could not be read<?= !empty($trace['detail_error']) ? ': ' . e(mb_substr((string) $trace['detail_error'], 0, 300)) : '' ?>.</p>
                <?php else: ?>
                    <dl class="row mb-0">
                        <dt class="col-4">Program</dt><dd class="col-8 font-monospace text-break"><?= e((string) ($d['exe'] ?? '—')) ?><?= !empty($d['deleted']) ? ' <span class="nim-state is-error">deleted from disk</span>' : '' ?></dd>
                        <dt class="col-4">Folder</dt><dd class="col-8 font-monospace text-break"><?= e((string) ($d['cwd'] ?? '—')) ?></dd>
                        <dt class="col-4">Root</dt><dd class="col-8 font-monospace text-break"><?= e((string) ($d['root'] ?? '—')) ?></dd>
                        <?php foreach ((array) ($d['status'] ?? []) as $k => $v): ?><dt class="col-4"><?= e((string) $k) ?></dt><dd class="col-8 font-monospace text-break"><?= e((string) $v) ?></dd><?php endforeach; ?>
                    </dl>
                <?php endif; ?>
            </div>
        </div>
    </div>
    <div class="col-xl-7">
        <div class="card h-100">
            <div class="card-header">Open files and connections (<?= count((array) ($d['files'] ?? [])) ?>)</div>
            <div class="card-body"><pre class="nim-db-create small mb-0 nim-update-log"><?= e(implode("\n", (array) ($d['files'] ?? []))) ?: '—' ?></pre></div>
        </div>
    </div>
    <div class="col-12">
        <div class="card">
            <div class="card-header">System calls for five seconds (strace)</div>
            <div class="card-body">
                <?php if (empty($trace['strace_available'])): ?>
                    <p class="small mb-0">strace is not installed. <?php if ($this->hasRoute('packages')): ?>Install it on the <a href="<?= e($this->url('packages')) ?>">Packages</a> page (System call tracer), then trace again.<?php endif; ?></p>
                <?php else: ?>
                    <pre class="nim-db-create small mb-0 nim-update-log"><?= e((string) ($trace['strace'] ?? '')) ?: 'No system calls in five seconds (the process was waiting).' ?></pre>
                <?php endif; ?>
            </div>
        </div>
    </div>
</div>
