<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, mixed> $report */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Reports');
$top = $report['top_pages'];
$max = max(1, ...array_column($top ?: [['hits' => 1]], 'hits'));
$status = static function (array $counts): string {
    $max = max(1, ...array_values($counts ?: [0]));
    $out = '<div class="bar-list">';
    foreach ($counts as $label => $n) {
        $out .= '<div class="bar-row"><div>' . e(str_replace('_', ' ', (string) $label)) . '<progress class="meter" value="' . (int) $n . '" max="' . $max . '"></progress></div><div class="num">' . (int) $n . '</div></div>';
    }
    return $out . '</div>';
};
?>
<div class="app-top"><div><h1>Reports</h1><p class="muted mb-0">Generated <?= e($report['generated_at']) ?> UTC. Cached for a minute.</p></div>
    <div class="cta-row flush">
        <a class="btn btn-outline btn-sm" href="/admin/reports/users.csv">Users CSV</a>
        <a class="btn btn-outline btn-sm" href="/admin/reports/requests.csv">Requests CSV</a>
        <a class="btn btn-outline btn-sm" href="/admin/reports/messages.csv">Messages CSV</a>
    </div></div>
<div class="tiles">
    <div class="tile"><small>Users</small><strong><?= (int) $report['totals']['users'] ?></strong></div>
    <div class="tile"><small>Clients</small><strong><?= (int) $report['totals']['clients'] ?></strong></div>
    <div class="tile"><small>Administrators</small><strong><?= (int) $report['totals']['admins'] ?></strong></div>
    <div class="tile"><small>Published posts</small><strong><?= (int) $report['totals']['posts'] ?></strong></div>
    <div class="tile"><small>Sponsors</small><strong><?= (int) $report['totals']['sponsors'] ?></strong></div>
</div>
<div class="panel"><h2>Sign-ups, last 30 days</h2><?= $v->barChart($report['signups'], 'sign-ups') ?>
    <div class="bars-axis"><span><?= e(array_key_first($report['signups'])) ?></span><span><?= e(array_key_last($report['signups'])) ?></span></div></div>
<div class="panel"><h2>Visits, last 14 days</h2><?= $v->barChart($report['visits'], 'visits') ?>
    <div class="bars-axis"><span><?= e(array_key_first($report['visits'])) ?></span><span><?= e(array_key_last($report['visits'])) ?></span></div></div>
<div class="grid grid-2">
    <div class="panel"><h2>Messages by status</h2><?= $status($report['messages']) ?></div>
    <div class="panel"><h2>Build requests by status</h2><?= $status($report['requests']) ?></div>
</div>
<div class="panel"><h2>Most visited pages, last 14 days</h2>
    <?php if ($top === []): ?><p class="muted mb-0">No visits recorded yet.</p><?php else: ?>
        <div class="bar-list"><?php foreach ($top as $row): ?>
            <div class="bar-row"><div><code><?= e($row['route']) ?></code><progress class="meter" value="<?= (int) $row['hits'] ?>" max="<?= (int) $max ?>"></progress></div><div class="num"><?= (int) $row['hits'] ?></div></div>
        <?php endforeach; ?></div>
    <?php endif; ?>
    <p class="muted mt-2 mb-0">Visits are counted per page and day. No address or identifier is stored.</p>
</div>
