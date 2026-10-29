<?php
/** @var \NimVorSite\Core\View $v */
/** @var array{reported: list<array<string, mixed>>, progress: list<array<string, mixed>>, resolved: list<array<string, mixed>>, fetched_at: ?string, old: bool, error: ?string} $board */
/** @var string $status */
/** @var string $repo */
$v->layout('layouts/site');
$v->set('title', 'Problems');
$v->set('description', 'Problems reported to the NimVor project and where each one stands: reported, in progress or resolved.');
$cols = [
    'reported' => ['Reported', 'Waiting to be looked at', 'badge-new'],
    'progress' => ['In progress', 'Someone is working on it', 'badge-warn'],
    'resolved' => ['Resolved', 'Fixed or closed', 'badge-ok'],
];
$total = count($board['reported']) + count($board['progress']) + count($board['resolved']);
$ago = static function (?string $iso): string {
    $t = $iso === null || $iso === '' ? 0 : (int) strtotime($iso);
    if ($t === 0) {
        return '';
    }
    $d = max(0, time() - $t);
    return $d < 3600 ? max(1, intdiv($d, 60)) . ' min ago' : ($d < 86400 ? intdiv($d, 3600) . ' h ago' : ($d < 86400 * 60 ? intdiv($d, 86400) . ' days ago' : gmdate('j M Y', $t)));
};
?>
<?= $v->partial('partials/page-head', ['title' => 'Problems', 'intro' => 'What has been reported, what is being worked on and what is fixed. It is read from the project\'s issues on GitHub.', 'crumbs' => [['Resources', '/resources'], ['Community & source', '/resources/community'], ['Problems', '']]]) ?>
<section class="section">
    <div class="container">
        <div class="cta-row">
            <a class="btn btn-primary" href="<?= e($repo) ?>/issues/new/choose" rel="noopener">Report a problem on GitHub <?= $v->icon('arrow', 16) ?></a>
            <a class="btn btn-outline" href="<?= e($repo) ?>/issues" rel="noopener">All issues on GitHub</a>
            <span class="muted">Search first: your problem may already be here. Security problems: <a href="/docs/security-policy">report them privately</a>.</span>
        </div>
        <?php if ($board['old']): ?>
            <div class="alert alert-info mt-2" role="status"><?= $board['fetched_at'] === null ? 'GitHub could not be reached, so there is nothing to show yet.' : 'GitHub could not be reached; this is the list as it was ' . e($ago($board['fetched_at'])) . '.' ?> <a href="<?= e($repo) ?>/issues" rel="noopener">See the issues on GitHub</a>.</div>
        <?php endif; ?>
        <?php if ($total > 0): ?>
            <div class="tiles mt-2">
                <?php foreach ($cols as $key => [$label, $hint]): ?>
                    <a class="tile" href="/resources/problems?status=<?= e($key) ?>#list"><small><?= e($label) ?></small><strong><?= count($board[$key]) ?></strong><span class="muted"><?= e($hint) ?></span></a>
                <?php endforeach; ?>
            </div>
            <?php $done = count($board['resolved']); ?>
            <p class="muted mt-2 mb-0"><?= $done ?> of <?= $total ?> reported problems are resolved.</p>
            <progress class="meter" max="<?= $total ?>" value="<?= $done ?>" aria-label="Resolved problems"><?= $done ?> of <?= $total ?></progress>
        <?php endif; ?>
    </div>
</section>
<section class="section section-soft" id="list">
    <div class="container">
        <?php if ($total === 0): ?>
            <div class="card empty"><h3>No problems listed</h3><p>When someone reports one on GitHub it appears here, with its progress.</p></div>
        <?php else: ?>
            <div class="board<?= $status !== '' ? ' board-one' : '' ?>">
                <?php foreach ($cols as $key => [$label, $hint, $badge]): if ($status !== '' && $status !== $key) { continue; } ?>
                    <div class="board-col">
                        <h2 class="board-h"><span class="badge <?= e($badge) ?>"><?= e($label) ?></span> <span class="muted"><?= count($board[$key]) ?></span></h2>
                        <?php $shown = $status === $key ? $board[$key] : array_slice($board[$key], 0, 8); ?>
                        <?php if ($shown === []): ?><p class="muted">Nothing here.</p><?php endif; ?>
                        <ul class="issues">
                            <?php foreach ($shown as $i): ?>
                                <li>
                                    <a href="<?= e($i['url']) ?>" rel="noopener"><span class="num">#<?= (int) $i['number'] ?></span> <?= e($i['title']) ?></a>
                                    <div class="meta">
                                        <?php foreach (array_slice((array) $i['labels'], 0, 4) as $l): ?><span class="chip-l"><?= e($l) ?></span><?php endforeach; ?>
                                        <span><?= $key === 'resolved' ? ($i['reason'] === 'not_planned' ? 'closed (not planned) ' : 'closed ') . e($ago($i['closed_at'])) : 'opened ' . e($ago($i['created_at'])) ?></span>
                                        <?php if ((int) $i['comments'] > 0): ?><span><?= (int) $i['comments'] ?> comment<?= (int) $i['comments'] === 1 ? '' : 's' ?></span><?php endif; ?>
                                    </div>
                                </li>
                            <?php endforeach; ?>
                        </ul>
                        <?php if ($status !== $key && count($board[$key]) > 8): ?><a class="link-arrow" href="/resources/problems?status=<?= e($key) ?>#list">Show all <?= count($board[$key]) ?> <?= $v->icon('arrow', 16) ?></a><?php endif; ?>
                    </div>
                <?php endforeach; ?>
            </div>
            <?php if ($status !== ''): ?><p class="mt-2"><a href="/resources/problems#list">&larr; Back to the board</a></p><?php endif; ?>
            <p class="muted mt-2">Open issues are <em>in progress</em> when someone is assigned or they carry a label such as “in progress” or “confirmed”. The list shows the 200 most recently updated issues<?= $board['fetched_at'] !== null ? '; checked ' . e($ago($board['fetched_at'])) : '' ?>.</p>
        <?php endif; ?>
    </div>
</section>
