<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $site */
/** @var string $path */
/** @var string $sort */
/** @var array<string, mixed>|null $scan */
/** @var array<string, mixed>|null $running */
$id = (int) $site['id'];
$entries = (array) ($scan['result']['entries'] ?? []);
$total = max(1, (int) ($scan['result']['bytes'] ?? 0));
usort($entries, static fn (array $a, array $b): int => match ($sort) {
    'name' => strcmp((string) $a['name'], (string) $b['name']),
    'files' => $b['files'] <=> $a['files'],
    default => $b['bytes'] <=> $a['bytes'],
});
$crumbs = [];
$acc = '';
foreach ($path === '' ? [] : explode('/', $path) as $part) {
    $acc = ltrim($acc . '/' . $part, '/');
    $crumbs[] = [$part, $acc];
}
$link = fn (array $q): string => $this->url('sites.disk', ['id' => $id], array_filter(['path' => $path, 'sort' => $sort === 'size' ? '' : $sort] + $q, static fn ($v): bool => $v !== ''));
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'disk']) ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>
<div class="nim-panel">
    <div class="nim-actionbar">
        <nav aria-label="Folder">
            <ol class="breadcrumb mb-0">
                <li class="breadcrumb-item"><a href="<?= e($this->url('sites.disk', ['id' => $id])) ?>"><?= e((string) $site['domain']) ?></a></li>
                <?php foreach ($crumbs as $i => [$name, $p]): ?>
                    <li class="breadcrumb-item<?= $i === count($crumbs) - 1 ? ' active' : '' ?>"<?= $i === count($crumbs) - 1 ? ' aria-current="page"' : '' ?>><?php if ($i === count($crumbs) - 1): ?><?= e($name) ?><?php else: ?><a href="<?= e($this->url('sites.disk', ['id' => $id], ['path' => $p])) ?>"><?= e($name) ?></a><?php endif; ?></li>
                <?php endforeach; ?>
            </ol>
        </nav>
        <div class="nim-actionbar-end">
            <?php if ($scan !== null): ?><span class="small text-body-secondary">Measured <?= e(time_ago((string) $scan['finished_at'])) ?></span><?php endif; ?>
            <?php if ($running !== null): ?>
                <a class="btn btn-sm btn-outline-secondary" href="#" data-nim-job-open="<?= e((string) $running['job_id']) ?>"><span class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>Measuring…</a>
            <?php else: ?>
                <form method="post" action="<?= e($this->url('sites.disk.scan', ['id' => $id])) ?>" data-nim-busy><?= $this->csrfField() ?><input type="hidden" name="path" value="<?= e($path) ?>"><button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><i class="bi bi-arrow-repeat me-1"></i><?= $scan === null ? 'Measure' : 'Measure again' ?></button></form>
            <?php endif; ?>
        </div>
    </div>
    <?php if ($scan === null): ?>
        <div class="nim-empty-state"><i class="bi bi-pie-chart" aria-hidden="true"></i><div class="fw-medium"><?= $running !== null ? 'Measuring this folder…' : 'Not measured yet' ?></div><div class="small">See which folders use the most space<?= $path === '' ? ', including the domain\'s mailboxes' : '' ?>. The measurement runs in the background.</div></div>
    <?php elseif ($scan['status'] === 'failed'): ?>
        <div class="alert alert-danger mt-3 mb-0">The measurement failed: <?= e((string) $scan['error']) ?></div>
    <?php else: ?>
        <div class="nim-dtable table-responsive">
            <table class="table align-middle">
                <thead><tr>
                    <?php foreach (['name' => 'Folder', 'size' => 'Size', 'files' => 'Files'] as $k => $l): ?>
                        <th class="<?= $k === 'name' ? '' : 'text-end' ?>"<?= $sort === $k ? ' aria-sort="' . ($k === 'name' ? 'ascending' : 'descending') . '"' : '' ?>><a class="link-body-emphasis text-decoration-none" href="<?= e($link(['sort' => $k === 'size' ? '' : $k])) ?>"><?= e($l) ?><?= $sort === $k ? ' <i class="bi bi-caret-' . ($k === 'name' ? 'up' : 'down') . '-fill" aria-hidden="true"></i>' : '' ?></a></th>
                    <?php endforeach; ?>
                    <th class="nim-w-30"><span class="visually-hidden">Share</span></th>
                </tr></thead>
                <tbody>
                <?php foreach ($entries as $e): $share = (int) round(100 * (int) $e['bytes'] / $total); ?>
                    <tr>
                        <td>
                            <?php if ($e['kind'] === 'dir'): ?><a class="text-decoration-none" href="<?= e($this->url('sites.disk', ['id' => $id], ['path' => ltrim($path . '/' . $e['name'], '/')])) ?>"><i class="bi bi-folder2 me-1" aria-hidden="true"></i><?= e((string) $e['name']) ?></a>
                            <?php elseif ($e['kind'] === 'mail'): ?><i class="bi bi-envelope me-1" aria-hidden="true"></i>Email (mailboxes)
                            <?php else: ?><i class="bi bi-files me-1" aria-hidden="true"></i><span class="text-body-secondary">Files in this folder</span><?php endif; ?>
                        </td>
                        <td class="text-end text-nowrap fw-medium"><?= e(format_bytes((int) $e['bytes'])) ?></td>
                        <td class="text-end text-nowrap"><?= number_format((int) $e['files']) ?></td>
                        <td><progress class="nim-du-bar" value="<?= (int) $e['bytes'] ?>" max="<?= $total ?>" aria-label="<?= $share ?>% of the total"><?= $share ?>%</progress> <span class="small text-body-secondary"><?= $share ?>%</span></td>
                    </tr>
                <?php endforeach; ?>
                <?php if ($entries === []): ?><tr><td colspan="4" class="text-center text-body-secondary py-4">This folder is empty.</td></tr><?php endif; ?>
                </tbody>
                <tfoot><tr class="fw-semibold"><td>Total</td><td class="text-end text-nowrap"><?= e(format_bytes((int) ($scan['result']['bytes'] ?? 0))) ?><?= !empty($scan['result']['partial']) ? '+' : '' ?></td><td></td><td></td></tr></tfoot>
            </table>
        </div>
        <?php if (!empty($scan['result']['partial'])): ?><p class="small text-warning-emphasis mb-0">The measurement stopped at its time limit; the sizes are at least this large.</p><?php endif; ?>
        <?php if ((int) ($scan['result']['truncated'] ?? 0) > 0): ?><p class="small text-body-secondary mb-0"><?= (int) $scan['result']['truncated'] ?> smaller folder(s) are not listed; they are counted in the total.</p><?php endif; ?>
    <?php endif; ?>
</div>
