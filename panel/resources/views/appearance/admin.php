<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $values */
/** @var array<string, array<string, mixed>> $definitions */
use NimVor\View\Themes;

$canEdit = $this->can(\NimVor\Security\Permissions::SETTINGS_MANAGE);
$errors = (array) ($this->shared('errors') ?? []);
$old = (array) ($this->shared('old') ?? []);
$val = static fn (string $k) => $old[$k] ?? $values[$k];
$checkbox = function (string $key) use ($definitions, $val, $errors): string {
    $on = filter_var($val($key), FILTER_VALIDATE_BOOL);
    return '<input type="hidden" name="' . e($key) . '" value="0"><div class="form-check form-switch"><input class="form-check-input" type="checkbox" role="switch" id="' . e($key) . '" name="' . e($key) . '" value="1"' . ($on ? ' checked' : '') . '><label class="form-check-label" for="' . e($key) . '">' . e($definitions[$key]['label']) . '</label></div>'
        . '<div class="form-text mb-2">' . e($definitions[$key]['help']) . '</div>';
};
?>
<?php $this->start(); ?>Customer panel themes<?php $this->end('title'); ?>
<?php $this->start(); ?>Which themes hosting customers can use, and how the panel looks until they choose<?php $this->end('subtitle'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('settings')) ?>#themes"><i class="bi bi-arrow-left me-1" aria-hidden="true"></i>Settings</a><?php $this->end('actions'); ?>

<form method="post" action="<?= e($this->url('settings.themes.save')) ?>" data-nim-noajax>
    <?= $this->csrfField() ?>
    <fieldset<?= $canEdit ? '' : ' disabled' ?>>
    <div class="row g-3">
        <div class="col-lg-7">
            <section class="card mb-3">
                <div class="card-header"><i class="bi bi-layout-text-window me-1" aria-hidden="true"></i>Themes offered</div>
                <div class="card-body">
                    <p class="small text-body-secondary">NimVor Modern is always offered. A theme changes how the customer panel looks and how its menus are arranged; every theme has every feature, the same permissions and the same security checks.</p>
                    <?php foreach (Themes::all() as $key => $t): ?>
                        <div class="d-flex gap-3 align-items-start py-2 border-top">
                            <?= $this->partial('appearance/thumb', ['theme' => $key]) ?>
                            <div class="flex-grow-1">
                                <div class="fw-semibold"><?= e($t['label']) ?></div>
                                <div class="small text-body-secondary mb-1"><?= e($t['tagline']) ?></div>
                                <?php if ($key === Themes::DEFAULT): ?>
                                    <span class="small text-success"><i class="bi bi-check-circle-fill me-1" aria-hidden="true"></i>Always on</span>
                                <?php else: ?>
                                    <input type="hidden" name="theme_enabled_<?= e($key) ?>" value="0">
                                    <div class="form-check form-switch"><input class="form-check-input" type="checkbox" role="switch" id="theme_enabled_<?= e($key) ?>" name="theme_enabled_<?= e($key) ?>" value="1"<?= filter_var($val('theme_enabled_' . $key), FILTER_VALIDATE_BOOL) ? ' checked' : '' ?>><label class="form-check-label" for="theme_enabled_<?= e($key) ?>">Offer to customers</label></div>
                                <?php endif; ?>
                            </div>
                        </div>
                    <?php endforeach; ?>
                </div>
            </section>
        </div>
        <div class="col-lg-5">
            <section class="card mb-3">
                <div class="card-header"><i class="bi bi-sliders me-1" aria-hidden="true"></i>Defaults</div>
                <div class="card-body">
                    <div class="mb-3">
                        <label class="form-label" for="theme_default"><?= e($definitions['theme_default']['label']) ?></label>
                        <select class="form-select<?= isset($errors['theme_default']) ? ' is-invalid' : '' ?>" id="theme_default" name="theme_default">
                            <?php foreach ($definitions['theme_default']['options'] as $k => $label): ?><option value="<?= e($k) ?>"<?= (string) $val('theme_default') === $k ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                        </select>
                        <?php if (isset($errors['theme_default'])): ?><div class="invalid-feedback"><?= e($errors['theme_default']) ?></div><?php endif; ?>
                        <div class="form-text"><?= e($definitions['theme_default']['help']) ?></div>
                    </div>
                    <?= $checkbox('theme_customers_choose') ?>
                    <div class="mb-3">
                        <label class="form-label" for="theme_default_mode"><?= e($definitions['theme_default_mode']['label']) ?></label>
                        <select class="form-select" id="theme_default_mode" name="theme_default_mode">
                            <?php foreach ($definitions['theme_default_mode']['options'] as $k => $label): ?><option value="<?= e($k) ?>"<?= (string) $val('theme_default_mode') === $k ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                        </select>
                        <div class="form-text"><?= e($definitions['theme_default_mode']['help']) ?></div>
                    </div>
                    <div class="mb-3">
                        <label class="form-label" for="theme_default_accent"><?= e($definitions['theme_default_accent']['label']) ?></label>
                        <select class="form-select<?= isset($errors['theme_default_accent']) ? ' is-invalid' : '' ?>" id="theme_default_accent" name="theme_default_accent">
                            <?php foreach ($definitions['theme_default_accent']['options'] as $k => $label): ?><option value="<?= e($k) ?>"<?= (string) $val('theme_default_accent') === $k ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                        </select>
                        <?php if (isset($errors['theme_default_accent'])): ?><div class="invalid-feedback"><?= e($errors['theme_default_accent']) ?></div><?php endif; ?>
                        <div class="form-text"><?= e($definitions['theme_default_accent']['help']) ?></div>
                    </div>
                </div>
            </section>
            <section class="card mb-3">
                <div class="card-header"><i class="bi bi-palette me-1" aria-hidden="true"></i>Accent colours customers can pick</div>
                <div class="card-body">
                    <?php foreach (Themes::accents() as $k => [$label]): ?>
                        <input type="hidden" name="theme_accent_<?= e($k) ?>" value="0">
                        <div class="form-check form-switch mb-1"><input class="form-check-input" type="checkbox" role="switch" id="theme_accent_<?= e($k) ?>" name="theme_accent_<?= e($k) ?>" value="1"<?= filter_var($val('theme_accent_' . $k), FILTER_VALIDATE_BOOL) ? ' checked' : '' ?>><label class="form-check-label" for="theme_accent_<?= e($k) ?>"><span class="nim-accent-dot nim-accent-<?= e($k) ?> me-1" aria-hidden="true"></span><?= e($label) ?></label></div>
                    <?php endforeach; ?>
                </div>
            </section>
        </div>
    </div>
    <?php if ($canEdit): ?><button class="btn btn-primary" type="submit"><i class="bi bi-check-lg me-1" aria-hidden="true"></i>Save</button><?php endif; ?>
    </fieldset>
</form>
