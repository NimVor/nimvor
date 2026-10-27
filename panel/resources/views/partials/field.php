<?php
/**
 * Form field partial.
 * @var string $name
 * @var string $label
 * @var string $type
 * @var string $value
 * @var string|null $help
 * @var array<string, string> $attrs
 */
$err = $errors[$name] ?? null;
$id = ($idPrefix ?? 'f-') . $name;
$attrs ??= [];
?>
<div class="mb-3">
    <label class="form-label" for="<?= e($id) ?>"><?= e($label) ?></label>
    <input class="form-control<?= $err ? ' is-invalid' : '' ?>" id="<?= e($id) ?>" name="<?= e($name) ?>" type="<?= e($type ?? 'text') ?>"
           value="<?= e($value ?? '') ?>"<?php foreach ($attrs as $k => $v): ?> <?= e($k) ?>="<?= e($v) ?>"<?php endforeach; ?><?= $err ? ' aria-describedby="' . e($id) . '-err"' : '' ?>>
    <?php if ($err): ?><div class="invalid-feedback" id="<?= e($id) ?>-err"><?= e($err) ?></div><?php endif; ?>
    <?php if (!empty($help)): ?><div class="form-text"><?= e($help) ?></div><?php endif; ?>
</div>
