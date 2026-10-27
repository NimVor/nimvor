<?php
/**
 * ModSecurity events table.
 *
 * @var \WhaCP\View\View $this
 * @var list<array<string, mixed>> $events
 * @var array<string, array<string, mixed>> $byDomain host => website (links)
 * @var bool $showHost
 * @var array<string, mixed>|null $disableFor website whose rules an administrator may turn off here
 */
$disableFor ??= null;
// The anomaly score summaries say that a request was blocked, not why.
$summary = ['949110', '959100', '980130', '980140', '980170'];
?>
<div class="wcp-dtable table-responsive">
    <table class="table align-middle">
        <thead><tr><th>Time</th><?php if ($showHost): ?><th>Website</th><?php endif; ?><th>Request</th><th>Result</th><th>Rules</th></tr></thead>
        <tbody>
        <?php foreach ($events as $ev): $site = $byDomain[(string) $ev['host']] ?? null; $blocked = (int) $ev['status'] >= 400 && ($ev['engine'] ?? '') !== 'DetectionOnly'; ?>
            <tr data-wcp-row>
                <td class="text-nowrap small" title="<?= e((string) $ev['time']) ?>"><?= e(time_ago((string) $ev['time'])) ?></td>
                <?php if ($showHost): ?><td class="small"><?php if ($site !== null): ?><a href="<?= e($this->url('sites.firewall', ['id' => (int) $site['id']])) ?>"><?= e((string) $ev['host']) ?></a><?php else: ?><?= e((string) $ev['host']) ?><?php endif; ?></td><?php endif; ?>
                <td class="small"><span class="font-monospace"><?= e((string) $ev['method']) ?> <?= e(mb_strimwidth((string) $ev['uri'], 0, 80, '…')) ?></span><div class="wcp-muted"><?= e((string) $ev['client_ip']) ?></div></td>
                <td><span class="wcp-state <?= $blocked ? 'is-error' : 'is-warn' ?>"><?= $blocked ? 'Blocked (' . (int) $ev['status'] . ')' : 'Detected' ?></span></td>
                <td class="small">
                    <?php foreach ((array) $ev['rules'] as $r): if (in_array((string) $r['id'], $summary, true)) { continue; } ?>
                        <div class="d-flex align-items-start gap-2 mb-1">
                            <span class="font-monospace text-nowrap"><?= e((string) $r['id']) ?></span>
                            <span class="flex-grow-1"><?= e((string) $r['message']) ?></span>
                            <?php if ($disableFor !== null && (int) $r['id'] > 0): ?>
                                <form method="post" action="<?= e($this->url('sites.firewall.update', ['id' => (int) $disableFor['id']])) ?>" data-wcp-busy data-wcp-confirm="Turn rule <?= e((string) $r['id']) ?> off for <?= e((string) $disableFor['domain']) ?>? Requests that only match it are no longer blocked on this website."><?= $this->csrfField() ?><input type="hidden" name="disable_rule" value="<?= e((string) $r['id']) ?>"><button class="btn btn-sm btn-link p-0 text-nowrap">Turn off here</button></form>
                            <?php endif; ?>
                        </div>
                    <?php endforeach; ?>
                </td>
            </tr>
        <?php endforeach; ?>
        <?php if ($events === []): ?><tr><td colspan="<?= $showHost ? 5 : 4 ?>" class="text-center py-4 wcp-muted">No blocked or detected requests.</td></tr><?php endif; ?>
        </tbody>
    </table>
</div>
