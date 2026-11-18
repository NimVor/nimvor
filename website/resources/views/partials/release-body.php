<?php
/**
 * One release's changes, grouped by category.
 * @var \NimVorSite\Core\View $v
 * @var array<string, mixed> $r
 * @var \Closure $inline
 * @var \Closure $brief the one-line form of a change (used when $limit > 0)
 * @var int $limit how many changes to show per category (0 = all)
 */
$icons = ['added' => 'bolt', 'changed' => 'tool', 'fixed' => 'check', 'security' => 'shield', 'removed' => 'file', 'deprecated' => 'file'];
$href = '/resources/releases/' . rawurlencode((string) $r['version']);
?>
<?php foreach ($r['sections'] as $s):
    $key = isset($icons[$s['key']]) ? $s['key'] : 'other';
    $shown = $limit > 0 ? array_slice($s['items'], 0, $limit) : $s['items'];
    $more = count($s['items']) - count($shown);
    ?>
    <section class="rel-cat rel-cat-<?= e($key) ?>">
        <h4><span class="rel-cat-icon"><?= $v->icon($icons[$key] ?? 'info', 16) ?></span><?= e($s['name']) ?> <span class="rel-n"><?= count($s['items']) ?></span></h4>
        <ul>
            <?php foreach ($shown as $item): ?><li><?= $limit > 0 ? $brief($item) : $inline($item) ?></li><?php endforeach; ?>
        </ul>
        <?php if ($more > 0): ?><a class="link-arrow rel-more" href="<?= e($href) ?>#<?= e($s['key']) ?>"><?= $more ?> more <?= e(strtolower($s['name'])) ?> <?= $v->icon('arrow', 16) ?></a><?php endif; ?>
    </section>
<?php endforeach; ?>
