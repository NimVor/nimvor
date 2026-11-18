<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, mixed> $r */
/** @var ?array<string, mixed> $newer */
/** @var ?array<string, mixed> $older */
/** @var \Closure $inline */
/** @var string $repo */
/** @var string $install */
$v->layout('layouts/site');
$v->set('title', 'NimVor ' . $r['version']);
$v->set('description', 'What is new in NimVor ' . $r['version'] . ($r['date'] ? ' (' . gmdate('j F Y', (int) strtotime((string) $r['date'])) . ')' : '') . ': ' . $r['count'] . ' changes, with downloads.');
$date = $r['date'] !== null && strtotime((string) $r['date']) !== false ? gmdate('j F Y', (int) strtotime((string) $r['date'])) : '';
$size = static fn (int $b): string => $b >= 1048576 ? number_format($b / 1048576, 1) . ' MB' : max(1, (int) round($b / 1024)) . ' KB';
$kinds = ['major' => 'Major release', 'minor' => 'Minor release', 'patch' => 'Patch release', 'pre' => 'Pre-release'];
$cmd = $install . ' -s -- --version ' . $r['version'];
$compare = $older !== null && $r['published'] && $older['published'] ? $repo . '/compare/v' . $older['version'] . '...v' . $r['version'] : '';
?>
<?= $v->partial('partials/page-head', ['title' => 'NimVor ' . $r['version'], 'intro' => $date !== '' ? $kinds[$r['kind']] . ' · ' . $date : $kinds[$r['kind']], 'crumbs' => [['Resources', '/resources'], ['Releases', '/resources/releases'], [$r['version'], '']]]) ?>
<section class="section">
    <div class="container rel-detail">
        <div class="rel-detail-main">
            <?php if ($r['summary'] !== ''): ?><p class="rel-lead"><?= $inline($r['summary']) ?></p><?php endif; ?>
            <?php if ($r['sections'] === []): ?><p class="muted">This release has no itemised changes in the changelog.</p><?php endif; ?>
            <?= $v->partial('partials/release-body', ['r' => $r, 'inline' => $inline, 'brief' => $inline, 'limit' => 0]) ?>
            <nav class="rel-pager" aria-label="Other releases">
                <?php if ($older !== null): ?><a href="/resources/releases/<?= e(rawurlencode($older['version'])) ?>">&larr; <?= e($older['version']) ?></a><?php else: ?><span></span><?php endif; ?>
                <a href="/resources/releases">All releases</a>
                <?php if ($newer !== null): ?><a href="/resources/releases/<?= e(rawurlencode($newer['version'])) ?>"><?= e($newer['version']) ?> &rarr;</a><?php else: ?><span></span><?php endif; ?>
            </nav>
        </div>
        <aside class="rel-detail-side">
            <div class="panel">
                <h2>Download</h2>
                <?php if ($r['assets'] !== []): ?>
                    <ul class="rel-assets">
                        <?php foreach ($r['assets'] as $a): ?><li><a href="<?= e($a['url']) ?>" rel="noopener"><?= $v->icon('backup', 16) ?><span><?= e($a['name']) ?></span><small><?= e($size((int) $a['size'])) ?></small></a></li><?php endforeach; ?>
                    </ul>
                    <p class="muted">Check a download against <code>SHA256SUMS</code> before you run it.</p>
                <?php elseif (!$r['published']): ?>
                    <p class="muted">This version was never published on its own; its changes are in the next release.</p>
                <?php else: ?>
                    <p class="muted">The downloads could not be read right now. They are on the <a href="<?= e((string) $r['url']) ?>" rel="noopener">GitHub release</a>.</p>
                <?php endif; ?>
                <?php if ($r['published']): ?>
                    <h3 class="mt-2">Install this version</h3>
                    <?= $v->partial('partials/install', ['command' => $cmd]) ?>
                <?php endif; ?>
            </div>
            <div class="panel">
                <h2>Links</h2>
                <ul class="rel-linklist">
                    <?php if ($r['url'] !== null): ?><li><a href="<?= e((string) $r['url']) ?>" rel="noopener">GitHub release</a></li><?php endif; ?>
                    <?php if ($compare !== ''): ?><li><a href="<?= e($compare) ?>" rel="noopener">Code changes since <?= e($older['version']) ?></a></li><?php endif; ?>
                    <li><a href="<?= e($repo) ?>/blob/main/CHANGELOG.md" rel="noopener">The whole changelog</a></li>
                    <li><a href="/docs/security-policy">Report a vulnerability</a></li>
                </ul>
            </div>
        </aside>
    </div>
</section>
