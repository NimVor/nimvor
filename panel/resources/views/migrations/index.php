<?php
/**
 * @var \NimVor\View\View $this
 * @var list<array<string, mixed>> $migrations
 */
$label = ['draft' => 'Connecting', 'connected' => 'Connected', 'discovering' => 'Discovering…', 'discovered' => 'Discovered', 'selected' => 'Planned', 'assessed' => 'Checked',
    'running' => 'Running…', 'paused' => 'Paused', 'failed' => 'Failed', 'interrupted' => 'Interrupted', 'completed' => 'Completed', 'cancelled' => 'Cancelled', 'rolled_back' => 'Rolled back'];
$state = ['completed' => 'is-ok', 'running' => 'is-warn', 'discovering' => 'is-warn', 'failed' => 'is-error', 'interrupted' => 'is-error', 'cancelled' => '', 'paused' => 'is-warn'];
?>
<?php $this->start(); ?>Migrations<?php $this->end('title'); ?>
<?php $this->start(); ?>Move websites and databases from another server<?php $this->end('subtitle'); ?>

<div class="nim-actionbar mb-3 px-0">
    <div class="small text-body-secondary">Copy websites and databases from another server into NimVor, step by step. It reads the source over SSH and never changes or deletes anything on it.</div>
    <div class="nim-actionbar-end"><a class="btn btn-sm btn-primary" href="<?= e($this->url('migrations.new')) ?>"><i class="bi bi-plus-lg me-1"></i>New migration</a></div>
</div>
<div class="nim-panel">
    <div class="nim-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>#</th><th>Source</th><th>Panel</th><th>Status</th><th>Started</th><th class="text-end">Operate</th></tr></thead>
            <tbody>
            <?php foreach ($migrations as $m): ?>
                <tr>
                    <td><?= (int) $m['id'] ?></td>
                    <td class="fw-semibold"><?= e($m['source_host'] !== '' ? $m['source_user'] . '@' . $m['source_host'] . ':' . $m['source_port'] : '—') ?></td>
                    <td class="small"><?= e((string) $m['panel']) ?></td>
                    <td class="small"><span class="nim-state <?= e($state[$m['status']] ?? '') ?>"><?= e($label[$m['status']] ?? (string) $m['status']) ?></span><?php if ($m['error'] !== ''): ?><div class="text-danger-emphasis text-break"><?= e((string) $m['error']) ?></div><?php endif; ?></td>
                    <td class="small text-nowrap"><?= e(str_replace('T', ' ', substr((string) $m['created_at'], 0, 16))) ?></td>
                    <td class="text-end text-nowrap">
                        <a class="btn btn-sm btn-outline-primary" href="<?= e($this->url('migrations.show', ['id' => (int) $m['id']])) ?>"><?= in_array($m['status'], ['completed', 'cancelled', 'rolled_back'], true) ? 'Open' : 'Continue' ?></a>
                        <?php if ($m['status'] !== 'running'): ?><form class="d-inline" method="post" action="<?= e($this->url('migrations.delete', ['id' => (int) $m['id']])) ?>" data-nim-confirm="Remove the record of migration <?= (int) $m['id'] ?>? Files and databases already copied stay where they are."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" aria-label="Remove migration <?= (int) $m['id'] ?>"><i class="bi bi-trash"></i></button></form><?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            <?php if ($migrations === []): ?><tr><td colspan="6" class="text-center py-4 nim-muted">No migration yet. Choose “New migration” to move a website from another server.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
</div>
