<?php
/**
 * @var \NimVor\View\View $this
 * @var list<array<string, mixed>> $installs
 * @var list<array<string, mixed>>|null $all
 * @var string $filter
 * @var bool $canManage
 */
$every = $all ?? $installs;
$outdated = count(array_filter($every, static fn (array $i): bool => $i['outdated'] || (int) $i['plugin_updates'] + (int) $i['theme_updates'] > 0));
$issues = count(array_filter($every, static fn (array $i): bool => (int) $i['issues'] > 0));
$tabs = ['' => ['All', count($every)], 'outdated' => ['Updates available', $outdated], 'issues' => ['To review', $issues]];
?>
<?php $this->start(); ?>WordPress<?php $this->end('title'); ?>
<?php $this->start(); ?>Installations in the websites you manage: versions, updates and security checks<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
<?php if ($canManage): ?>
    <form method="post" action="<?= e($this->url('wordpress.scan')) ?>" data-nim-busy data-nim-confirm="Scan every website folder for WordPress? It reads files only and may take a minute.">
        <?= $this->csrfField() ?>
        <button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><i class="bi bi-search me-1" aria-hidden="true"></i>Scan all websites</button>
    </form>
<?php endif; ?>
<?php $this->end('actions'); ?>

<ul class="nav nav-pills small mb-3">
    <?php foreach ($tabs as $k => [$label, $n]): ?>
        <li class="nav-item"><a class="nav-link<?= $filter === $k ? ' active' : '' ?>" href="<?= e($this->url('wordpress')) ?><?= $k === '' ? '' : '?filter=' . e($k) ?>"><?= e($label) ?> <span class="badge rounded-pill text-bg-light border fw-normal"><?= (int) $n ?></span></a></li>
    <?php endforeach; ?>
</ul>

<div class="nim-panel" data-nim-dt>
    <div class="nim-actionbar">
        <div class="fw-semibold">Installations</div>
        <div class="nim-actionbar-end"><div class="nim-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search installations" data-nim-dt-search><i class="bi bi-search" aria-hidden="true"></i></div></div>
    </div>
    <?php if ($installs === []): ?>
        <div class="nim-empty-state">
            <i class="bi bi-wordpress" aria-hidden="true"></i>
            <div class="fw-medium"><?= $every === [] ? 'No WordPress found yet' : 'Nothing in this list' ?></div>
            <div class="small"><?= $every === [] ? 'Scan the websites to find their WordPress installations.' : 'Every installation is up to date here.' ?></div>
        </div>
    <?php else: ?>
    <div class="nim-dtable table-responsive"><table class="table align-middle">
        <thead><tr><th>Website</th><th>Folder</th><th>Version</th><th>PHP</th><th>HTTPS</th><th>Updates</th><th>Checks</th><th></th></tr></thead>
        <tbody>
        <?php foreach ($installs as $i): ?>
            <tr>
                <td><div class="fw-medium"><?= e($i['domain']) ?></div><?php if ($i['owner_name'] !== null): ?><div class="small text-body-secondary"><?= e($i['owner_name']) ?></div><?php endif; ?></td>
                <td><code><?= e($i['path'] === '' ? '/' : $i['path']) ?></code></td>
                <td><?= e($i['version'] ?: '?') ?><?php if ($i['outdated']): ?><div><span class="nim-state is-warn"><?= e($i['latest']) ?> available</span></div><?php endif; ?></td>
                <td class="small"><?= e($i['php_version']) ?></td>
                <td><?= $i['https'] ? '<i class="bi bi-lock-fill text-success" title="HTTPS"></i>' : '<i class="bi bi-unlock text-warning" title="No HTTPS"></i>' ?></td>
                <td class="small"><?= $i['checked_at'] === null ? '<span class="text-body-secondary">not checked</span>' : (int) $i['plugin_updates'] . ' plugin(s), ' . (int) $i['theme_updates'] . ' theme(s)' ?></td>
                <td><?= (int) $i['issues'] > 0 ? '<span class="nim-state is-warn">' . (int) $i['issues'] . '</span>' : ($i['checked_at'] === null ? '—' : '<span class="nim-state is-ok">OK</span>') ?></td>
                <td class="text-end"><a class="btn btn-sm btn-outline-primary" href="<?= e($this->url('sites.wordpress.show', ['id' => (int) $i['website_id'], 'wp' => (int) $i['id']])) ?>">Manage</a></td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table></div>
    <?php endif; ?>
</div>
<p class="small text-body-secondary mt-2">Update counts and checks come from each installation's last overview (open it, or use "Check for updates" there). The scan only reads files; WordPress is not run for it.</p>
