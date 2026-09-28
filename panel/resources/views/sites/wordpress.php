<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $site
 * @var list<array<string, mixed>> $installs
 * @var bool $canManage
 */
$id = (int) $site['id'];
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'wordpress']) ?>

<div class="wcp-panel" data-wcp-dt>
    <div class="wcp-actionbar">
        <div class="fw-semibold">WordPress installations <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($installs) ?></span></div>
        <?php if ($canManage): ?>
            <div class="wcp-actionbar-end">
                <form method="post" action="<?= e($this->url('sites.wordpress.scan', ['id' => $id])) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <button class="btn btn-sm btn-outline-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><i class="bi bi-search me-1" aria-hidden="true"></i>Scan for WordPress</button>
                </form>
            </div>
        <?php endif; ?>
    </div>
    <?php if ($installs === []): ?>
        <div class="wcp-empty-state">
            <i class="bi bi-wordpress" aria-hidden="true"></i>
            <div class="fw-medium">No WordPress found yet</div>
            <div class="small">Scan the website to find WordPress in its folders<?php if ($this->hasRoute('sites.apps') && $site['kind'] === 'php'): ?>, or <a href="<?= e($this->url('sites.apps', ['id' => $id])) ?>#app-wordpress">install WordPress</a><?php endif; ?>.</div>
        </div>
    <?php else: ?>
        <div class="wcp-dtable table-responsive">
            <table class="table align-middle">
                <thead><tr><th>Folder</th><th>Version</th><th>Updates</th><th>Checks</th><th></th></tr></thead>
                <tbody>
                <?php foreach ($installs as $i): ?>
                    <tr>
                        <td><code><?= e($i['path'] === '' ? '/' : $i['path']) ?></code><?php if ($i['url'] !== null): ?><div class="small"><a href="<?= e($i['url']) ?>/" target="_blank" rel="noopener noreferrer"><?= e($i['url']) ?></a></div><?php endif; ?></td>
                        <td><?= e($i['version'] ?: '?') ?><?php if ($i['outdated']): ?> <span class="wcp-state is-warn"><?= e($i['latest']) ?> available</span><?php endif; ?></td>
                        <td class="small"><?= (int) $i['plugin_updates'] ?> plugin(s), <?= (int) $i['theme_updates'] ?> theme(s)<?= $i['checked_at'] === null ? ' <span class="text-body-secondary">(not checked yet)</span>' : '' ?></td>
                        <td><?= (int) $i['issues'] > 0 ? '<span class="wcp-state is-warn">' . (int) $i['issues'] . ' to review</span>' : ($i['checked_at'] === null ? '<span class="text-body-secondary small">—</span>' : '<span class="wcp-state is-ok">OK</span>') ?></td>
                        <td class="text-end"><a class="btn btn-sm btn-primary" href="<?= e($this->url('sites.wordpress.show', ['id' => $id, 'wp' => (int) $i['id']])) ?>">Manage</a></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
</div>
