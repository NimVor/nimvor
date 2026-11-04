<?php
/**
 * The dialogs of the Sequences section. See _dialog_shell.php and postgres-dialogs.js.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 */
$act = $this->url('postgres.sequence_alter');
$prv = $this->url('postgres.sequence_preview');
?>
<?= $this->partial('postgres/_dialog_shell', ['db' => $db, 'schema' => '', 'table' => '']) ?>
<template data-pgd-tpl="seq_create" data-title="New sequence" data-ok="Create" data-op="create" data-action="<?= e($act) ?>" data-preview="<?= e($prv) ?>">
    <div class="row g-2">
        <div class="col-6 col-md-3"><label class="form-label small mb-0" for="pgd-sq-s">Schema</label><input class="form-control form-control-sm" id="pgd-sq-s" name="schema" value="public" maxlength="63"></div>
        <div class="col-6 col-md-5"><label class="form-label small mb-0" for="pgd-sq-n">Name</label><input class="form-control form-control-sm" id="pgd-sq-n" name="name" maxlength="63" autocomplete="off"></div>
        <div class="col-6 col-md-2"><label class="form-label small mb-0" for="pgd-sq-st">Start</label><input class="form-control form-control-sm" id="pgd-sq-st" name="start"></div>
        <div class="col-6 col-md-2"><label class="form-label small mb-0" for="pgd-sq-i">Step</label><input class="form-control form-control-sm" id="pgd-sq-i" name="increment"></div>
    </div>
</template>
<template data-pgd-tpl="seq_set" data-title="Set the value" data-ok="Set value" data-op="set_value" data-action="<?= e($act) ?>" data-preview="<?= e($prv) ?>">
    <input type="hidden" name="schema"><input type="hidden" name="name">
    <label class="form-label small" for="pgd-ss">Last value used by <span class="font-monospace" data-pgd-fill="name"></span> (the next call returns the one after it)</label><input class="form-control" id="pgd-ss" name="value" autocomplete="off">
</template>
<template data-pgd-tpl="seq_restart" data-title="Restart the sequence" data-ok="Restart" data-op="restart" data-action="<?= e($act) ?>" data-preview="<?= e($prv) ?>">
    <input type="hidden" name="schema"><input type="hidden" name="name">
    <label class="form-label small" for="pgd-sr">Next value <span class="font-monospace" data-pgd-fill="name"></span> should return</label><input class="form-control" id="pgd-sr" name="value" autocomplete="off">
</template>
<template data-pgd-tpl="seq_alter" data-title="Change the settings" data-ok="Apply" data-op="alter" data-action="<?= e($act) ?>" data-preview="<?= e($prv) ?>">
    <input type="hidden" name="schema"><input type="hidden" name="name">
    <p class="small text-body-secondary">Sequence <span class="font-monospace" data-pgd-fill="name"></span>. Leave a field empty to keep it.</p>
    <div class="row g-2">
        <div class="col-6 col-md-3"><label class="form-label small mb-0" for="pgd-sa-i">Step</label><input class="form-control form-control-sm" id="pgd-sa-i" name="increment"></div>
        <div class="col-6 col-md-3"><label class="form-label small mb-0" for="pgd-sa-mn">Minimum</label><input class="form-control form-control-sm" id="pgd-sa-mn" name="min"></div>
        <div class="col-6 col-md-3"><label class="form-label small mb-0" for="pgd-sa-mx">Maximum</label><input class="form-control form-control-sm" id="pgd-sa-mx" name="max"></div>
        <div class="col-6 col-md-3"><label class="form-label small mb-0" for="pgd-sa-c">Cache</label><input class="form-control form-control-sm" id="pgd-sa-c" name="cache"></div>
        <div class="col-12 col-md-4"><label class="form-label small mb-0" for="pgd-sa-cy">Cycle</label><select class="form-select form-select-sm" id="pgd-sa-cy" name="cycle"><option value="">keep</option><option value="yes">cycle</option><option value="no">no cycle</option></select></div>
    </div>
</template>
<template data-pgd-tpl="seq_sync" data-title="Follow the column again" data-ok="Follow column" data-op="sync" data-action="<?= e($act) ?>" data-preview="<?= e($prv) ?>">
    <input type="hidden" name="schema"><input type="hidden" name="name">
    <p>The next value of <strong class="font-monospace" data-pgd-fill="name"></strong> becomes the highest value in <span class="font-monospace" data-pgd-fill="owned_by"></span> plus one.</p>
</template>
<template data-pgd-tpl="seq_drop" data-title="Drop a sequence" data-ok="Drop sequence" data-op="drop" data-danger="1" data-action="<?= e($act) ?>" data-preview="<?= e($prv) ?>">
    <input type="hidden" name="schema"><input type="hidden" name="name">
    <p>Drop the sequence <strong class="font-monospace" data-pgd-fill="name"></strong>? Tables that use it as a default will fail until you change them.</p>
    <label class="form-label small" for="pgd-sd">Type the sequence name to confirm</label><input class="form-control font-monospace" id="pgd-sd" name="confirm" autocomplete="off" data-pgd-confirm="name">
    <label class="form-check mt-2"><input class="form-check-input" type="checkbox" name="cascade" value="1"> also drop what depends on it (CASCADE)</label>
</template>
