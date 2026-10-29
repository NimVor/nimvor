<?php
/** @var \NimVorSite\Core\View $v */
/** @var array{totals: array{total: int, active_30: int, new_30: int, latest: ?string}, versions: array<string, int>, systems: array<string, int>, arch: array<string, int>, months: array<string, int>} $stats */
$v->layout('layouts/site');
$v->set('title', 'Statistics');
$v->set('description', 'How many servers run NimVor: installations over time, versions and systems. Anonymous counts.');
$t = $stats['totals'];
$bars = static function (array $rows, int $total) use ($v): string {
    if ($rows === []) {
        return '<p class="muted">No data yet.</p>';
    }
    $out = '<ul class="stat-list">';
    foreach ($rows as $label => $n) {
        $pct = $total > 0 ? (int) round($n * 100 / $total) : 0;
        $out .= '<li class="stat-row"><span>' . e($label) . '</span><span class="muted">' . $n . ' · ' . $pct . '%</span><progress class="meter" max="' . max(1, $total) . '" value="' . $n . '" aria-label="' . e($label) . '"></progress></li>';
    }
    return $out . '</ul>';
};
?>
<?= $v->partial('partials/page-head', ['title' => 'Statistics', 'intro' => 'How many servers run NimVor. The numbers come from anonymous reports the installer sends; see below for exactly what.', 'crumbs' => [['Resources', '/resources'], ['Statistics', '']]]) ?>
<section class="section">
    <div class="container">
        <div class="tiles">
            <div class="tile"><small>Servers installed</small><strong><?= number_format($t['total']) ?></strong><span class="muted">since the first report</span></div>
            <div class="tile"><small>Reported in 30 days</small><strong><?= number_format($t['active_30']) ?></strong><span class="muted">installed or upgraded</span></div>
            <div class="tile"><small>New in 30 days</small><strong><?= number_format($t['new_30']) ?></strong><span class="muted">first-time installs</span></div>
        </div>
        <?php if ($t['total'] === 0): ?>
            <div class="card empty mt-3"><h3>No reports yet</h3><p>The first installation that reports itself will appear here.</p></div>
        <?php else: ?>
            <h2 class="mt-3">New servers per month</h2>
            <div class="panel"><?= $v->barChart($stats['months'], 'new servers') ?><div class="bars-axis"><span><?= e((string) array_key_first($stats['months'])) ?></span><span><?= e((string) array_key_last($stats['months'])) ?></span></div></div>
            <div class="grid grid-3 mt-3">
                <div><h3>Versions</h3><?= $bars($stats['versions'], $t['total']) ?></div>
                <div><h3>Systems</h3><?= $bars($stats['systems'], $t['total']) ?></div>
                <div><h3>Processors</h3><?= $bars($stats['arch'], $t['total']) ?></div>
            </div>
        <?php endif; ?>
    </div>
</section>
<section class="section section-soft">
    <div class="container narrow prose">
        <h2 class="mt-0">What is reported, and what is not</h2>
        <p>When NimVor is installed or upgraded, the installer sends <strong>one small report</strong> to this site:</p>
        <ul>
            <li>a random identifier the installer made (not derived from anything on the server),</li>
            <li>the NimVor version,</li>
            <li>the operating system's name and version (for example <code>ubuntu 24.04</code>) and the processor type (<code>amd64</code> or <code>arm64</code>).</li>
        </ul>
        <p>No host name, address, user name, website, or setting is sent, and this site does not keep the sender's address (it is counted for an hour only to limit abuse). The numbers are indicative: anyone could send a report.</p>
        <p>The report is part of installing NimVor: it is what these numbers are made from. See the <a href="/docs/installation">installation guide</a> and the <a href="/support/privacy">privacy policy</a>.</p>
    </div>
</section>
