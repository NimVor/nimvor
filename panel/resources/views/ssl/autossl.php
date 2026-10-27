<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed>|null $renewal
 * @var list<string> $renewLog
 * @var list<array<string, mixed>> $log
 * @var list<array<string, mixed>> $queued
 * @var int|null $nextCheck
 * @var bool $autoNew
 * @var bool $autoReplace
 */
use WhaCP\Security\Permissions as P;

$canManage = $this->can(P::SSL_MANAGE);
$badge = ['issued' => 'is-ok', 'failed' => 'is-error', 'skipped' => 'is-warn', 'queued' => ''];
?>
<?= $this->partial('ssl/_tabs', get_defined_vars()) ?>

<div class="wcp-autossl-hero card mb-3">
    <div class="card-body d-flex flex-wrap gap-3 align-items-center">
        <div class="me-auto">
            <div class="fw-semibold mb-1">Current provider: Let's Encrypt</div>
            <div class="small">
                <?php if ($nextCheck !== null): ?>The next regular AutoSSL check runs at <strong><?= e(date('H:i', max($nextCheck, time()))) ?></strong> (every 5 minutes for new websites; certificates to replace are looked for every 6 hours).
                <?php else: ?><span class="wcp-state is-warn">The scheduler has not run yet</span> — enable whacp-scheduler.timer.<?php endif; ?>
            </div>
            <div class="small mt-1">
                Automatic SSL for new websites: <span class="wcp-state <?= $autoNew ? 'is-ok' : '' ?>"><?= $autoNew ? 'on' : 'off' ?></span>
                · Replace expired or invalid certificates: <span class="wcp-state <?= $autoReplace ? 'is-ok' : '' ?>"><?= $autoReplace ? 'on' : 'off' ?></span>
                <?php if ($this->hasRoute('settings') && $this->can(P::SETTINGS_MANAGE)): ?> · <a href="<?= e($this->url('settings')) ?>#ssl">Change</a><?php endif; ?>
            </div>
            <div class="small mt-1">
                Renewal of Let's Encrypt certificates:
                <?php if ($renewal === null): ?>unknown
                <?php elseif (!empty($renewal['timer_active'])): ?><span class="wcp-state is-ok">scheduled</span> <span class="wcp-muted">(<?= e((string) $renewal['method']) ?>)</span>
                <?php elseif (empty($renewal['certbot_installed'])): ?><span class="wcp-state is-warn">certbot is not installed</span>
                <?php else: ?><span class="wcp-state is-error">not scheduled</span><?php endif; ?>
            </div>
        </div>
        <?php if ($canManage): ?>
            <div class="d-flex flex-column gap-2">
                <form method="post" action="<?= e($this->url('ssl.autossl.run')) ?>" data-wcp-noajax data-wcp-busy data-wcp-confirm="Check every website now? Websites without HTTPS and those with an expired, expiring or invalid certificate get a Let's Encrypt certificate once their domain points to this server.">
                    <?= $this->csrfField() ?><button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><i class="bi bi-arrow-repeat me-1"></i>Run AutoSSL for all websites</button>
                </form>
                <?php if ($renewal !== null && empty($renewal['timer_active']) && !empty($renewal['can_enable'])): ?>
                    <form method="post" action="<?= e($this->url('ssl.renewal.enable')) ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-primary w-100">Turn on renewal</button></form>
                <?php endif; ?>
            </div>
        <?php endif; ?>
    </div>
</div>

<?php if ($queued !== []): ?>
<div class="wcp-panel mb-3">
    <div class="wcp-actionbar"><div class="fw-semibold">Waiting (<?= count($queued) ?>)</div></div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle table-sm">
            <thead><tr><th>Website</th><th>Why</th><th>Attempts</th><th>Next try</th></tr></thead>
            <tbody>
            <?php foreach ($queued as $q): ?>
                <tr><td class="fw-semibold"><?= e((string) $q['domain']) ?></td><td class="small"><?= e((string) $q['auto_ssl_note']) ?></td><td><?= (int) $q['auto_ssl_tries'] ?></td><td class="small text-nowrap"><?= $q['auto_ssl_next'] ? e(date('Y-m-d H:i', (int) strtotime((string) $q['auto_ssl_next']))) : 'now' ?></td></tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
</div>
<?php endif; ?>

<div class="wcp-panel mb-3" data-wcp-dt>
    <div class="wcp-actionbar">
        <div class="fw-semibold">AutoSSL log</div>
        <div class="wcp-actionbar-end">
            <select class="form-select form-select-sm w-auto" aria-label="Show" data-wcp-dt-select="result"><option value="">Everything</option><option value="issued">Issued</option><option value="failed">Failed</option><option value="skipped">Skipped</option><option value="queued">Queued</option></select>
            <div class="wcp-search"><input class="form-control form-control-sm" type="search" placeholder="Search domains" aria-label="Search domains" data-wcp-dt-search><i class="bi bi-search" aria-hidden="true"></i></div>
        </div>
    </div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle table-sm">
            <thead><tr><th>Time</th><th>Website</th><th>Result</th><th>Details</th></tr></thead>
            <tbody>
            <?php foreach ($log as $l): ?>
                <tr data-wcp-row data-dt-result="<?= e((string) $l['result']) ?>">
                    <td class="small text-nowrap"><?= e(date('Y-m-d H:i', (int) strtotime((string) $l['created_at']))) ?></td>
                    <td class="fw-semibold"><?= e((string) $l['domain']) ?></td>
                    <td><span class="wcp-state <?= $badge[$l['result']] ?? '' ?>"><?= e((string) $l['result']) ?></span></td>
                    <td class="small text-break"><?= e((string) $l['message']) ?></td>
                </tr>
            <?php endforeach; ?>
            <tr class="<?= $log === [] ? '' : 'd-none' ?>" data-wcp-dt-empty><td colspan="4" class="text-center py-4 wcp-muted">Nothing logged yet.</td></tr>
            </tbody>
        </table>
    </div>
    <div class="wcp-pager" data-wcp-dt-pager></div>
</div>

<div class="card">
    <div class="card-header"><i class="bi bi-clock-history me-1"></i>Renewal log (certbot)</div>
    <div class="card-body">
        <pre class="wcp-db-create small mb-0 wcp-update-log"><?= $renewLog !== [] ? e(implode("\n", $renewLog)) : 'certbot has not logged a run yet.' ?></pre>
        <p class="small wcp-muted mt-2 mb-0">certbot renews Let's Encrypt certificates 30 days before they expire, twice a day; each run's results are shown here (from /var/log/letsencrypt).</p>
    </div>
</div>
