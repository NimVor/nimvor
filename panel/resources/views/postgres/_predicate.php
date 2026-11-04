<?php
/**
 * A column / operator / value condition (a partial index or a CHECK constraint), as form fields.
 *
 * @var list<string> $columns
 * @var string $legend
 */
$ops = ['eq' => '=', 'ne' => '<>', 'lt' => '<', 'le' => '<=', 'gt' => '>', 'ge' => '>=', 'null' => 'is null', 'not_null' => 'is not null', 'like' => 'like', 'between' => 'between', 'in' => 'in (a, b, ...)'];
?>
<div class="row g-1 align-items-center mb-1">
    <div class="col-12 small text-body-secondary"><?= e($legend) ?></div>
    <div class="col-6 col-md-3"><select class="form-select form-select-sm" name="pcol" aria-label="Condition column"><option value="">column…</option><?php foreach ($columns as $c): ?><option><?= e((string) $c) ?></option><?php endforeach; ?></select></div>
    <div class="col-6 col-md-2"><select class="form-select form-select-sm" name="pop" aria-label="Condition operator"><option value="">operator…</option><?php foreach ($ops as $k => $v): ?><option value="<?= e($k) ?>"><?= e($v) ?></option><?php endforeach; ?></select></div>
    <div class="col-6 col-md-3"><input class="form-control form-control-sm" name="pval" placeholder="value" maxlength="200" aria-label="Condition value"></div>
    <div class="col-6 col-md-3"><input class="form-control form-control-sm" name="pval2" placeholder="upper value (between)" maxlength="200" aria-label="Condition upper value"></div>
</div>
