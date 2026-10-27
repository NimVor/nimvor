<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $site
 * @var array<string, mixed> $install
 * @var array{path: string, exists: bool, lines: list<string>, size: int} $log
 */
$id = (int) $site['id'];
$wid = (int) $install['id'];
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'wordpress']) ?>

<div class="d-flex flex-wrap align-items-center gap-2 mb-3">
    <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.wordpress.show', ['id' => $id, 'wp' => $wid])) ?>"><i class="bi bi-arrow-left me-1" aria-hidden="true"></i>Back to WordPress</a>
    <div class="fw-semibold me-auto">WordPress logs <span class="text-body-secondary small">in <code><?= e($install['path'] === '' ? '/' : $install['path']) ?></code></span></div>
    <?php if ($this->hasRoute('sites.logs')): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.logs', ['id' => $id])) ?>"><i class="bi bi-journal-text me-1" aria-hidden="true"></i>Web server and PHP logs</a><?php endif; ?>
</div>

<div class="wcp-panel">
    <div class="wcp-actionbar">
        <div class="fw-semibold">Debug log <span class="text-body-secondary small fw-normal"><code><?= e($log['path']) ?></code><?= $log['exists'] ? ' · ' . e(format_bytes($log['size'])) : '' ?></span></div>
        <div class="wcp-actionbar-end"><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.wordpress.logs', ['id' => $id, 'wp' => $wid])) ?>"><i class="bi bi-arrow-repeat me-1" aria-hidden="true"></i>Refresh</a></div>
    </div>
    <?php if (!$log['exists']): ?>
        <div class="wcp-empty-state"><i class="bi bi-journal" aria-hidden="true"></i><div class="fw-medium">No debug log</div><div class="small">WordPress writes one when debugging and "Write errors to wp-content/debug.log" are on (Dashboard → Debugging).</div></div>
    <?php elseif ($log['lines'] === []): ?>
        <div class="wcp-empty-state"><i class="bi bi-check-circle" aria-hidden="true"></i><div>The debug log is empty.</div></div>
    <?php else: ?>
        <pre class="wcp-pre wcp-logview mb-0"><?php foreach (array_reverse($log['lines']) as $l): ?><?= e($l) . "\n" ?><?php endforeach; ?></pre>
        <div class="small text-body-secondary mt-1">Newest first; the last <?= count($log['lines']) ?> lines.</div>
    <?php endif; ?>
</div>
