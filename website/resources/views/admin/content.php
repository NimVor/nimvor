<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $pages */
/** @var ?string $syncedAt */
/** @var array<string, mixed> $stats */
/** @var string $repo */
/** @var bool $hasToken */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Docs and stats');
$t = $stats['totals'];
?>
<div class="app-top"><div><h1>Docs and stats</h1><p class="muted mb-0">What the site reads from GitHub and from installers.</p></div></div>
<div class="panel form">
    <h2 class="mb-0">Documentation</h2>
    <p class="muted mb-0"><?= count($pages) ?> page(s) from <a href="<?= e($repo) ?>" rel="noopener"><?= e($repo) ?></a>. <?= $syncedAt === null ? 'Never synced.' : 'Last synced ' . e(gmdate('j M Y H:i', (int) strtotime($syncedAt))) . ' UTC.' ?> The site refreshes them itself every six hours when someone visits; sync now to publish a change at once.</p>
    <form method="post" action="/admin/content/docs-sync" data-once><?= $v->csrf() ?><button class="btn btn-primary" type="submit">Sync documentation now</button></form>
    <p class="muted mb-0"><?= $hasToken ? 'A GitHub token is set (higher rate limit).' : 'No GitHub token is set: GitHub allows 60 anonymous requests an hour, which is enough here.' ?></p>
</div>
<div class="panel form">
    <h2 class="mb-0">Installations reported</h2>
    <div class="tiles">
        <div class="tile"><small>Servers</small><strong><?= number_format($t['total']) ?></strong></div>
        <div class="tile"><small>Reported in 30 days</small><strong><?= number_format($t['active_30']) ?></strong></div>
        <div class="tile"><small>New in 30 days</small><strong><?= number_format($t['new_30']) ?></strong></div>
    </div>
    <p class="mb-0"><a href="/resources/stats">Open the public statistics page</a>. Counts are anonymous and indicative; see the privacy policy for what is sent.</p>
</div>
<?php if ($pages !== []): ?>
<div class="table-wrap"><table class="data"><thead><tr><th>Page</th><th>Group</th><th>Address</th></tr></thead><tbody>
    <?php foreach ($pages as $p): ?>
        <tr><td><?= e($p['label']) ?></td><td><?= e($p['grp']) ?></td><td><a href="<?= e(\NimVorSite\Services\DocsCatalog::url((string) $p['slug'])) ?>"><?= e(\NimVorSite\Services\DocsCatalog::url((string) $p['slug'])) ?></a></td></tr>
    <?php endforeach; ?>
</tbody></table></div>
<?php endif; ?>
