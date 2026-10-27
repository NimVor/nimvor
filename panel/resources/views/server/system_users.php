<?php /** @var \WhaCP\View\View $this */ ?>
<?php $this->start(); ?>System accounts<?php $this->end('title'); ?>
<?php $this->start(); ?>Linux accounts from /etc/passwd<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
<a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('system_users', [], $showSystem ? [] : ['all' => '1'])) ?>">
    <?= $showSystem ? 'Hide system accounts' : 'Show system accounts' ?>
</a>
<?php $this->end('actions'); ?>

<?= $this->partial('partials/discovery_bar', ['discoveredAt' => $discoveredAt]) ?>

<div class="card">
    <div class="table-responsive">
        <table class="table table-hover align-middle mb-0 wcp-table">
            <thead><tr><th>User</th><th>UID</th><th>GID</th><th>Home</th><th>Shell</th><th>Login</th></tr></thead>
            <tbody>
            <?php foreach ($users as $u): ?>
                <tr>
                    <td class="fw-semibold"><?= e($u['name']) ?><?php if ($u['comment'] !== ''): ?><div class="small text-body-secondary fw-normal"><?= e($u['comment']) ?></div><?php endif; ?></td>
                    <td><?= (int) $u['uid'] ?></td>
                    <td><?= (int) $u['gid'] ?></td>
                    <td><code class="small"><?= e($u['home']) ?></code></td>
                    <td><code class="small"><?= e($u['shell']) ?></code></td>
                    <td><?= !empty($u['can_login']) ? $this->statusBadge('warning', 'shell') : $this->statusBadge('unknown', 'no shell') ?></td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
    <div class="card-footer small text-body-secondary">Password hashes (/etc/shadow) are never read.</div>
</div>
