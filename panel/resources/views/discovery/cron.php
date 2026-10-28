<?php /** @var \NimVor\View\View $this */ ?>
<?php $this->start(); ?>Cron jobs<?php $this->end('title'); ?>
<?php $this->start(); ?>Scheduled tasks found on the server<?php $this->end('subtitle'); ?>

<?= $this->partial('partials/discovery_bar', ['discoveredAt' => $discoveredAt]) ?>

<?php foreach ($warnings as $w): ?>
    <div class="alert alert-warning py-2 small"><?= e($w) ?></div>
<?php endforeach; ?>

<div class="card">
    <?php if ($jobs === []): ?>
        <div class="card-body text-center py-5 text-body-secondary nim-empty"><i class="bi bi-clock-history display-6"></i><p class="mt-3 mb-0">No cron jobs were found.</p></div>
    <?php else: ?>
        <div class="table-responsive">
            <table class="table table-hover align-middle mb-0 nim-table">
                <thead><tr><th>Schedule</th><th>User</th><th>Command</th><th class="d-none d-lg-table-cell">Source</th></tr></thead>
                <tbody>
                <?php foreach ($jobs as $j): ?>
                    <tr>
                        <td class="text-nowrap"><code><?= e($j['schedule']) ?></code></td>
                        <td><?= e($j['user']) ?></td>
                        <td><code class="small nim-break"><?= e($j['command']) ?></code></td>
                        <td class="d-none d-lg-table-cell small text-body-secondary"><?= e($j['source']) ?>:<?= (int) $j['line'] ?></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
        <div class="card-footer small text-body-secondary"><i class="bi bi-eye-slash me-1"></i>Passwords, tokens and keys in commands are redacted by the agent before they reach the panel.</div>
    <?php endif; ?>
</div>
