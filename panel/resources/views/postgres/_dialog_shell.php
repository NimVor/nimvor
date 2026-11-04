<?php
/**
 * The one modal every PostgreSQL create / change form opens in, like the MySQL browser's dialogs. The form's fields come
 * from a <template data-pgd-tpl="..."> on the page (see _dialogs_*.php); public/assets/js/postgres-dialogs.js fills it
 * in, shows the SQL a form would run ("Show SQL", from a dry run on the agent) and lets the normal POST do the change.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 * @var string $schema
 * @var string $table
 */
?>
<div class="modal fade" id="pg-dialog" tabindex="-1" aria-labelledby="pg-dialog-title" aria-hidden="true">
    <div class="modal-dialog modal-lg modal-dialog-scrollable">
        <form class="modal-content" method="post" data-pgd-form data-nim-busy novalidate>
            <?= $this->csrfField() ?>
            <input type="hidden" name="db" value="<?= (int) $db['id'] ?>"><input type="hidden" name="schema" value="<?= e($schema) ?>"><input type="hidden" name="table" value="<?= e($table) ?>">
            <input type="hidden" name="op" data-pgd-op>
            <div class="modal-header"><h2 class="modal-title h6" id="pg-dialog-title" data-pgd-title></h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body" data-pgd-body></div>
            <div class="modal-footer flex-wrap">
                <pre class="nim-db-create small w-100 mb-2" data-pgd-sql></pre>
                <div class="invalid-feedback d-block w-100 mb-2" data-pgd-err></div>
                <button type="button" class="btn btn-outline-secondary me-auto d-none" data-pgd-preview><i class="bi bi-code-slash me-1"></i>Show SQL</button>
                <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
                <button type="submit" class="btn btn-primary" data-pgd-ok>Save</button>
            </div>
        </form>
    </div>
</div>
