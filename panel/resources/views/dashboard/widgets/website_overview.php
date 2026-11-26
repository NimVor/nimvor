<?php
/**
 * Website statistics, in tabs: the real HTTP checks (overview and slowest websites), websites created per month, and the websites by
 * state with the discovery's counts, PHP versions and certificates. All drawn on the server.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$segments = [
    ['label' => 'Active', 'value' => $d['active'], 'cls' => 'c-ok'],
    ['label' => 'Disabled', 'value' => $d['disabled'], 'cls' => 'c-muted'],
    ['label' => 'Suspended', 'value' => $d['suspended'], 'cls' => 'c-warn'],
];
$max = max(1, max((array) $d['created']));
$https = $d['total'] > 0 ? (int) round(100 * $d['https'] / $d['total']) : 0;
$unavailable = '<div class="text-body-secondary">Not available right now. The other tabs still show.</div>';
?>
<div data-nim-tabs="website-statistics">
    <?= $this->partial('dashboard/tabs', ['tabs' => ['overview' => 'Overview', 'slowest' => 'Slowest websites', 'created' => 'Created per month', 'websites' => 'Websites'], 'label' => 'Website statistics']) ?>
    <div data-nim-pane="overview">
        <?= $d['health'] === null ? $unavailable : $this->partial('dashboard/widgets/website_health', ['d' => $d['health'], 'part' => 'overview']) ?>
    </div>
    <div data-nim-pane="slowest" hidden>
        <?= $d['health'] === null ? $unavailable : $this->partial('dashboard/widgets/website_health', ['d' => $d['health'], 'part' => 'slowest']) ?>
    </div>
    <div data-nim-pane="created" hidden>
        <div class="nim-bars" role="img" aria-label="Websites created per month">
            <?php foreach ((array) $d['created'] as $month => $n): ?>
                <div class="nim-bar" title="<?= e($month . ': ' . (int) $n . ' website' . ((int) $n === 1 ? '' : 's') . ' created') ?>">
                    <span class="nim-bar-n"><?= (int) $n ?></span>
                    <span class="nim-bar-track"><span class="nim-bar-fill" data-bar-height="<?= e((string) round(100 * $n / $max)) ?>"></span></span>
                    <span class="nim-bar-l"><?= e(gmdate('M', (int) strtotime($month . '-01'))) ?></span>
                </div>
            <?php endforeach; ?>
        </div>
        <div class="nim-widget-foot">Counts websites that still exist.</div>
    </div>
    <div data-nim-pane="websites" hidden>
        <div class="nim-split mb-3">
            <?= $this->partial('dashboard/charts/donut', ['segments' => $segments, 'centerValue' => (string) $d['total'], 'centerLabel' => 'Websites', 'size' => 96]) ?>
            <ul class="nim-legend-list">
                <?php foreach ($segments as $s): ?><li><span class="nim-chart-key <?= e($s['cls']) ?>"></span><span class="me-auto"><?= e($s['label']) ?></span><strong><?= (int) $s['value'] ?></strong></li><?php endforeach; ?>
                <li><span class="nim-chart-key c-info"></span><span class="me-auto">Using HTTPS</span><strong><?= (int) $d['https'] ?> <span class="text-body-secondary fw-normal">(<?= $https ?>%)</span></strong></li>
            </ul>
        </div>
        <?= $d['discovery'] === null ? $unavailable : $this->partial('dashboard/widgets/websites_summary', ['d' => ['summary' => $d['discovery']]]) ?>
    </div>
</div>
