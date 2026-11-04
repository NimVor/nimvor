<?php
/**
 * The fields that describe one column, shared by "new table" and "add column". $prefix is the field-name prefix
 * ("cols[0]" or "col"); $primary shows the primary-key box.
 *
 * @var \NimVor\View\View $this
 * @var string $prefix
 * @var bool $primary
 */
?>
<div class="row g-1 align-items-center mb-1">
    <div class="col-12 col-md-3"><input class="form-control form-control-sm" name="<?= e($prefix) ?>[name]" placeholder="name" maxlength="63" aria-label="Column name"></div>
    <div class="col-6 col-md-2"><input class="form-control form-control-sm" name="<?= e($prefix) ?>[type]" list="pg-types" placeholder="type" maxlength="40" aria-label="Column type"></div>
    <div class="col-6 col-md-2">
        <select class="form-select form-select-sm" name="<?= e($prefix) ?>[default_kind]" aria-label="Default value">
            <option value="none">no default</option><option value="literal">default value</option><option value="function">default function</option>
        </select>
    </div>
    <div class="col-12 col-md-2"><input class="form-control form-control-sm" name="<?= e($prefix) ?>[default]" placeholder="value, or now / current_date" maxlength="200" aria-label="Default"></div>
    <div class="col-12 col-md-3 d-flex flex-wrap gap-2 small">
        <label class="form-check mb-0"><input class="form-check-input" type="checkbox" name="<?= e($prefix) ?>[not_null]" value="1"> not null</label>
        <label class="form-check mb-0"><input class="form-check-input" type="checkbox" name="<?= e($prefix) ?>[identity]" value="1"> auto-number</label>
        <?php if ($primary): ?><label class="form-check mb-0"><input class="form-check-input" type="checkbox" name="<?= e($prefix) ?>[primary]" value="1"> primary key</label><?php endif; ?>
    </div>
</div>
