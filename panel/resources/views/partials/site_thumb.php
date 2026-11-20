<?php
/**
 * The picture of a website's home page inside a card's thumbnail, with the
 * refresh button; the first letter shows until a picture exists.
 *
 * @var \NimVor\View\View $this
 * @var int $siteId
 * @var string|null $takenAt when the picture was taken
 */
$takenAt ??= null;
?>
<?php if ($takenAt !== null): ?><img class="nim-site-shot" src="<?= e($this->url('sites.thumbnail', ['id' => $siteId])) ?>?v=<?= (int) strtotime($takenAt) ?>" alt="" loading="lazy"><?php endif; ?>
<?php if ($this->hasRoute('sites.thumbnail.refresh') && $this->routeAllowed('sites.thumbnail.refresh')): ?>
    <span class="nim-site-refresh" role="button" tabindex="0" title="Refresh the picture of the home page" aria-label="Refresh the picture of the home page" data-nim-thumb-refresh="<?= e($this->url('sites.thumbnail.refresh', ['id' => $siteId])) ?>"><i class="bi bi-arrow-clockwise" aria-hidden="true"></i></span>
<?php endif; ?>
