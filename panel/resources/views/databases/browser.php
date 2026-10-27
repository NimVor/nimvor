<?php
/**
 * @var \NimVor\View\View $this
 * @var string $api
 * @var string $database
 * @var string $back
 */
?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($back) ?>"><i class="bi bi-arrow-left"></i> Databases</a><?php $this->end('back'); ?>
<?php $this->start(); ?><i class="bi bi-table me-1"></i>Database <span class="font-monospace"><?= e($database) ?></span><?php $this->end('title'); ?>
<?php $this->start(); ?><span class="small text-body-secondary d-none d-md-inline" data-db-server></span><?php $this->end('actions'); ?>

<div class="nim-db" id="nim-db" data-api="<?= e($api) ?>" data-database="<?= e($database) ?>">
    <aside class="nim-db-side">
        <div class="p-2 border-bottom d-flex gap-1"><input class="form-control form-control-sm" type="search" placeholder="Filter tables" aria-label="Filter tables" data-db-table-filter><button type="button" class="btn btn-sm btn-outline-primary text-nowrap" data-db-new-table title="Create a table"><i class="bi bi-plus-lg"></i><span class="visually-hidden">New table</span></button></div>
        <ul class="list-group list-group-flush" data-db-tables aria-label="Tables"></ul>
        <div class="small text-body-secondary px-2 py-2" data-db-tables-count></div>
    </aside>
    <section class="nim-db-main">
        <nav class="nim-db-tabs" role="tablist">
            <button type="button" class="btn btn-sm" role="tab" data-db-tab="browse" disabled><i class="bi bi-grid-3x3"></i> Browse</button>
            <button type="button" class="btn btn-sm" role="tab" data-db-tab="structure" disabled><i class="bi bi-diagram-3"></i> Structure</button>
            <button type="button" class="btn btn-sm" role="tab" data-db-tab="insert" disabled><i class="bi bi-plus-lg"></i> Insert</button>
            <button type="button" class="btn btn-sm" role="tab" data-db-tab="sql"><i class="bi bi-terminal"></i> SQL</button>
            <span class="ms-auto small text-body-secondary font-monospace text-truncate" data-db-current></span>
        </nav>
        <div class="nim-db-pane" data-db-pane aria-live="polite">
            <div class="text-body-secondary p-4">Choose a table on the left, or open the SQL tab.</div>
        </div>
    </section>
</div>

<div class="modal fade" id="db-dialog" tabindex="-1" aria-labelledby="db-dialog-title" aria-hidden="true">
    <div class="modal-dialog modal-dialog-centered">
        <form class="modal-content" data-db-dialog-form novalidate>
            <div class="modal-header"><h2 class="modal-title h6" id="db-dialog-title"></h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body" data-db-dialog-body></div>
            <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button type="submit" class="btn btn-danger" data-db-dialog-ok>OK</button></div>
        </form>
    </div>
</div>
<div class="modal fade" id="db-form" tabindex="-1" aria-labelledby="db-form-title" aria-hidden="true">
    <div class="modal-dialog modal-lg modal-dialog-scrollable">
        <form class="modal-content" data-db-form novalidate>
            <div class="modal-header"><h2 class="modal-title h6" id="db-form-title"></h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body" data-db-form-body></div>
            <div class="modal-footer flex-wrap">
                <pre class="nim-db-create small w-100 mb-2" data-db-form-sql></pre>
                <button type="button" class="btn btn-outline-secondary me-auto" data-db-form-preview><i class="bi bi-code-slash me-1"></i>Show SQL</button>
                <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
                <button type="submit" class="btn btn-primary" data-db-form-ok>Save</button>
            </div>
        </form>
    </div>
</div>
<?php $this->start(); ?><script src="<?= e($this->asset('assets/js/dbbrowser.js')) ?>" defer></script><?php $this->end('scripts'); ?>
