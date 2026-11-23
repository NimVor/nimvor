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
// The live metric tiles draw their own card (they have no title bar, link or refresh button).
if ($def->frameless) {
    echo $this->partial('dashboard/widgets/' . $def->id, ['d' => $result['data']]);
    return;
}
?>
<div class="card nim-widget h-100" data-widget="<?= e($def->id) ?>" data-state="<?= e($state) ?>">
    <div class="card-header nim-widget-head">
        <span class="nim-widget-title"><i class="bi bi-<?= e($def->icon) ?>" aria-hidden="true"></i><span><?= e($def->title) ?></span></span>
        <span class="nim-widget-actions">
            <?php if ($def->linkRoute !== '' && $this->hasRoute($def->linkRoute) && $this->routeAllowed($def->linkRoute)): ?><a class="nim-widget-link" href="<?= e($this->url($def->linkRoute)) ?>"><?= e($def->linkLabel) ?></a><?php endif; ?>
            <time class="nim-widget-meta" data-widget-updated title="Measured at <?= e(gmdate('H:i:s', $result['at'])) ?> UTC"><?= e(gmdate('H:i', $result['at'])) ?></time>
            <button type="button" class="nim-widget-refresh" data-widget-refresh title="Refresh" aria-label="Refresh <?= e($def->title) ?>"><i class="bi bi-arrow-clockwise" aria-hidden="true"></i></button>
        </span>
    </div>
    <div class="card-body nim-widget-body">
        <?php if ($state === 'ok'): ?>
            <?= $this->partial('dashboard/widgets/' . $def->id, ['d' => $result['data']]) ?>
        <?php elseif ($state === 'empty'): ?>
            <div class="nim-widget-note"><i class="bi bi-inbox" aria-hidden="true"></i>Nothing to show yet.</div>
        <?php elseif ($state === 'unavailable'): ?>
            <div class="nim-widget-note"><i class="bi bi-plug" aria-hidden="true"></i>Not available right now: the server's agent did not answer. This is not a zero; the numbers come back when it does.</div>
        <?php else: ?>
            <div class="nim-widget-note is-error"><i class="bi bi-exclamation-triangle" aria-hidden="true"></i>This widget could not be loaded. Use the refresh button to try again.</div>
        <?php endif; ?>
    </div>
</div>
