<?php
/**
 * Server → Disk usage: filesystems, where the space goes, each website's
 * share and the largest files (removable in bulk).
 *
 * @var \NimVor\View\View $this
 * @var \NimVor\Agent\AgentException|null $agentError
 * @var list<array<string, mixed>> $filesystems
 * @var string $tab
 * @var array<string, mixed>|null $scan latest finished scan
 * @var array<string, mixed>|null $running
 * @var array<string, int> $siteIds website id by domain
 */
$canManage = $this->routeAllowed('server.disk.scan');
$result = (array) ($scan['result'] ?? []);
$areas = (array) ($result['areas'] ?? []);
$sites = (array) ($result['sites'] ?? []);
$large = (array) ($result['large'] ?? []);
$areaLabel = [];
foreach ($areas as $a) { $areaLabel[(string) $a['key']] = (string) $a['label']; }
$bar = static fn (float $p): string => $p >= 90 ? 'bg-danger' : ($p >= 75 ? 'bg-warning' : '');
$tabs = ['overview' => 'Overview', 'websites' => 'Websites (' . count($sites) . ')', 'files' => 'Large files (' . count($large) . ')'];
$maxArea = 0;
foreach ($areas as $a) { $maxArea = max($maxArea, (int) $a['bytes']); }
$siteTotal = 0;
foreach ($sites as $s) { $siteTotal += (int) $s['bytes']; }
?>
<?php $this->start(); ?>Disk usage<?php $this->end('title'); ?>
<?php $this->start(); ?>Where the disk space goes, per website, and the biggest files<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
    <?php if ($canManage): ?>
        <form method="post" action="<?= e($this->url('server.disk.scan')) ?>" class="d-flex gap-2 align-items-center" data-nim-busy>
            <?= $this->csrfField() ?>
            <label class="visually-hidden" for="min-mb">List files from</label>
            <select class="form-select form-select-sm w-auto" id="min-mb" name="min_mb">
                <?php foreach ([10 => '10 MB', 50 => '50 MB', 100 => '100 MB', 500 => '500 MB', 1000 => '1 GB'] as $mb => $l): ?>
                    <option value="<?= $mb ?>"<?= (int) ($scan['min_mb'] ?? 50) === $mb ? ' selected' : '' ?>>Files from <?= e($l) ?></option>
                <?php endforeach; ?>
            </select>
            <button class="btn btn-sm btn-primary"<?= $running !== null ? ' disabled' : '' ?>><i class="bi bi-search me-1" aria-hidden="true"></i><?= $running !== null ? 'Measuring…' : 'Scan the disk' ?></button>
        </form>
    <?php endif; ?>
<?php $this->end('actions'); ?>

<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>

<?php if ($running !== null): ?>
    <div class="alert alert-info small" role="status"><i class="bi bi-hourglass-split me-1" aria-hidden="true"></i>Measuring the disk since <?= e(substr((string) $running['started_at'], 11, 5)) ?> UTC. Reload this page in a minute or two.</div>
<?php endif; ?>

<ul class="nav nav-tabs mb-3">
    <?php foreach ($tabs as $k => $label): ?>
        <li class="nav-item"><a class="nav-link<?= $tab === $k ? ' active' : '' ?>" href="<?= e($this->url('server.disk')) ?>?tab=<?= e($k) ?>"><?= e($label) ?></a></li>
    <?php endforeach; ?>
</ul>

<?php if ($tab === 'overview'): ?>
    <div class="nim-panel mb-3">
        <div class="px-3 pt-3 fw-semibold"><i class="bi bi-hdd me-1" aria-hidden="true"></i>Filesystems</div>
        <div class="table-responsive">
            <table class="table align-middle mb-0">
                <thead><tr><th>Mounted on</th><th>Device</th><th>Type</th><th>Used / size</th><th style="min-width:180px">Space</th><th style="min-width:140px">Inodes (files)</th></tr></thead>
                <tbody>
                <?php foreach ($filesystems as $f): $p = (float) $f['used_percent']; $ip = (float) $f['inodes_percent']; ?>
                    <tr>
                        <td class="font-monospace"><?= e((string) $f['mount']) ?></td>
                        <td class="small"><?= e((string) $f['device']) ?></td>
                        <td class="small"><?= e((string) $f['type']) ?></td>
                        <td class="small"><?= e(format_bytes($f['used_bytes'])) ?> / <?= e(format_bytes($f['total_bytes'])) ?><div class="nim-muted"><?= e(format_bytes($f['avail_bytes'])) ?> free</div></td>
                        <td><div class="progress" role="progressbar" aria-valuenow="<?= e((string) $p) ?>" aria-valuemin="0" aria-valuemax="100"><div class="progress-bar <?= $bar($p) ?>" data-width="<?= e((string) $p) ?>"></div></div><div class="small nim-muted"><?= e((string) $p) ?>%</div></td>
                        <td><div class="progress" role="progressbar" aria-valuenow="<?= e((string) $ip) ?>" aria-valuemin="0" aria-valuemax="100"><div class="progress-bar <?= $bar($ip) ?>" data-width="<?= e((string) $ip) ?>"></div></div><div class="small nim-muted"><?= e((string) $ip) ?>% of <?= e(number_format((int) $f['inodes'])) ?></div></td>
                    </tr>
                <?php endforeach; ?>
                <?php if ($filesystems === []): ?><tr><td colspan="6" class="nim-muted p-3">No filesystems were reported.</td></tr><?php endif; ?>
                </tbody>
            </table>
        </div>
    </div>

    <div class="nim-panel">
        <div class="px-3 pt-3 fw-semibold"><i class="bi bi-pie-chart me-1" aria-hidden="true"></i>Where the space goes
            <?php if ($scan !== null): ?><span class="small nim-muted fw-normal ms-2">scanned <?= e(substr((string) ($result['scanned_at'] ?? $scan['finished_at']), 0, 16)) ?> UTC<?= !empty($result['partial']) ? ' · stopped at the time limit, sizes are lower bounds' : '' ?></span><?php endif; ?></div>
        <?php if ($scan === null): ?>
            <p class="px-3 pb-3 nim-muted mb-0">Not measured yet. <?= $canManage ? 'Press <strong>Scan the disk</strong> above.' : 'An administrator can scan the disk.' ?></p>
        <?php else: ?>
            <div class="table-responsive">
                <table class="table align-middle mb-0">
                    <thead><tr><th>Place</th><th>Path</th><th class="text-end">Size</th><th class="text-end">Files</th><th style="min-width:160px"></th></tr></thead>
                    <tbody>
                    <?php $sorted = $areas; usort($sorted, static fn (array $a, array $b): int => (int) $b['bytes'] <=> (int) $a['bytes']);
                    foreach ($sorted as $a): $w = $maxArea > 0 ? round((int) $a['bytes'] * 100 / $maxArea, 1) : 0; ?>
                        <tr>
                            <td class="fw-medium"><?= e((string) $a['label']) ?></td>
                            <td class="small font-monospace"><?= e((string) $a['path']) ?></td>
                            <td class="text-end"><?= !empty($a['missing']) ? '<span class="nim-muted">not on this server</span>' : e(format_bytes($a['bytes'])) . (!empty($a['partial']) ? '+' : '') ?></td>
                            <td class="text-end small"><?= !empty($a['missing']) ? '' : e(number_format((int) $a['files'])) ?></td>
                            <td><div class="progress" role="progressbar" aria-valuenow="<?= e((string) $w) ?>" aria-valuemin="0" aria-valuemax="100"><div class="progress-bar" data-width="<?= e((string) $w) ?>"></div></div></td>
                        </tr>
                    <?php endforeach; ?>
                    </tbody>
                </table>
            </div>
        <?php endif; ?>
    </div>
<?php elseif ($tab === 'websites'): ?>
    <div class="nim-panel">
        <?php if ($scan === null): ?>
            <p class="p-3 nim-muted mb-0">Not measured yet. Scan the disk first.</p>
        <?php else: ?>
            <div class="table-responsive">
                <table class="table align-middle mb-0">
                    <thead><tr><th>Website</th><th class="text-end">Size</th><th class="text-end">Files</th><th style="min-width:160px">Share of the websites</th><th></th></tr></thead>
                    <tbody>
                    <?php foreach ($sites as $s): $share = $siteTotal > 0 ? round((int) $s['bytes'] * 100 / $siteTotal, 1) : 0; ?>
                        <tr>
                            <td class="fw-medium"><?= e((string) $s['domain']) ?></td>
                            <td class="text-end"><?= e(format_bytes($s['bytes'])) ?></td>
                            <td class="text-end small"><?= e(number_format((int) $s['files'])) ?></td>
                            <td><div class="progress" role="progressbar" aria-valuenow="<?= e((string) $share) ?>" aria-valuemin="0" aria-valuemax="100"><div class="progress-bar" data-width="<?= e((string) $share) ?>"></div></div><div class="small nim-muted"><?= e((string) $share) ?>%</div></td>
                            <td class="text-end"><?php if (!empty($siteIds[(string) $s['domain']])): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.disk', ['id' => (int) $siteIds[(string) $s['domain']]])) ?>">Folders</a><?php endif; ?></td>
                        </tr>
                    <?php endforeach; ?>
                    <?php if ($sites === []): ?><tr><td colspan="5" class="nim-muted p-3">No website folders were found.</td></tr><?php endif; ?>
                    </tbody>
                </table>
            </div>
        <?php endif; ?>
    </div>
<?php else: ?>
    <div class="nim-panel">
        <?php if ($scan === null): ?>
            <p class="p-3 nim-muted mb-0">Not measured yet. Scan the disk first.</p>
        <?php else: ?>
            <form method="post" action="<?= e($this->url('server.disk.remove')) ?>" data-nim-confirm="Delete the selected files? This cannot be undone. Active log files are emptied instead of removed.">
                <?= $this->csrfField() ?>
                <div class="d-flex align-items-center gap-2 px-3 pt-3">
                    <span class="small nim-muted me-auto">The <?= count($large) ?> biggest files of <?= e(format_bytes((int) ($result['min_bytes'] ?? 0))) ?> or more. Mailboxes cannot be removed from here.</span>
                    <?php if ($canManage): ?><button class="btn btn-sm btn-danger"><i class="bi bi-trash me-1" aria-hidden="true"></i>Delete selected</button><?php endif; ?>
                </div>
                <div class="table-responsive">
                    <table class="table align-middle mb-0">
                        <thead><tr><th style="width:2rem"><input type="checkbox" class="form-check-input" data-nim-select-all="paths[]" aria-label="Select all"></th><th>File</th><th>Place</th><th class="text-end">Size</th><th>Modified</th></tr></thead>
                        <tbody>
                        <?php foreach ($large as $f): ?>
                            <tr>
                                <td><input type="checkbox" class="form-check-input" name="paths[]" value="<?= e((string) $f['path']) ?>"<?= empty($f['deletable']) || !$canManage ? ' disabled' : '' ?> aria-label="Select <?= e((string) $f['path']) ?>"></td>
                                <td class="small font-monospace text-break"><?= e((string) $f['path']) ?></td>
                                <td class="small"><?= e((string) ($areaLabel[(string) $f['area']] ?? $f['area'])) ?><?= !empty($f['site']) ? ' · ' . e((string) $f['site']) : '' ?></td>
                                <td class="text-end text-nowrap"><?= e(format_bytes($f['bytes'])) ?></td>
                                <td class="small text-nowrap"><?= e(substr((string) $f['modified'], 0, 10)) ?></td>
                            </tr>
                        <?php endforeach; ?>
                        <?php if ($large === []): ?><tr><td colspan="5" class="nim-muted p-3">No files of that size were found.</td></tr><?php endif; ?>
                        </tbody>
                    </table>
                </div>
            </form>
        <?php endif; ?>
    </div>
<?php endif; ?>
