<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed>|null $report */
/** @var string $source */
/** @var string $range */
/** @var array{address: string, status: string} $filters */
$ranges = ['1h' => 'Last hour', '24h' => 'Last 24 hours', '7d' => 'Last 7 days', '30d' => 'Last 30 days'];
$states = ['sent' => ['Sent', 'is-ok'], 'delivered' => ['Delivered here', 'is-ok'], 'deferred' => ['Deferred', 'is-warn'], 'bounced' => ['Bounced', 'is-error'], 'rejected' => ['Rejected', 'is-error']];
$t = (array) ($report['totals'] ?? []);
?>
<?= $this->partial('mail/admin/_tabs', get_defined_vars()) ?>
<?php if ($report === null) { return; } ?>
<form class="nim-toolbar mb-3" method="get" action="<?= e($this->url('mail.admin.reports')) ?>">
    <?php if ($tab === 'stats'): ?><input type="hidden" name="view" value="stats"><?php endif; ?>
    <select class="form-select form-select-sm w-auto" name="range" aria-label="Time range" data-nim-autosubmit><?php foreach ($ranges as $k => $l): ?><option value="<?= e($k) ?>"<?= $range === $k ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?></select>
    <?php if ($tab === 'reports'): ?>
        <input class="form-control form-control-sm nim-w-30" type="search" name="address" value="<?= e($filters['address']) ?>" placeholder="Sender or recipient contains…" aria-label="Address">
        <select class="form-select form-select-sm w-auto" name="status" aria-label="Status"><option value="">Any status</option><?php foreach ($states as $k => [$l]): ?><option value="<?= e($k) ?>"<?= $filters['status'] === $k ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?></select>
        <button class="btn btn-sm btn-outline-secondary">Filter</button>
    <?php endif; ?>
    <span class="small text-body-secondary ms-auto"><?= $source === '' ? 'No mail log found (journal, /var/log/mail.log or maillog).' : 'From ' . e($source) ?></span>
</form>

<?php if ($tab === 'stats'): ?>
    <section class="nim-site-summary mb-3" aria-label="Totals">
        <?php foreach (['received' => 'Received', 'sent' => 'Sent out', 'delivered' => 'Delivered here', 'deferred' => 'Deferred', 'bounced' => 'Bounced', 'rejected' => 'Rejected', 'auth_ok' => 'Logins', 'auth_failed' => 'Failed logins'] as $k => $l): ?>
            <div class="nim-sum"><div class="nim-sum-label"><?= e($l) ?></div><div class="nim-sum-value<?= in_array($k, ['bounced', 'rejected', 'auth_failed'], true) && ($t[$k] ?? 0) > 0 ? ' nim-bad' : '' ?>"><?= e(number_format((int) ($t[$k] ?? 0))) ?></div></div>
        <?php endforeach; ?>
    </section>
    <div class="row g-3">
        <div class="col-xl-6">
            <div class="card h-100"><div class="card-header"><i class="bi bi-bar-chart me-1"></i>Over time</div>
                <?php $series = (array) ($report['series'] ?? []); $max = 1; foreach ($series as $s) { $max = max($max, (int) ($s['received'] ?? 0) + (int) ($s['sent'] ?? 0)); } ?>
                <?php if ($series === []): ?><div class="card-body small text-body-secondary">No mail in this period.</div><?php else: ?>
                <ul class="list-group list-group-flush small">
                    <?php foreach ($series as $s): $v = (int) ($s['received'] ?? 0) + (int) ($s['sent'] ?? 0); ?>
                        <li class="list-group-item">
                            <div class="d-flex"><span><?= e(str_replace('T', ' ', substr((string) $s['start'], 0, in_array($range, ['7d', '30d'], true) ? 10 : 16))) ?></span><span class="ms-auto"><?= (int) ($s['received'] ?? 0) ?> in · <?= (int) ($s['sent'] ?? 0) ?> out<?= ($s['bounced'] ?? 0) + ($s['rejected'] ?? 0) > 0 ? ' · <span class="nim-bad">' . ((int) ($s['bounced'] ?? 0) + (int) ($s['rejected'] ?? 0)) . ' failed</span>' : '' ?></span></div>
                            <div class="progress mt-1 nim-progress-thin" role="presentation"><div class="progress-bar nim-w-<?= (int) (ceil($v * 100 / $max / 5) * 5) ?>"></div></div>
                        </li>
                    <?php endforeach; ?>
                </ul>
                <?php endif; ?>
            </div>
        </div>
        <div class="col-xl-6">
            <div class="card mb-3"><div class="card-header"><i class="bi bi-people me-1"></i>Relayers — who sent mail</div>
                <?php $rel = (array) ($report['relayers'] ?? []); ?>
                <?php if ($rel === []): ?><div class="card-body small text-body-secondary">Nobody sent mail in this period.</div><?php else: ?>
                <ul class="list-group list-group-flush small">
                    <?php foreach (array_slice($rel, 0, 15) as $r): ?>
                        <li class="list-group-item d-flex gap-2"><span class="badge text-bg-light border"><?= e(['sasl' => 'mailbox', 'local' => 'server account', 'client' => 'client'][$r['kind']] ?? $r['kind']) ?></span><span class="text-break"><?= e($r['name']) ?></span><span class="ms-auto fw-medium"><?= (int) $r['messages'] ?></span></li>
                    <?php endforeach; ?>
                </ul>
                <?php endif; ?>
                <div class="card-footer small text-body-secondary">A "server account" is mail sent by a program on the server (e.g. a website's PHP) — the usual source of spam from a hacked site.</div>
            </div>
            <div class="card"><div class="card-header"><i class="bi bi-send me-1"></i>Sent summary by sender domain</div>
                <?php $snd = (array) ($report['senders'] ?? []); ?>
                <?php if ($snd === []): ?><div class="card-body small text-body-secondary">No mail was sent out.</div><?php else: ?>
                <ul class="list-group list-group-flush small">
                    <?php foreach (array_slice($snd, 0, 15) as $r): ?><li class="list-group-item d-flex"><span><?= e($r['name']) ?></span><span class="ms-auto fw-medium"><?= (int) $r['messages'] ?></span></li><?php endforeach; ?>
                </ul>
                <?php endif; ?>
            </div>
        </div>
    </div>
<?php else: ?>
    <div class="nim-panel" data-nim-dt id="deliveries">
        <div class="nim-dtable table-responsive">
            <table class="table align-middle">
                <thead><tr><th>Time (UTC)</th><th>From</th><th>To</th><th>Status</th><th>Details</th></tr></thead>
                <tbody>
                <?php foreach ((array) $report['deliveries'] as $d): [$l, $cls] = $states[$d['status']] ?? [$d['status'], '']; ?>
                    <tr>
                        <td class="small text-nowrap"><?= e(str_replace('T', ' ', substr((string) $d['time'], 0, 19))) ?></td>
                        <td class="small text-break"><?= e($d['from'] !== '' ? $d['from'] : '—') ?></td>
                        <td class="small text-break"><?= e($d['to']) ?></td>
                        <td class="small"><span class="nim-state <?= e($cls) ?>"><?= e($l) ?></span><div class="nim-muted"><?= e($d['dsn']) ?></div></td>
                        <td class="small"><div class="text-break"><?= e(mb_strimwidth((string) $d['reason'], 0, 160, '…')) ?></div><div class="nim-muted text-break"><?= e($d['relay']) ?><?= $d['delay'] !== '' ? ' · ' . e($d['delay']) . ' s' : '' ?><?= $d['id'] !== '' ? ' · ' . e($d['id']) : '' ?></div></td>
                    </tr>
                <?php endforeach; ?>
                <?php if ((array) $report['deliveries'] === []): ?><tr><td colspan="5" class="text-center py-4 nim-muted">No deliveries match.</td></tr><?php endif; ?>
                </tbody>
            </table>
        </div>
        <?php if (!empty($report['truncated'])): ?><div class="small text-body-secondary mt-2">Showing the newest 500; narrow the filter to see others.</div><?php endif; ?>
    </div>
<?php endif; ?>
