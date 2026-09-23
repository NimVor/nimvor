<?php
/** @var \WhaCP\View\View $this */
$withSsl = array_values(array_filter($sites, static fn ($s) => !empty($s['ssl'])));
usort($withSsl, static fn ($a, $b) => (int) $a['ssl']['days_remaining'] <=> (int) $b['ssl']['days_remaining']);
$status = static function (array $ssl, int $warn): string {
    if (($ssl['status'] ?? '') === 'valid' && (int) $ssl['days_remaining'] <= $warn) {
        return 'expiring';
    }
    return (string) ($ssl['status'] ?? 'error');
};
?>
<?php $this->start(); ?>SSL certificates<?php $this->end('title'); ?>
<?php $this->start(); ?>Certificates used by websites and managed by certbot<?php $this->end('subtitle'); ?>

<?= $this->partial('partials/discovery_bar', ['discoveredAt' => $discoveredAt]) ?>

<div class="card mb-3">
    <div class="card-header"><i class="bi bi-globe2 me-1"></i>Website certificates</div>
    <?php if ($withSsl === []): ?>
        <div class="card-body text-body-secondary small">No website references a certificate.</div>
    <?php else: ?>
        <div class="table-responsive">
            <table class="table table-hover align-middle mb-0 wcp-table">
                <thead><tr><th>Website</th><th>Status</th><th>Expires</th><th>Issuer</th><th>Matches domain</th><th class="d-none d-xl-table-cell">File</th></tr></thead>
                <tbody>
                <?php foreach ($withSsl as $s): $ssl = $s['ssl']; ?>
                    <tr>
                        <td><a href="<?= e($this->url('websites.show', ['id' => (string) $s['id']])) ?>"><?= e($s['domain']) ?></a></td>
                        <td><?= $this->statusBadge($status($ssl, $warnDays)) ?></td>
                        <td><?= empty($ssl['error']) ? e(gmdate('Y-m-d', (int) strtotime((string) $ssl['not_after']))) . ' <span class="small text-body-secondary">(' . (int) $ssl['days_remaining'] . ' d)</span>' : '—' ?></td>
                        <td class="small"><?= e($ssl['issuer'] ?? '') ?></td>
                        <td><?= !empty($ssl['covers_domain']) ? $this->statusBadge('yes') : $this->statusBadge('error', 'no') ?></td>
                        <td class="d-none d-xl-table-cell small"><code><?= e($ssl['cert_file']) ?></code></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
</div>

<div class="card">
    <div class="card-header"><i class="bi bi-shield-lock me-1"></i>Let's Encrypt (certbot) lineages</div>
    <?php if ($certificates === []): ?>
        <div class="card-body text-body-secondary small">No certbot certificates were found.</div>
    <?php else: ?>
        <div class="table-responsive">
            <table class="table table-hover align-middle mb-0 wcp-table">
                <thead><tr><th>Name</th><th>Domains</th><th>Status</th><th>Expires</th><th>Renewal</th><th>Private key</th></tr></thead>
                <tbody>
                <?php foreach ($certificates as $c): $cert = $c['certificate']; ?>
                    <tr>
                        <td class="fw-semibold"><?= e($c['name']) ?></td>
                        <td class="small"><?= e(implode(', ', $cert['dns_names'] ?? [])) ?></td>
                        <td><?= $this->statusBadge($status(['status' => !empty($cert['error']) ? 'error' : (!empty($cert['expired']) ? 'expired' : 'valid'), 'days_remaining' => $cert['days_remaining']], $warnDays)) ?></td>
                        <td><?= empty($cert['error']) ? e(gmdate('Y-m-d', (int) strtotime((string) $cert['not_after']))) : '—' ?></td>
                        <td class="small"><?= e(($c['authenticator'] ?? '') !== '' ? $c['authenticator'] : 'no renewal config') ?></td>
                        <td>
                            <?php if (empty($cert['key_exists'])): ?><?= $this->statusBadge('warning', 'missing') ?>
                            <?php elseif (!empty($cert['key_world_readable'])): ?><?= $this->statusBadge('error', 'world-readable!') ?>
                            <?php else: ?><?= $this->statusBadge('ok', 'protected') ?><?php endif; ?>
                        </td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
        <div class="card-footer small text-body-secondary">Private keys are never read; only their presence and permissions are checked.</div>
    <?php endif; ?>
</div>
