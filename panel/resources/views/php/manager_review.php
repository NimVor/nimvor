<?php
/**
 * PHP Manager: the review of a profile before applying it.
 *
 * @var \NimVor\View\View $this
 * @var array{versions: list<string>, extensions: list<string>} $profile
 * @var array{items: list<array<string, mixed>>, changes: bool, removals: bool} $plan
 * @var array<string, string> $eol
 */
$groups = [
    'install' => ['To install', 'bi-plus-circle text-success'],
    'update' => ['To update', 'bi-arrow-up-circle text-primary'],
    'enable' => ['To switch on', 'bi-toggle-on text-success'],
    'disable' => ['To switch off', 'bi-toggle-off text-warning'],
    'remove' => ['To remove', 'bi-dash-circle text-danger'],
    'blocked' => ['Kept or not possible', 'bi-lock text-body-secondary'],
    'unchanged' => ['No change', 'bi-check-circle text-body-secondary'],
];
$by = [];
foreach ((array) $plan['items'] as $it) {
    $by[(string) $it['action']][] = $it;
}
$what = static fn (array $it): string => 'PHP ' . $it['version'] . (($it['extension'] ?? '') !== '' ? ' · ' . $it['extension'] : '');
?>
<?php $this->start(); ?>Review PHP changes<?php $this->end('title'); ?>
<?php $this->start(); ?>PHP <?= e(implode(', ', $profile['versions'])) ?> with <?= count($profile['extensions']) ?> extension(s)<?php $this->end('subtitle'); ?>

<div class="row g-3 mb-3">
    <?php foreach ($groups as $k => [$label, $icon]): if (empty($by[$k])) { continue; } ?>
        <div class="col-6 col-md-4 col-xl"><div class="nim-panel p-3 h-100" data-group="<?= e($k) ?>"><div class="small nim-muted"><i class="bi <?= e($icon) ?> me-1" aria-hidden="true"></i><?= e($label) ?></div><div class="fs-4 fw-semibold"><?= count($by[$k]) ?></div></div></div>
    <?php endforeach; ?>
</div>

<?php foreach ($groups as $k => [$label, $icon]): if (empty($by[$k])) { continue; } ?>
    <details class="nim-panel mb-3"<?= $k === 'unchanged' ? '' : ' open' ?> data-review="<?= e($k) ?>">
        <summary class="p-3 fw-semibold"><i class="bi <?= e($icon) ?> me-1" aria-hidden="true"></i><?= e($label) ?> (<?= count($by[$k]) ?>)</summary>
        <div class="nim-dtable table-responsive">
            <table class="table table-sm align-middle mb-0">
                <tbody>
                <?php foreach ($by[$k] as $it): ?>
                    <tr>
                        <td class="fw-medium text-nowrap"><?= e($what($it)) ?></td>
                        <td class="small"><?= e((string) ($it['detail'] ?? '')) ?></td>
                        <td class="small font-monospace nim-muted text-break"><?= e(implode(' ', (array) ($it['packages'] ?? []))) ?></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    </details>
<?php endforeach; ?>

<?php if (empty($plan['changes'])): ?>
    <div class="nim-infobar mb-3"><i class="bi bi-check-circle-fill" aria-hidden="true"></i><span>The server already matches this profile: nothing to apply.</span></div>
    <a class="btn btn-outline-secondary" href="<?= e($this->url('php.manager')) ?>">Back</a>
<?php else: ?>
    <form method="post" action="<?= e($this->url('php.manager.apply')) ?>" class="nim-panel p-3" data-nim-busy>
        <?= $this->csrfField() ?>
        <?php foreach ($profile['versions'] as $v): ?><input type="hidden" name="versions[]" value="<?= e($v) ?>"><?php endforeach; ?>
        <?php foreach ($profile['extensions'] as $x): ?><input type="hidden" name="extensions[]" value="<?= e($x) ?>"><?php endforeach; ?>
        <?php if (!empty($plan['removals'])): ?>
            <div class="form-check mb-3">
                <input class="form-check-input" type="checkbox" name="confirm_removals" value="1" id="pm-confirm" required>
                <label class="form-check-label" for="pm-confirm">I understand that the versions and extensions under <strong>To remove</strong> and <strong>To switch off</strong> go away. Websites that need them stop working.</label>
            </div>
        <?php endif; ?>
        <div class="d-flex flex-wrap align-items-center gap-2">
            <button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Apply</button>
            <a class="btn btn-outline-secondary" href="<?= e($this->url('php.manager')) ?>">Change the selection</a>
            <span class="small nim-muted">Runs as one job (see its log). The plan is checked again when it starts; each version is tested and started at boot afterwards. The profile is kept for next time and can be exported.</span>
        </div>
    </form>
<?php endif; ?>
