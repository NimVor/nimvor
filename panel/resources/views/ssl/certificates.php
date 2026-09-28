<?php
/**
 * @var \WhaCP\View\View $this
 * @var list<array<string, mixed>> $certificates
 * @var int $warnDays
 */
use WhaCP\Security\Permissions as P;

$canManage = $this->can(P::SSL_MANAGE);
$sources = ['letsencrypt' => "Let's Encrypt", 'whacp' => 'Installed in WhaCP', 'other' => 'Elsewhere (read only)'];
?>
<?= $this->partial('ssl/_tabs', get_defined_vars()) ?>

<div class="wcp-panel" data-wcp-dt>
    <div class="wcp-actionbar">
        <div class="fw-semibold"><?= count($certificates) ?> certificates <span class="small wcp-muted fw-normal">· private keys are never shown</span></div>
        <div class="wcp-actionbar-end">
            <select class="form-select form-select-sm w-auto" aria-label="Source" data-wcp-dt-select="source"><option value="">All sources</option><?php foreach ($sources as $k => $l): ?><option value="<?= e($k) ?>"><?= e($l) ?></option><?php endforeach; ?></select>
            <div class="wcp-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search certificates" data-wcp-dt-search><i class="bi bi-search" aria-hidden="true"></i></div>
        </div>
    </div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Certificate</th><th>Names</th><th>State</th><th>Used by</th><th class="text-end">Operate</th></tr></thead>
            <tbody>
            <?php foreach ($certificates as $s): $c = (array) $s['certificate']; ?>
                <tr data-wcp-row data-dt-source="<?= e((string) $s['source']) ?>">
                    <td><div class="fw-semibold font-monospace text-break"><?= e((string) $s['name']) ?></div><div class="small wcp-muted"><?= e($sources[$s['source']] ?? '') ?><?= !empty($c['chain_length']) ? ' · chain of ' . (int) $c['chain_length'] : '' ?><?= !empty($c['key_world_readable']) ? ' · <span class="text-danger">key readable by everyone</span>' : '' ?></div></td>
                    <td class="small font-monospace text-break"><?= e(implode(', ', (array) ($c['dns_names'] ?? [])) ?: (string) ($c['common_name'] ?? '')) ?></td>
                    <td><?= $this->partial('ssl/_cert', ['c' => $c, 'warnDays' => $warnDays]) ?>
                        <details class="small mt-1"><summary class="wcp-muted">Details</summary>
                            <div class="font-monospace text-break">Subject: <?= e((string) ($c['subject'] ?? '')) ?><br>Issuer: <?= e((string) ($c['issuer'] ?? '')) ?><br>Valid: <?= e(substr((string) ($c['not_before'] ?? ''), 0, 10)) ?> – <?= e(substr((string) ($c['not_after'] ?? ''), 0, 10)) ?><br>SHA-256: <?= e((string) ($c['fingerprint_sha256'] ?? '')) ?><br>File: <?= e((string) ($c['file'] ?? '')) ?></div>
                        </details></td>
                    <td class="small font-monospace text-break"><?= $s['used_by'] === [] ? '<span class="wcp-muted">unused</span>' : e(implode(', ', (array) $s['used_by'])) ?></td>
                    <td class="text-end text-nowrap"><?php if ($canManage && !empty($s['deletable'])): ?>
                        <form class="d-inline" method="post" action="<?= e($this->url('ssl.certificates.delete')) ?>" data-wcp-confirm="Delete the certificate <?= e((string) $s['name']) ?> and its private key? Nothing uses it."><?= $this->csrfField() ?><input type="hidden" name="source" value="<?= e((string) $s['source']) ?>"><input type="hidden" name="name" value="<?= e((string) $s['name']) ?>"><button class="btn btn-sm btn-outline-danger">Delete</button></form>
                    <?php elseif ($s['source'] !== 'other'): ?><span class="small wcp-muted">in use</span><?php endif; ?></td>
                </tr>
            <?php endforeach; ?>
            <tr class="<?= $certificates === [] ? '' : 'd-none' ?>" data-wcp-dt-empty><td colspan="5" class="text-center py-4 wcp-muted">No certificates on this server yet.</td></tr>
            </tbody>
        </table>
    </div>
    <div class="wcp-pager" data-wcp-dt-pager></div>
</div>
