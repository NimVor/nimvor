<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var array<string, mixed>|null $report
 * @var int $hours
 * @var array<int, string> $windows
 */
$id = (int) $site['id'];
$n = static fn (int $v): string => number_format($v);
/** A horizontal bar (inline SVG: styles are not allowed inline). */
$bar = static function (int $value, int $max, string $class = 'nim-bar-req'): string {
    $w = $max > 0 ? max(1, (int) round($value / $max * 100)) : 0;
    return '<svg class="nim-rank" viewBox="0 0 100 6" preserveAspectRatio="none" role="img" aria-hidden="true"><rect class="nim-track" width="100" height="6" rx="2"/><rect class="' . $class . '" width="' . $w . '" height="6" rx="2"/></svg>';
};
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'report']) ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php return; endif; ?>

<div class="d-flex flex-wrap align-items-center gap-2 mb-3">
    <div class="btn-group btn-group-sm" role="group" aria-label="Period">
        <?php foreach ($windows as $h => $label): ?><a class="btn btn-outline-secondary<?= $hours === $h ? ' active' : '' ?>" href="<?= e($this->url('sites.report', ['id' => $id], ['hours' => $h])) ?>"><?= e($label) ?></a><?php endforeach; ?>
    </div>
    <span class="small text-body-secondary">From the website's access log, without query strings.</span>
</div>
<?php if (!$report['exists']): ?>
    <div class="nim-empty-state"><i class="bi bi-bar-chart" aria-hidden="true"></i><div class="fw-medium">No access log yet</div><div class="small">The report appears once the website has had visitors.</div></div>
    <?php return; ?>
<?php endif; ?>
<?php if ($report['truncated']): ?><div class="nim-infobar mb-3"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>The access log is large: only its newest part was read, so the earliest hours of this period may be incomplete.</span></div><?php endif; ?>

<div class="row g-3 mb-3">
    <?php foreach ([['Requests', $report['requests'], 'bi-graph-up'], ['Refused or limited', $report['refused'], 'bi-slash-circle'], ['Traffic limit (429)', $report['limited'], 'bi-speedometer2'], ['Closed (444)', $report['closed'], 'bi-x-octagon'], ['Forbidden (403)', $report['forbidden'], 'bi-shield-exclamation'], ['ModSecurity events', $report['modsec']['events'], 'bi-shield-check']] as [$label, $value, $icon]): ?>
        <div class="col-6 col-lg-2">
            <div class="card h-100"><div class="card-body py-3">
                <div class="small text-body-secondary"><i class="bi <?= e($icon) ?>" aria-hidden="true"></i> <?= e($label) ?></div>
                <div class="fs-4 fw-semibold"><?= $n((int) $value) ?></div>
            </div></div>
        </div>
    <?php endforeach; ?>
</div>

<div class="card mb-3">
    <div class="card-header">Requests per hour <span class="small text-body-secondary fw-normal">(red: refused or limited)</span></div>
    <div class="card-body">
        <?php
        $tl = $report['timeline'];
        $max = 1;
        foreach ($tl as $b) { $max = max($max, $b['requests']); }
        $count = max(1, count($tl));
        $bw = 600 / $count;
        ?>
        <svg class="nim-chart" viewBox="0 0 600 110" role="img" aria-label="Requests per hour">
            <?php foreach ($tl as $i => $b): $h = (int) round($b['requests'] / $max * 90); $rh = (int) round($b['refused'] / $max * 90); ?>
                <g>
                    <title><?= e(gmdate('D H:i', $b['start'])) ?> UTC: <?= (int) $b['requests'] ?> requests, <?= (int) $b['refused'] ?> refused</title>
                    <rect class="nim-bar-req" x="<?= round($i * $bw + 0.5, 2) ?>" y="<?= 95 - $h ?>" width="<?= round(max(1, $bw - 1), 2) ?>" height="<?= $h ?>"/>
                    <?php if ($rh > 0): ?><rect class="nim-bar-ref" x="<?= round($i * $bw + 0.5, 2) ?>" y="<?= 95 - $rh ?>" width="<?= round(max(1, $bw - 1), 2) ?>" height="<?= $rh ?>"/><?php endif; ?>
                </g>
            <?php endforeach; ?>
            <text class="nim-axis" x="0" y="108"><?= e(gmdate('M j H:i', $tl[0]['start'] ?? time())) ?> UTC</text>
            <text class="nim-axis" x="600" y="108" text-anchor="end"><?= e(gmdate('M j H:i', $tl[count($tl) - 1]['start'] ?? time())) ?> UTC</text>
            <text class="nim-axis" x="0" y="9">peak <?= $n($max) ?>/h</text>
        </svg>
    </div>
</div>

<div class="row g-3 mb-3">
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header">Most refused addresses</div>
            <?php if ($report['refused_ips'] === []): ?>
                <div class="card-body small text-body-secondary">No refused requests in this period.</div>
            <?php else: $m = max(array_column($report['refused_ips'], 'refused')); ?>
                <div class="nim-dtable table-responsive"><table class="table align-middle mb-0">
                    <thead><tr><th>Address</th><th>Country</th><th class="text-end">Refused</th><th></th><th></th></tr></thead>
                    <tbody>
                    <?php foreach ($report['refused_ips'] as $r): ?>
                        <tr>
                            <td class="font-monospace small"><?= e($r['key']) ?></td>
                            <td class="small"><?= e($r['country'] !== '' ? \NimVor\Security\Countries::name($r['country']) : '—') ?></td>
                            <td class="text-end"><?= $n($r['refused']) ?> <span class="small text-body-secondary">of <?= $n($r['total']) ?></span></td>
                            <td><?= $bar($r['refused'], $m, 'nim-bar-ref') ?></td>
                            <td class="text-end">
                                <form class="d-inline" method="post" action="<?= e($this->url('sites.accesslists.add', ['id' => $id])) ?>" data-nim-busy data-nim-confirm="Deny <?= e($r['key']) ?> on <?= e($site['domain']) ?>?">
                                    <?= $this->csrfField() ?><input type="hidden" name="kind" value="ip"><input type="hidden" name="action" value="deny"><input type="hidden" name="value" value="<?= e($r['key']) ?>"><input type="hidden" name="note" value="from the traffic report">
                                    <button class="btn btn-sm btn-outline-danger">Deny</button>
                                </form>
                            </td>
                        </tr>
                    <?php endforeach; ?>
                    </tbody>
                </table></div>
            <?php endif; ?>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header">Busiest addresses</div>
            <?php if ($report['ips'] === []): ?>
                <div class="card-body small text-body-secondary">No requests in this period.</div>
            <?php else: $m = max(array_column($report['ips'], 'total')); ?>
                <div class="nim-dtable table-responsive"><table class="table align-middle mb-0">
                    <thead><tr><th>Address</th><th>Country</th><th class="text-end">Requests</th><th></th></tr></thead>
                    <tbody>
                    <?php foreach ($report['ips'] as $r): ?>
                        <tr>
                            <td class="font-monospace small"><?= e($r['key']) ?></td>
                            <td class="small"><?= e(($r['country'] ?? '') !== '' ? \NimVor\Security\Countries::name($r['country']) : '—') ?></td>
                            <td class="text-end"><?= $n($r['total']) ?></td>
                            <td><?= $bar($r['total'], $m) ?></td>
                        </tr>
                    <?php endforeach; ?>
                    </tbody>
                </table></div>
            <?php endif; ?>
        </div>
    </div>
</div>

<div class="row g-3 mb-3">
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header">Most refused URLs</div>
            <?php if ($report['refused_urls'] === []): ?>
                <div class="card-body small text-body-secondary">No refused requests in this period.</div>
            <?php else: $m = max(array_column($report['refused_urls'], 'refused')); ?>
                <div class="nim-dtable table-responsive"><table class="table align-middle mb-0">
                    <thead><tr><th>URL</th><th class="text-end">Refused</th><th></th></tr></thead>
                    <tbody>
                    <?php foreach ($report['refused_urls'] as $r): ?>
                        <tr><td class="font-monospace small text-break"><?= e($r['key']) ?></td><td class="text-end"><?= $n($r['refused']) ?> <span class="small text-body-secondary">of <?= $n($r['total']) ?></span></td><td><?= $bar($r['refused'], $m, 'nim-bar-ref') ?></td></tr>
                    <?php endforeach; ?>
                    </tbody>
                </table></div>
            <?php endif; ?>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header">Most requested URLs</div>
            <?php if ($report['urls'] === []): ?>
                <div class="card-body small text-body-secondary">No requests in this period.</div>
            <?php else: $m = max(array_column($report['urls'], 'total')); ?>
                <div class="nim-dtable table-responsive"><table class="table align-middle mb-0">
                    <thead><tr><th>URL</th><th class="text-end">Requests</th><th></th></tr></thead>
                    <tbody>
                    <?php foreach ($report['urls'] as $r): ?>
                        <tr><td class="font-monospace small text-break"><?= e($r['key']) ?></td><td class="text-end"><?= $n($r['total']) ?></td><td><?= $bar($r['total'], $m) ?></td></tr>
                    <?php endforeach; ?>
                    </tbody>
                </table></div>
            <?php endif; ?>
        </div>
    </div>
</div>

<div class="row g-3 mb-3">
    <div class="col-lg-7">
        <div class="card h-100">
            <div class="card-header">Countries <span class="small text-body-secondary fw-normal">(by address, DB-IP)</span></div>
            <?php if ($report['countries'] === []): ?>
                <div class="card-body small text-body-secondary">Countries need the country database (an administrator loads it under Security → Brute Force Protection → Countries Management).</div>
            <?php else: $m = max(array_column($report['countries'], 'total')); ?>
                <div class="nim-dtable table-responsive"><table class="table align-middle mb-0">
                    <thead><tr><th>Country</th><th class="text-end">Requests</th><th class="text-end">Refused</th><th></th></tr></thead>
                    <tbody>
                    <?php foreach ($report['countries'] as $r): ?>
                        <tr><td><?= e($r['name']) ?> <span class="font-monospace small text-body-secondary"><?= e($r['code']) ?></span></td><td class="text-end"><?= $n($r['total']) ?></td><td class="text-end"><?= $n($r['refused']) ?></td><td><?= $bar($r['total'], $m) ?></td></tr>
                    <?php endforeach; ?>
                    </tbody>
                </table></div>
                <div class="card-footer small"><a href="<?= e($this->url('sites.countries', ['id' => $id])) ?>">Country restrictions</a> can refuse or allow whole countries.</div>
            <?php endif; ?>
        </div>
    </div>
    <div class="col-lg-5">
        <div class="card h-100">
            <div class="card-header">Browsers most refused</div>
            <?php if ($report['agents'] === []): ?>
                <div class="card-body small text-body-secondary">None.</div>
            <?php else: ?>
                <ul class="list-group list-group-flush small">
                    <?php foreach ($report['agents'] as $r): ?><li class="list-group-item d-flex justify-content-between gap-2"><span class="text-break"><?= e($r['key'] === '' ? '(no browser)' : $r['key']) ?></span><span class="text-nowrap"><?= $n($r['refused']) ?></span></li><?php endforeach; ?>
                </ul>
            <?php endif; ?>
        </div>
    </div>
</div>

<div class="card mb-3">
    <div class="card-header">ModSecurity <span class="small text-body-secondary fw-normal">(its latest <?= (int) $report['modsec']['events'] ?> events for this website)</span></div>
    <?php if (!$report['modsec']['available']): ?>
        <div class="card-body small text-body-secondary">ModSecurity events could not be read.</div>
    <?php elseif ($report['modsec']['events'] === 0): ?>
        <div class="card-body small text-body-secondary">ModSecurity has caught nothing for this website. <a href="<?= e($this->url('sites.firewall', ['id' => $id])) ?>">Firewall settings</a></div>
    <?php else: ?>
        <div class="card-body">
            <div class="row g-3 small">
                <?php foreach ([['Addresses', $report['modsec']['ips']], ['Rules', $report['modsec']['rules']], ['URLs', $report['modsec']['urls']]] as [$title, $rows]): ?>
                    <div class="col-lg-4"><div class="fw-semibold mb-1"><?= e($title) ?></div>
                        <ul class="list-unstyled mb-0">
                            <?php foreach ($rows as $r): ?><li class="d-flex justify-content-between gap-2"><span class="text-break font-monospace"><?= e($r['key']) ?></span><span><?= $n($r['total']) ?></span></li><?php endforeach; ?>
                        </ul>
                    </div>
                <?php endforeach; ?>
            </div>
        </div>
    <?php endif; ?>
</div>
<ul class="small text-body-secondary">
    <li>"Refused or limited" counts the answers 403, 429 and 444. A 403 can also be a file the web server may not read, so check a surprising number against the URL list.</li>
    <li>Addresses are those Nginx sees: behind a CDN or proxy every visitor has the proxy's address unless the real address is restored first.</li>
    <li>Query strings are never shown (they can carry tokens). The report reads the website's own access log only.</li>
</ul>
