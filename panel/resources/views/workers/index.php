<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $workers */
?>
<?php $this->start(); ?>Workers<?php $this->end('title'); ?>
<?php $this->start(); ?>Long-running processes of all websites<?php $this->end('subtitle'); ?>
<div class="card">
    <?php if ($workers === []): ?>
        <div class="card-body text-body-secondary">No workers yet. Open a website and use its <strong>Workers</strong> tab to add one.</div>
    <?php else: ?>
        <div class="table-responsive"><table class="table table-sm align-middle mb-0">
            <thead><tr><th>Worker</th><th>Website</th><th>Command</th><th>State</th></tr></thead>
            <tbody>
            <?php foreach ($workers as $w): $st = $status[$w['unit']] ?? null; ?>
                <tr>
                    <td><?= e($w['name']) ?><div class="small text-body-secondary"><code><?= e($w['unit']) ?></code></div></td>
                    <td><a href="<?= e($this->url('sites.workers', ['id' => (int) $w['website_id']])) ?>"><?= e($w['domain']) ?></a></td>
                    <td><code class="small text-break"><?= e($w['command']) ?></code></td>
                    <td><?= $st === null ? '<span class="small text-body-secondary">unknown</span>' : $this->statusBadge((string) $st['active_state']) ?></td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table></div>
    <?php endif; ?>
</div>
