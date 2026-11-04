<?php
/**
 * The dialog of the Routines section: drop one. See _dialog_shell.php and postgres-dialogs.js.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 */
?>
<?= $this->partial('postgres/_dialog_shell', ['db' => $db, 'schema' => '', 'table' => '']) ?>
<template data-pgd-tpl="routine_drop" data-title="Drop a routine" data-ok="Drop routine" data-op="" data-danger="1" data-action="<?= e($this->url('postgres.routine_drop')) ?>">
    <input type="hidden" name="oid"><input type="hidden" name="label">
    <p>Drop <strong class="font-monospace" data-pgd-fill="label"></strong>? Anything that calls it will fail. This cannot be undone.</p>
    <label class="form-label small" for="pgd-rd">Type the database name (<span class="font-monospace" data-pgd-fill="word"></span>) to confirm</label><input class="form-control font-monospace" id="pgd-rd" name="confirm" autocomplete="off" data-pgd-confirm="word">
    <label class="form-check mt-2"><input class="form-check-input" type="checkbox" name="cascade" value="1"> also drop dependent objects (CASCADE)</label>
</template>
