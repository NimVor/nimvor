<?php
/** @var \NimVor\View\View $this */
/** Home › website › tool, from the website menu the page shared (or the page's title outside a website). */
$menu = $this->shared('site_menu');
$crumbs = [['Websites', $this->url('websites')]];
if ($menu !== null) {
    $current = null;
    foreach ($menu['items'] as $i) {
        if ($i['key'] === $menu['active']) {
            $current = $i;
        }
    }
    $overview = $menu['active'] === 'overview' || $current === null;
    $crumbs[] = [$menu['site']['domain'], $overview ? null : $this->url('sites.show', ['id' => (int) $menu['site']['id']])];
    if (!$overview) {
        $crumbs[] = [$current['label'], null];
    }
} elseif ($this->section('title') !== '' && !$this->isActive('/websites')) {
    $crumbs[] = [trim(strip_tags($this->section('title'))), null];
}
?>
<nav class="nt-crumbs" aria-label="Breadcrumb">
    <?php foreach ($crumbs as $n => [$label, $url]): ?>
        <?php if ($n > 0): ?><i class="bi bi-chevron-right" aria-hidden="true"></i><?php endif; ?>
        <?php if ($url !== null): ?><a href="<?= e($url) ?>"><?= e($label) ?></a><?php else: ?><span aria-current="page"><?= e($label) ?></span><?php endif; ?>
    <?php endforeach; ?>
</nav>
