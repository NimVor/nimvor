<?php
/**
 * @var \NimVor\View\View $this
 * @var list<array<string, mixed>> $hosts
 * @var array<string, array<string, mixed>> $sites
 * @var array<string, mixed>|null $renewal
 * @var bool $autoSsl
 * @var int $warnDays
 */
$counts = ['ok' => 0, 'warn' => 0, 'none' => 0];
foreach ($hosts as $h) {
    $c = $h['certificate'] ?? null;
    if (!$h['https']) {
        $counts['none']++;
    } elseif (!$h['covered'] || !empty($c['expired']) || !empty($c['error']) || (int) ($c['days_remaining'] ?? 0) <= $warnDays) {
        $counts['warn']++;
    } else {
        $counts['ok']++;
    }
}
?>
<?= $this->partial('ssl/_tabs', get_defined_vars()) ?>

<div class="nim-infobar">
    <i class="bi bi-arrow-repeat" aria-hidden="true"></i>
    <span>AutoSSL for new websites is <strong><?= $autoSsl ? 'on' : 'off' ?></strong>.
        Renewal of Let's Encrypt certificates: <?php if ($renewal === null): ?>unknown<?php elseif (!empty($renewal['timer_active'])): ?><span class="nim-state is-ok">active</span> <span class="nim-muted small">(<?= e((string) $renewal['method']) ?>)</span><?php elseif (empty($renewal['certbot_installed'])): ?><span class="nim-state is-warn">certbot is not installed</span><?php else: ?><span class="nim-state is-error">not scheduled</span><?php endif; ?></span>
    <?php if ($renewal !== null && empty($renewal['timer_active']) && !empty($renewal['can_enable']) && $this->can(\NimVor\Security\Permissions::SSL_MANAGE)): ?>
        <form method="post" action="<?= e($this->url('ssl.renewal.enable')) ?>" class="d-inline"><?= $this->csrfField() ?><button class="btn btn-sm btn-primary">Turn on renewal</button></form>
    <?php endif; ?>
    <?php if ($this->hasRoute('settings')): ?><a class="ms-auto" href="<?= e($this->url('settings')) ?>#ssl">AutoSSL settings</a><?php endif; ?>
</div>

<div class="nim-panel" data-nim-dt>
    <div class="nim-actionbar">
        <div class="fw-semibold"><?= count($hosts) ?> host names · <span class="nim-state is-ok"><?= $counts['ok'] ?> secure</span> <span class="nim-state is-warn"><?= $counts['warn'] ?> need attention</span> <span class="nim-state"><?= $counts['none'] ?> without HTTPS</span></div>
        <div class="nim-actionbar-end">
            <select class="form-select form-select-sm w-auto" aria-label="Show" data-nim-dt-select="state"><option value="">All hosts</option><option value="warn">Need attention</option><option value="none">Without HTTPS</option><option value="ok">Secure</option></select>
            <div class="nim-search"><input class="form-control form-control-sm" type="search" placeholder="Search host names" aria-label="Search host names" data-nim-dt-search><i class="bi bi-search" aria-hidden="true"></i></div>
        </div>
    </div>
    <div class="nim-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Host name</th><th>HTTPS</th><th>Certificate</th><th>Served by</th><th class="text-end">Operate</th></tr></thead>
            <tbody>
            <?php foreach ($hosts as $h): $c = $h['certificate'] ?? null; $site = $sites[$h['name']] ?? null;
                $state = !$h['https'] ? 'none' : ((!$h['covered'] || !empty($c['expired']) || !empty($c['error']) || (int) ($c['days_remaining'] ?? 0) <= $warnDays) ? 'warn' : 'ok'); ?>
                <tr data-nim-row data-dt-state="<?= $state ?>">
                    <td class="fw-semibold font-monospace text-break"><?= e((string) $h['name']) ?><?= empty($h['enabled']) ? ' <span class="badge text-bg-light border fw-normal">disabled</span>' : '' ?></td>
                    <td class="small"><?php if (!$h['https']): ?><span class="nim-muted">No</span><?php else: ?>Yes<?= !empty($h['redirects_to_https']) ? ' <span class="nim-muted">· HTTP redirects</span>' : '' ?><?php endif; ?></td>
                    <td><?php if ($c !== null): ?><?= $this->partial('ssl/_cert', ['c' => $c, 'warnDays' => $warnDays]) ?><?= !$h['covered'] ? '<div class="small text-danger">Does not cover this name (browsers warn)</div>' : '' ?><?php else: ?><span class="nim-muted small">—</span><?php endif; ?></td>
                    <td class="small font-monospace text-break"><?= e((string) $h['file']) ?></td>
                    <td class="text-end text-nowrap"><?php if ($site !== null): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.ssl', ['id' => (int) $site['id']])) ?>"><?= $h['https'] ? 'Manage' : 'Enable HTTPS' ?></a><?php else: ?><span class="small nim-muted"><?= !empty($h['nimvor']) ? 'NimVor service' : 'not managed' ?></span><?php endif; ?></td>
                </tr>
            <?php endforeach; ?>
            <tr class="<?= $hosts === [] ? '' : 'd-none' ?>" data-nim-dt-empty><td colspan="5" class="text-center py-4 nim-muted">No host names found in Nginx.</td></tr>
            </tbody>
        </table>
    </div>
    <div class="nim-pager" data-nim-dt-pager></div>
</div>
