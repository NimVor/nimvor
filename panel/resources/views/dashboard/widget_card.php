<?php
/**
 * The frame every dashboard widget is drawn in: title, link, refresh button, the
 * widget's own body, or an honest message when it has none to show.
 *
 * @var \NimVor\View\View $this
 * @var \NimVor\Dashboard\Widget $def
 * @var array{state: string, data: array<string, mixed>, at: int} $result
 */
$state = $result['state'];
?>
<div class="card h-100" data-widget="<?= e($def->id) ?>" data-state="<?= e($state) ?>">
    <div class="card-header d-flex justify-content-between align-items-center gap-2">
        <span><i class="bi bi-<?= e($def->icon) ?> me-1" aria-hidden="true"></i><?= e($def->title) ?></span>
        <span class="d-flex align-items-center gap-2">
            <?php if ($def->linkRoute !== '' && $this->hasRoute($def->linkRoute) && $this->routeAllowed($def->linkRoute)): ?><a class="small" href="<?= e($this->url($def->linkRoute)) ?>"><?= e($def->linkLabel) ?></a><?php endif; ?>
            <button type="button" class="btn btn-sm btn-link p-0 nim-widget-refresh" data-widget-refresh title="Refresh" aria-label="Refresh <?= e($def->title) ?>"><i class="bi bi-arrow-clockwise" aria-hidden="true"></i></button>
        </span>
    </div>
    <div class="card-body small">
        <?php if ($state === 'ok'): ?>
            <?= $this->partial('dashboard/widgets/' . $def->id, ['d' => $result['data']]) ?>
        <?php elseif ($state === 'empty'): ?>
            <div class="text-body-secondary">Nothing to show yet.</div>
        <?php elseif ($state === 'unavailable'): ?>
            <div class="text-body-secondary"><i class="bi bi-plug me-1" aria-hidden="true"></i>Not available right now: the server's agent did not answer. This is not a zero; the numbers come back when it does.</div>
        <?php else: ?>
            <div class="text-danger"><i class="bi bi-exclamation-triangle me-1" aria-hidden="true"></i>This widget could not be loaded. Use the refresh button to try again.</div>
        <?php endif; ?>
    </div>
    <div class="card-footer small text-body-secondary py-1" data-widget-updated>Updated <?= e(gmdate('H:i:s', $result['at'])) ?> UTC</div>
</div>
