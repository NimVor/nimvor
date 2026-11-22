<?php
/** @var \NimVorSite\Core\View $v */
/** @var array{releases: list<array<string, mixed>>, upcoming: ?array<string, mixed>, fetched_at: ?string, old: bool, error: ?string} $history */
/** @var string $series */
/** @var list<string> $seriesList */
/** @var \Closure $inline */
/** @var \Closure $brief */
/** @var string $repo */
$v->layout('layouts/site');
$v->set('title', 'Releases');
$v->set('description', 'Every NimVor release: what was added, changed, fixed and secured in each version, with its downloads.');
$all = $history['releases'];
$latest = null;
foreach ($all as $r) {
    if ($r['kind'] !== 'pre') {
        $latest = $r;
        break;
    }
}
$date = static fn (?string $d): string => $d === null || $d === '' || strtotime($d) === false ? '' : gmdate('j F Y', (int) strtotime($d));
$size = static fn (int $b): string => $b >= 1048576 ? number_format($b / 1048576, 1) . ' MB' : max(1, (int) round($b / 1024)) . ' KB';
$rows = $series === '' ? $all : array_values(array_filter($all, static fn (array $r): bool => str_starts_with($r['version'], $series . '.')));
$kinds = ['major' => ['Major', 'badge-new'], 'minor' => ['Minor', ''], 'patch' => ['Patch', 'badge-off'], 'pre' => ['Pre-release', 'badge-warn']];
$totals = ['added' => 0, 'changed' => 0, 'fixed' => 0, 'security' => 0];
foreach ($all as $r) {
    foreach ($r['sections'] as $s) {
        if (isset($totals[$s['key']])) {
            $totals[$s['key']] += count($s['items']);
        }
    }
}
$first = null;
foreach (array_reverse($all) as $r) {
    if ($r['date'] !== null) {
        $first = $r['date'];
        break;
    }
}
?>
<?= $v->partial('partials/page-head', ['title' => 'Releases', 'intro' => 'Every version of NimVor, what changed in it and where to download it. Written from the project\'s own changelog.', 'crumbs' => [['Resources', '/resources'], ['Releases', '']]]) ?>
<section class="section">
    <div class="container">
        <?php if ($history['old']): ?>
            <div class="alert alert-info" role="status"><?= $all === [] ? 'GitHub could not be reached, so there is nothing to show yet.' : 'GitHub could not be reached; this is the list as it was last read.' ?> <a href="<?= e($repo) ?>/releases" rel="noopener">See the releases on GitHub</a>.</div>
        <?php endif; ?>
        <?php if ($latest !== null): ?>
            <article class="rel-hero">
                <div class="rel-hero-main">
                    <span class="badge badge-ok">Latest release</span>
                    <h2 class="rel-ver"><?= e($latest['version']) ?></h2>
                    <p class="muted rel-date"><?= e($date($latest['date'])) ?></p>
                    <?php if ($latest['summary'] !== ''): ?><p class="rel-summary"><?= $inline($latest['summary']) ?></p><?php endif; ?>
                    <div class="cta-row">
                        <a class="btn btn-primary" href="/resources/releases/<?= e(rawurlencode($latest['version'])) ?>">Read the release notes <?= $v->icon('arrow', 16) ?></a>
                        <a class="btn btn-outline" href="/get-started">Install NimVor</a>
                    </div>
                    <?php if ($latest['assets'] !== []): ?>
                        <ul class="rel-assets">
                            <?php foreach ($latest['assets'] as $a): ?><li><a href="<?= e($a['url']) ?>" rel="noopener"><?= $v->icon('backup', 16) ?><span><?= e($a['name']) ?></span><small><?= e($size((int) $a['size'])) ?></small></a></li><?php endforeach; ?>
                        </ul>
                    <?php endif; ?>
                </div>
                <div class="rel-hero-side">
                    <h3>In this release</h3>
                    <ul class="rel-counts">
                        <?php foreach ($latest['sections'] as $s): ?><li><strong><?= count($s['items']) ?></strong> <?= e($s['name']) ?></li><?php endforeach; ?>
                    </ul>
                    <?php $hl = []; foreach ($latest['sections'] as $s) { foreach ($s['items'] as $it) { if (count($hl) < 4 && in_array($s['key'], ['added', 'security'], true)) { $hl[] = $it; } } } ?>
                    <?php if ($hl !== []): ?>
                        <h3>Highlights</h3>
                        <ul class="rel-highlights">
                            <?php foreach ($hl as $it): ?><li><?= ($brief)($it) ?></li><?php endforeach; ?>
                        </ul>
                    <?php endif; ?>
                    <p class="rel-upgrade mb-0">Upgrade an existing server with <code>sudo nimvor update</code>.</p>
                </div>
            </article>
            <div class="tiles mt-2">
                <div class="tile"><small>Releases</small><strong><?= count($all) ?></strong><span class="muted">since <?= e($date($first)) ?></span></div>
                <div class="tile"><small>Features added</small><strong><?= $totals['added'] ?></strong><span class="muted">across all releases</span></div>
                <div class="tile"><small>Problems fixed</small><strong><?= $totals['fixed'] ?></strong><span class="muted">and <?= $totals['security'] ?> security notes</span></div>
            </div>
        <?php endif; ?>
    </div>
</section>
<section class="section section-soft" id="history">
    <div class="container">
        <?php if ($history['upcoming'] !== null): ?>
            <article class="rel-card rel-upcoming">
                <header><span class="badge badge-warn">Coming next</span><h3>Not released yet</h3></header>
                <p class="muted">What is already in the code and will be in the next release.</p>
                <?= $v->partial('partials/release-body', ['r' => $history['upcoming'], 'inline' => $inline, 'brief' => $brief, 'limit' => 4]) ?>
            </article>
        <?php endif; ?>
        <?php if ($all === []): ?>
            <div class="card empty"><h3>No releases listed</h3><p>When the changelog can be read, every release appears here.</p></div>
        <?php else: ?>
            <nav class="rel-filter" aria-label="Filter by version">
                <a href="/resources/releases#history"<?= $series === '' ? ' aria-current="true"' : '' ?>>All versions</a>
                <?php foreach ($seriesList as $sv): ?><a href="/resources/releases?series=<?= e($sv) ?>#history"<?= $series === $sv ? ' aria-current="true"' : '' ?>><?= e($sv) ?>.x</a><?php endforeach; ?>
            </nav>
            <ol class="rel-timeline">
                <?php foreach ($rows as $r): [$kl, $kb] = $kinds[$r['kind']] ?? ['Release', '']; $href = '/resources/releases/' . rawurlencode($r['version']); ?>
                    <li id="v<?= e($r['version']) ?>">
                        <article class="rel-card">
                            <header>
                                <h3><a href="<?= e($href) ?>"><?= e($r['version']) ?></a></h3>
                                <span class="badge <?= e($kb) ?>"><?= e($kl) ?></span>
                                <?php if ($date($r['date']) !== ''): ?><time class="muted" datetime="<?= e((string) $r['date']) ?>"><?= e($date($r['date'])) ?></time><?php endif; ?>
                                <span class="rel-links">
                                    <?php if (!$r['published']): ?><span class="muted">included in the next release</span><?php else: ?><a href="<?= e((string) $r['url']) ?>" rel="noopener">GitHub release</a><?php endif; ?>
                                </span>
                            </header>
                            <?php if ($r['summary'] !== ''): ?><p class="rel-summary"><?= $inline($r['summary']) ?></p><?php endif; ?>
                            <?= $v->partial('partials/release-body', ['r' => $r, 'inline' => $inline, 'brief' => $brief, 'limit' => 4]) ?>
                            <a class="link-arrow" href="<?= e($href) ?>">Release notes for <?= e($r['version']) ?> <?= $v->icon('arrow', 16) ?></a>
                        </article>
                    </li>
                <?php endforeach; ?>
            </ol>
        <?php endif; ?>
        <p class="muted mt-2 mb-0">Release notes follow <a href="https://keepachangelog.com/en/1.1.0/" rel="noopener">Keep a Changelog</a>; the full file is <a href="<?= e($repo) ?>/blob/main/CHANGELOG.md" rel="noopener">CHANGELOG.md</a> in the repository. Versions follow <a href="https://semver.org/" rel="noopener">Semantic Versioning</a>.</p>
    </div>
</section>
