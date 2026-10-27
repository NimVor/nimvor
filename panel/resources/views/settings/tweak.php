<?php
/**
 * Tweak Settings: every panel setting by tab, with search, its default,
 * whether it was changed, and a reset.
 *
 * @var \NimVor\View\View $this
 * @var array<string, array<string, array<string, mixed>>> $tabs
 * @var array<string, array{0: string, 1: string}> $labels
 * @var array<string, mixed> $values
 * @var array<string, bool> $editable
 * @var array<string, string|null> $ownPage
 * @var list<array<string, mixed>> $plans
 */
$old ??= [];
$errors = (array) ($this->shared('errors') ?? []);
$planNames = [0 => 'None'];
foreach ($plans as $p) {
    $planNames[(int) $p['id']] = (string) $p['name'];
}
// How a value reads (defaults and current values).
$show = static function (string $key, array $def, mixed $v) use ($planNames): string {
    if ($def['type'] === 'bool') {
        return $v ? 'On' : 'Off';
    }
    if ($key === 'default_plan_id') {
        return $planNames[(int) $v] ?? ('plan #' . (int) $v);
    }
    if (isset($def['options'])) {
        return (string) ($def['options'][(string) $v] ?? $v);
    }
    return (string) $v === '' ? '(empty)' : (string) $v;
};
$first = array_key_first($tabs);
foreach ($tabs as $t => $defs) {
    if (array_intersect_key($errors, $defs) !== []) {
        $first = $t;
        break;
    }
}
$changedCount = [];
foreach ($tabs as $t => $defs) {
    $changedCount[$t] = count(array_filter(array_keys($defs), static fn (string $k): bool => $values[$k] !== $defs[$k]['default']));
}
?>
<?php $this->start(); ?>Tweak Settings<?php $this->end('title'); ?>
<?php $this->start(); ?>Every panel setting with its default; search, change, reset<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
    <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('settings.basic')) ?>"><i class="bi bi-house-gear me-1" aria-hidden="true"></i>Basic WebHost Settings</a>
<?php $this->end('actions'); ?>

<div data-nim-tweak>
    <div class="nim-panel mb-3">
        <div class="nim-statusbar flex-wrap">
            <label class="visually-hidden" for="tw-search">Search settings</label>
            <div class="input-group input-group-sm nim-tweak-search">
                <span class="input-group-text"><i class="bi bi-search" aria-hidden="true"></i></span>
                <input class="form-control" type="search" id="tw-search" placeholder="Search all settings (name, description or key)" data-nim-tweak-search autocomplete="off">
            </div>
            <div class="form-check form-switch mb-0">
                <input class="form-check-input" type="checkbox" role="switch" id="tw-changed" data-nim-tweak-changed>
                <label class="form-check-label small" for="tw-changed">Only settings changed from the default (<?= array_sum($changedCount) ?>)</label>
            </div>
            <span class="small nim-muted" data-nim-tweak-count role="status" aria-live="polite"></span>
        </div>
    </div>

    <div class="row g-3">
        <div class="col-lg-3">
            <nav class="nav nav-pills flex-column nim-settings-nav" role="tablist" aria-label="Setting groups" aria-orientation="vertical">
                <?php foreach ($tabs as $t => $defs): ?>
                    <a class="nav-link d-flex align-items-center<?= $t === $first ? ' active' : '' ?>" id="tw-tab-<?= e($t) ?>" href="#<?= e($t) ?>" data-bs-toggle="pill" data-bs-target="#<?= e($t) ?>" role="tab" aria-controls="<?= e($t) ?>" aria-selected="<?= $t === $first ? 'true' : 'false' ?>">
                        <i class="bi bi-<?= e($labels[$t][1]) ?> me-2" aria-hidden="true"></i><span class="me-auto"><?= e($labels[$t][0]) ?></span>
                        <span class="badge rounded-pill text-bg-light border" data-nim-tweak-tabcount><?= count($defs) ?></span>
                    </a>
                <?php endforeach; ?>
            </nav>
        </div>
        <div class="col-lg-9 tab-content">
            <?php foreach ($tabs as $t => $defs): $can = $editable[$t] ?? false; ?>
                <section class="tab-pane fade<?= $t === $first ? ' show active' : '' ?>" id="<?= e($t) ?>" role="tabpanel" aria-labelledby="tw-tab-<?= e($t) ?>" data-nim-tweak-pane>
                    <h2 class="h6 d-none mb-2" data-nim-tweak-heading><?= e($labels[$t][0]) ?></h2>
                    <form class="nim-panel" method="post" action="<?= e($this->url('settings.tweak.save')) ?>" data-nim-busy novalidate>
                        <?= $this->csrfField() ?><input type="hidden" name="tab" value="<?= e($t) ?>">
                        <?php if (!$can): ?><div class="nim-infobar m-3"><i class="bi bi-lock" aria-hidden="true"></i><span><?= $t === 'policy' || $t === 'hulk' ? 'Only administrators change these settings.' : 'You may view these settings.' ?></span></div><?php endif; ?>
                        <div class="list-group list-group-flush">
                        <?php foreach ($defs as $key => $def):
                            $value = $values[$key];
                            $changed = $value !== $def['default'];
                            $own = array_key_exists($key, $ownPage) ? $ownPage[$key] : false;
                            $shown = $old[$key] ?? ($def['type'] === 'bool' ? ($value ? '1' : '0') : (string) $value);
                            $id = 'tw-' . $key;
                            $hay = mb_strtolower($def['label'] . ' ' . $def['help'] . ' ' . $key . ' ' . $labels[$t][0]);
                            ?>
                            <div class="list-group-item py-3" data-setting="<?= e($key) ?>" data-changed="<?= $changed ? '1' : '0' ?>" data-search="<?= e($hay) ?>">
                                <div class="row g-2 align-items-start">
                                    <div class="col-md-6">
                                        <label class="fw-medium" for="<?= e($id) ?>"><?= e((string) $def['label']) ?></label>
                                        <?php if ($changed): ?><span class="badge text-bg-warning ms-1">Changed</span><?php endif; ?>
                                        <?php if ((string) $def['help'] !== ''): ?><div class="small nim-muted"><?= e((string) $def['help']) ?></div><?php endif; ?>
                                        <div class="small nim-muted mt-1">Default: <span class="fw-medium"><?= e($show($key, $def, $def['default'])) ?></span> · <code class="small"><?= e($key) ?></code></div>
                                    </div>
                                    <div class="col-md-6">
                                        <?php if ($own !== false): ?>
                                            <div class="small"><span class="fw-medium"><?= e($show($key, $def, $value)) ?></span><?php if ($own !== null && $this->hasRoute($own) && $this->routeAllowed($own)): ?> · <a href="<?= e($this->url($own)) ?>">change on its page</a> (it also protects you from being locked out)<?php endif; ?></div>
                                        <?php elseif ($def['type'] === 'bool'): ?>
                                            <div class="form-check form-switch"><input type="hidden" name="<?= e($key) ?>" value="0"><input class="form-check-input" type="checkbox" role="switch" id="<?= e($id) ?>" name="<?= e($key) ?>" value="1"<?= $shown === '1' ? ' checked' : '' ?><?= $can ? '' : ' disabled' ?>><label class="form-check-label small" for="<?= e($id) ?>">On</label></div>
                                        <?php elseif ($key === 'default_plan_id' || isset($def['options'])): ?>
                                            <select class="form-select form-select-sm<?= isset($errors[$key]) ? ' is-invalid' : '' ?>" id="<?= e($id) ?>" name="<?= e($key) ?>"<?= $can ? '' : ' disabled' ?>>
                                                <?php foreach ($key === 'default_plan_id' ? $planNames : $def['options'] as $o => $l): ?><option value="<?= e((string) $o) ?>"<?= (string) $shown === (string) $o ? ' selected' : '' ?>><?= e((string) $l) ?></option><?php endforeach; ?>
                                            </select>
                                        <?php else: ?>
                                            <input class="form-control form-control-sm<?= isset($errors[$key]) ? ' is-invalid' : '' ?>" id="<?= e($id) ?>" name="<?= e($key) ?>" value="<?= e((string) $shown) ?>"
                                                <?= $def['type'] === 'int' ? 'type="number" min="' . (int) $def['min'] . '" max="' . (int) $def['max'] . '"' : 'type="' . ($def['type'] === 'url' ? 'url' : 'text') . '" maxlength="' . (int) ($def['max_length'] ?? 255) . '"' ?><?= $can ? '' : ' disabled' ?>>
                                            <?php if ($def['type'] === 'int'): ?><div class="form-text"><?= (int) $def['min'] ?>–<?= (int) $def['max'] ?></div><?php endif; ?>
                                        <?php endif; ?>
                                        <?php if (isset($errors[$key])): ?><div class="invalid-feedback d-block"><?= e((string) $errors[$key]) ?></div><?php endif; ?>
                                        <?php if ($can && $changed && $own === false): ?>
                                            <button class="btn btn-link btn-sm p-0 mt-1" type="submit" name="reset" value="<?= e($key) ?>" data-nim-confirm-submit="Reset “<?= e((string) $def['label']) ?>” to its default (<?= e($show($key, $def, $def['default'])) ?>)?"><i class="bi bi-arrow-counterclockwise me-1" aria-hidden="true"></i>Reset to default</button>
                                        <?php endif; ?>
                                    </div>
                                </div>
                            </div>
                        <?php endforeach; ?>
                        </div>
                        <?php if ($can): ?>
                            <div class="d-flex flex-wrap gap-2 p-3 border-top">
                                <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save <?= e(mb_strtolower($labels[$t][0])) ?></button>
                                <?php if ($changedCount[$t] > 0): ?><button class="btn btn-outline-secondary" type="submit" name="reset" value="all" data-nim-confirm-submit="Reset the <?= (int) $changedCount[$t] ?> changed setting(s) under <?= e($labels[$t][0]) ?> to their defaults?">Reset this tab to defaults</button><?php endif; ?>
                            </div>
                        <?php endif; ?>
                    </form>
                </section>
            <?php endforeach; ?>
            <div class="nim-panel p-4 text-center nim-muted d-none" data-nim-tweak-empty>No setting matches.</div>
        </div>
    </div>
</div>
