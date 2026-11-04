<?php
/**
 * The dialog of the Objects list: a new table. See _dialog_shell.php and postgres-dialogs.js.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 * @var list<string> $schemas
 */
?>
<?= $this->partial('postgres/_dialog_shell', ['db' => $db, 'schema' => '', 'table' => '']) ?>
<?= $this->partial('postgres/_types', []) ?>
<template data-pgd-tpl="new_table" data-title="New table" data-ok="Create table" data-op="create_table" data-action="<?= e($this->url('postgres.alter')) ?>" data-preview="<?= e($this->url('postgres.alter_preview')) ?>">
    <div class="row g-2 mb-2">
        <div class="col-12 col-md-6"><label class="form-label small mb-0" for="pgd-nt-name">Table name</label><input class="form-control form-control-sm" id="pgd-nt-name" name="table" maxlength="63" autocomplete="off"></div>
        <div class="col-12 col-md-6"><label class="form-label small mb-0" for="pgd-nt-schema">Schema</label><select class="form-select form-select-sm" id="pgd-nt-schema" name="schema"><?php foreach ($schemas ?: ['public'] as $sc): ?><option value="<?= e($sc) ?>"><?= e($sc) ?></option><?php endforeach; ?></select></div>
    </div>
    <p class="small text-body-secondary mb-1">Describe up to six columns here; add more afterwards on the table's Structure tab. Rows with no name are ignored.</p>
    <?php for ($i = 0; $i < 6; $i++): ?><?= $this->partial('postgres/_column_fields', ['prefix' => 'cols[' . $i . ']', 'primary' => true]) ?><?php endfor; ?>
</template>
