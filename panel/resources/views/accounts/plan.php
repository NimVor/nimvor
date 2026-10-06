<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $account */
/** @var list<array<string, mixed>> $plans */
/** @var array<string, mixed>|null $target */
/** @var array<string, mixed>|null $current */
/** @var list<array{label: string, from: int, to: int, used: ?int, override: ?int, over: bool}> $diff */
$fmt = static fn (int $v): string => $v > 0 ? number_format($v) : 'Unlimited';
$currentId = $account['plan_id'] !== null ? (int) $account['plan_id'] : 0;
$over = array_filter($diff, static fn (array $r): bool => $r['over']);
?>
<?php $this->start(); ?>Change plan: <?= e($account['username']) ?><?php $this->end('title'); ?>
<?php $this->start(); ?>Upgrade or downgrade the hosting plan; the account's data is kept<?php $this->end('subtitle'); ?>


<div class="card mb-3">
    <div class="card-body">
        <form method="get" action="<?= e($this->url('users.plan', ['id' => (int) $account['id']])) ?>" class="row g-2 align-items-end">
            <div class="col-md-4">
                <div class="form-label">Current plan</div>
                <div class="fw-semibold"><?= e($current['name'] ?? 'No plan (unlimited)') ?></div>
            </div>
            <div class="col-md-5">
                <label class="form-label" for="p-target">New plan</label>
                <select class="form-select" id="p-target" name="plan" data-wcp-autosubmit>
                    <option value="">Choose a plan…</option>
                    <?php foreach ($plans as $p): if ((int) $p['id'] === $currentId) { continue; } ?>
                        <option value="<?= (int) $p['id'] ?>"<?= $target !== null && (int) $target['id'] === (int) $p['id'] ? ' selected' : '' ?>><?= e($p['name']) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>
            <div class="col-md-auto"><button class="btn btn-outline-secondary">Compare</button></div>
        </form>
    </div>
</div>

<?php if ($target !== null): ?>
    <div class="card mb-3" id="plan-diff">
        <div class="card-header">From “<?= e($current['name'] ?? 'no plan') ?>” to “<?= e($target['name']) ?>”</div>
        <div class="wcp-dtable table-responsive">
            <table class="table align-middle mb-0">
                <thead><tr><th>Limit</th><th class="text-end">Now</th><th class="text-end">New plan</th><th class="text-end">In use</th><th>Note</th></tr></thead>
                <tbody>
                <?php foreach ($diff as $r): $change = $r['to'] === $r['from'] ? '' : (($r['to'] === 0 || ($r['from'] !== 0 && $r['to'] > $r['from'])) ? 'up' : 'down'); ?>
                    <tr<?= $r['over'] ? ' class="table-warning"' : '' ?>>
                        <td><?= e($r['label']) ?></td>
                        <td class="text-end"><?= e($fmt($r['from'])) ?></td>
                        <td class="text-end fw-semibold <?= $change === 'up' ? 'text-success' : ($change === 'down' ? 'text-danger' : '') ?>"><?= $change === 'up' ? '<i class="bi bi-arrow-up-short" aria-label="higher"></i>' : ($change === 'down' ? '<i class="bi bi-arrow-down-short" aria-label="lower"></i>' : '') ?><?= e($fmt($r['to'])) ?></td>
                        <td class="text-end"><?= $r['used'] === null ? '—' : e(number_format($r['used'])) ?></td>
                        <td class="small">
                            <?php if ($r['override'] !== null): ?>Account override stays: <?= e($fmt($r['override'])) ?>. <?php endif; ?>
                            <?php if ($r['over']): ?><span class="wcp-bad">Already over the new limit.</span><?php endif; ?>
                        </td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    </div>
    <?php if ($over !== []): ?>
        <div class="alert alert-warning small">The account already uses more than the new plan allows for <?= count($over) ?> limit(s). Nothing is removed: it keeps what it has but cannot add more until it is below the limit.</div>
    <?php endif; ?>
    <form method="post" action="<?= e($this->url('users.plan.change', ['id' => (int) $account['id']])) ?>" data-wcp-busy data-wcp-confirm="Move <?= e($account['username']) ?> to the “<?= e($target['name']) ?>” plan? The new limits apply immediately.">
        <?= $this->csrfField() ?><input type="hidden" name="plan_id" value="<?= (int) $target['id'] ?>">
        <button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Change to “<?= e($target['name']) ?>”</button>
    </form>
<?php endif; ?>
