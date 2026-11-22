<?php
/**
 * @var \NimVorSite\Core\View $v
 * @var int $days
 * @var array{version: string, os: string} $f
 * @var list<int> $periods
 * @var list<string> $versionsList
 * @var list<string> $systemsList
 * @var array<string, mixed> $totals
 * @var array<string, array<string, int>> $features
 */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Telemetry');
$q = static fn (array $over = []): string => '?' . http_build_query(array_filter(['days' => $days, 'version' => $f['version'], 'os' => $f['os']] + [], static fn (mixed $x): bool => $x !== '' && $x !== null) + $over);
$pct = static fn (int $n, int $of): string => $of > 0 ? round(100 * $n / $of) . '%' : 'N/A';
$bars = static function (array $counts, int $of = 0): string {
    if ($counts === []) {
        return '<p class="muted mb-0">N/A: no installation has reported this yet.</p>';
    }
    $max = max(1, ...array_values($counts));
    $out = '<div class="bar-list">';
    foreach ($counts as $label => $n) {
        $out .= '<div class="bar-row"><div>' . e((string) $label) . '<progress class="meter" value="' . (int) $n . '" max="' . $max . '"></progress></div><div class="num">' . (int) $n . ($of > 0 ? ' <span class="muted">' . round(100 * $n / $of) . '%</span>' : '') . '</div></div>';
    }
    return $out . '</div>';
};
$active = $totals['active'];
$activeSeries = $activeDaily;
$hl = $health['reporting'];
?>
<div class="app-top"><div><h1>Telemetry</h1><p class="muted mb-0">What installations that chose to report have sent. Counts, versions and yes/no answers only; no address, host name, domain or person is stored. <a href="https://github.com/NimVor/nimvor/blob/main/docs/telemetry/README.md" rel="noopener">What is collected</a>.</p></div>
    <div class="cta-row flush">
        <?php foreach (['versions' => 'Versions', 'systems' => 'Systems', 'software' => 'Software', 'features' => 'Features', 'active' => 'Active per day'] as $k => $text): ?>
            <a class="btn btn-outline btn-sm" href="/admin/telemetry/export.csv<?= e($q(['what' => $k])) ?>"><?= e($text) ?> CSV</a>
        <?php endforeach; ?>
    </div></div>
<form class="filters" method="get" action="/admin/telemetry">
    <select class="select" name="days" aria-label="Period"><?php foreach ($periods as $p): ?><option value="<?= $p ?>"<?= $days === $p ? ' selected' : '' ?>>Last <?= $p ?> days</option><?php endforeach; ?></select>
    <select class="select" name="version" aria-label="Version"><option value="">Any version</option><?php foreach ($versionsList as $x): ?><option<?= $f['version'] === $x ? ' selected' : '' ?>><?= e($x) ?></option><?php endforeach; ?></select>
    <select class="select" name="os" aria-label="Operating system"><option value="">Any system</option><?php foreach ($systemsList as $x): ?><option<?= $f['os'] === $x ? ' selected' : '' ?>><?= e($x) ?></option><?php endforeach; ?></select>
    <button class="btn btn-outline btn-sm" type="submit">Apply</button>
</form>
<?php if ($totals['registered'] === 0): ?>
    <div class="panel empty"><strong>No installation has registered yet.</strong> Reporting is off until a server's administrator turns it on (Settings → Telemetry in the panel), so these pages stay empty until then. Nothing is estimated or filled in.</div>
<?php endif; ?>
<div class="tiles">
    <div class="tile"><small>Active, last <?= $days ?> days</small><strong><?= $active ?></strong></div>
    <div class="tile"><small>Active, last 7 days</small><strong><?= (int) $totals['active_7'] ?></strong></div>
    <div class="tile"><small>New in the period</small><strong><?= (int) $totals['new_period'] ?></strong></div>
    <div class="tile"><small>Registered, ever</small><strong><?= (int) $totals['registered'] ?></strong></div>
    <div class="tile"><small>Stopped reporting</small><strong><?= (int) $totals['unregistered'] ?></strong></div>
</div>
<p class="muted">Only installations whose administrator opted in are counted, so these are lower bounds, not the number of NimVor servers. Percentages are of the installations that sent that kind of report (<?= (int) $totals['reporting_inventory'] ?> inventory, <?= $hl ?> health).</p>
<div class="grid grid-2">
    <div class="panel"><h2>Installations reporting each day</h2><?php if (array_sum($activeSeries) === 0): ?><p class="muted mb-0">N/A: no heartbeat in the last <?= (int) $chartDays ?> days.</p><?php else: ?><?= $v->barChart($activeSeries, 'installations') ?><div class="bars-axis"><span><?= e(array_key_first($activeSeries)) ?></span><span><?= e(array_key_last($activeSeries)) ?></span></div><?php endif; ?></div>
    <div class="panel"><h2>New registrations</h2><?php if (array_sum($registrations) === 0): ?><p class="muted mb-0">N/A: none in the last <?= (int) $chartDays ?> days.</p><?php else: ?><?= $v->barChart($registrations, 'registrations') ?><div class="bars-axis"><span><?= e(array_key_first($registrations)) ?></span><span><?= e(array_key_last($registrations)) ?></span></div><?php endif; ?></div>
</div>
<div class="grid grid-2">
    <div class="panel"><h2>NimVor versions</h2><?= $bars($versions, $active) ?></div>
    <div class="panel"><h2>Operating systems</h2><?= $bars($systems, $totals['reporting_inventory']) ?></div>
    <div class="panel"><h2>Architecture</h2><?= $bars($arch, $totals['reporting_inventory']) ?></div>
    <div class="panel"><h2>Virtualisation</h2><?= $bars($virt, $totals['reporting_inventory']) ?></div>
    <div class="panel"><h2>CPU cores</h2><?= $bars($cpu, $totals['reporting_inventory']) ?></div>
    <div class="panel"><h2>Memory (MB, rounded up)</h2><?= $bars($memory, $totals['reporting_inventory']) ?></div>
    <div class="panel"><h2>Disk (GB, rounded up)</h2><?= $bars($disk, $totals['reporting_inventory']) ?></div>
    <div class="panel"><h2>Agent versions</h2><?= $bars($agents, $active) ?></div>
</div>
<div class="panel"><h2>Software installed</h2>
    <?php if ($software === []): ?><p class="muted mb-0">N/A: no software inventory yet.</p><?php else: ?>
        <div class="table-wrap"><table class="data"><thead><tr><th>Component</th><th>Installations</th><th>Share</th><th>Running</th></tr></thead><tbody>
            <?php foreach ($software as $name => $s): ?><tr><td><?= e($name) ?></td><td><?= (int) $s['installed'] ?></td><td><?= e($pct($s['installed'], $totals['reporting_inventory'])) ?></td><td><?= (int) $s['running'] ?></td></tr><?php endforeach; ?>
        </tbody></table></div>
    <?php endif; ?></div>
<div class="panel"><h2>Features in use</h2>
    <?php if ($features === []): ?><p class="muted mb-0">N/A: no feature report yet. Only counts of things (for example how many websites) are sent, never their names.</p><?php else: ?>
        <div class="table-wrap"><table class="data"><thead><tr><th>Feature</th><th>Installations using it</th><th>Share</th><th>Items in total</th></tr></thead><tbody>
            <?php foreach ($features as $name => $s): ?><tr><td><?= e(str_replace('_', ' ', $name)) ?></td><td><?= (int) $s['using'] ?> of <?= (int) $s['reporting'] ?></td><td><?= e($pct($s['using'], $s['reporting'])) ?></td><td><?= (int) $s['total'] ?></td></tr><?php endforeach; ?>
        </tbody></table></div>
    <?php endif; ?></div>
<div class="grid grid-2">
    <div class="panel"><h2>Health, latest report of each</h2>
        <?php if ($hl === 0): ?><p class="muted mb-0">N/A: no health report yet.</p><?php else: ?>
            <div class="table-wrap"><table class="data"><tbody>
                <tr><td>With a failed service</td><td><?= (int) $health['with_failed_services'] ?> of <?= $hl ?> (<?= e($pct($health['with_failed_services'], $hl)) ?>)</td></tr>
                <tr><td>Updates pending</td><td><?= (int) $health['updates_pending'] ?> of <?= $hl ?> (<?= e($pct($health['updates_pending'], $hl)) ?>)</td></tr>
                <tr><td>Last backup failed</td><td><?= (int) $health['backup_failing'] ?> of <?= $hl ?> (<?= e($pct($health['backup_failing'], $hl)) ?>)</td></tr>
                <tr><td>A certificate about to expire</td><td><?= (int) $health['ssl_expiring'] ?> of <?= $hl ?> (<?= e($pct($health['ssl_expiring'], $hl)) ?>)</td></tr>
                <tr><td>Disk 90% full or more</td><td><?= (int) $health['disk_high'] ?> of <?= $hl ?> (<?= e($pct($health['disk_high'], $hl)) ?>)</td></tr>
                <tr><td>Failed background jobs</td><td><?= (int) $health['failed_jobs'] ?> of <?= $hl ?> (<?= e($pct($health['failed_jobs'], $hl)) ?>)</td></tr>
            </tbody></table></div>
        <?php endif; ?></div>
    <div class="panel"><h2>Installs and upgrades, last <?= $days ?> days</h2>
        <?php if ($releases['installs'] + $releases['upgrades'] + $releases['rollbacks'] === 0): ?><p class="muted mb-0">N/A: no install or upgrade event reported.</p><?php else: ?>
            <div class="table-wrap"><table class="data"><tbody>
                <tr><td>Installs</td><td><?= (int) $releases['installs_ok'] ?> succeeded of <?= (int) $releases['installs'] ?> (<?= e($pct($releases['installs_ok'], $releases['installs'])) ?>)</td></tr>
                <tr><td>Upgrades</td><td><?= (int) $releases['upgrades_ok'] ?> succeeded of <?= (int) $releases['upgrades'] ?> (<?= e($pct($releases['upgrades_ok'], $releases['upgrades'])) ?>)</td></tr>
                <tr><td>Rollbacks</td><td><?= (int) $releases['rollbacks'] ?></td></tr>
            </tbody></table></div>
        <?php endif; ?></div>
</div>
<div class="panel"><h2>Reports received by the interface</h2>
    <?php if ($api === []): ?><p class="muted mb-0">N/A: no request in the period.</p><?php else: ?><?= $bars($api) ?><p class="muted">Endpoint and answer code per request, kept 30 days; no address is stored.</p><?php endif; ?></div>
