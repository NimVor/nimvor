<?php /** @var \NimVor\View\View $this */ ?>
<?php $this->start(); ?>Domains<?php $this->end('title'); ?>
<?php $this->start(); ?><?= count($rows) ?> hostname(s) served by this server<?php $this->end('subtitle'); ?>

<?= $this->partial('partials/discovery_bar', ['discoveredAt' => $discoveredAt]) ?>

<div class="card">
    <?php if ($rows === []): ?>
        <div class="card-body text-center py-5 text-body-secondary nim-empty"><i class="bi bi-signpost-split display-6"></i><p class="mt-3 mb-0">No domains were found.</p></div>
    <?php else: ?>
    <div class="table-responsive">
        <table class="table table-hover align-middle mb-0 nim-table">
            <thead><tr><th>Hostname</th><th>Type</th><th>Website</th><th>Status</th><th>HTTPS</th></tr></thead>
            <tbody>
            <?php foreach ($rows as $r): $s = $r['site']; ?>
                <tr>
                    <td class="fw-semibold"><?= e($r['name']) ?></td>
                    <td><?= $r['primary'] ? '<span class="badge text-bg-light border">primary</span>' : '<span class="badge text-bg-light border">alias</span>' ?></td>
                    <td><a href="<?= e($this->url('websites.show', ['id' => (string) $s['id']])) ?>"><?= e($s['domain']) ?></a></td>
                    <td><?= !empty($s['enabled']) ? $this->statusBadge('enabled') : $this->statusBadge('disabled') ?></td>
                    <td>
                        <?php if (empty($s['https'])): ?><span class="text-body-secondary">no</span>
                        <?php elseif ($r['covered']): ?><?= $this->statusBadge('valid', 'covered') ?>
                        <?php else: ?><?= $this->statusBadge('warning', 'not covered by certificate') ?><?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
    <?php endif; ?>
</div>
