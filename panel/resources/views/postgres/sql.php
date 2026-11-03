<?php
/**
 * PostgreSQL SQL editor for one database: CodeMirror editor, results, history. The page script posts to
 * postgres.sql_run; nothing here touches the database.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 * @var array<string, list<string>> $schema
 * @var list<array<string, mixed>> $history
 * @var bool $canWrite
 */
$id = (int) $db['id'];
?>
<?php $this->start(); ?><?= e((string) $db['name']) ?> · SQL<?php $this->end('title'); ?>
<?php $this->start(); ?>PostgreSQL SQL editor<?php $this->end('subtitle'); ?>

<p class="mb-2 d-flex gap-3"><a href="<?= e($this->url('postgres')) ?>" class="small"><i class="bi bi-arrow-left"></i> PostgreSQL</a><a href="<?= e($this->url('postgres.browse') . '?db=' . $id) ?>" class="small"><i class="bi bi-table"></i> Browse tables</a></p>

<?php if (!empty($schemaError)): ?>
    <div class="alert alert-warning small">The table names for completion could not be read (<?= e($schemaError->getMessage()) ?>). You can still run statements.</div>
<?php endif; ?>

<div id="pgsql" data-run="<?= e($this->url('postgres.sql_run')) ?>" data-cancel="<?= e($this->url('postgres.sql_cancel')) ?>" data-db="<?= $id ?>" data-name="<?= e((string) $db['name']) ?>" data-can-write="<?= $canWrite ? '1' : '0' ?>" data-schema="<?= e((string) json_encode($schema, JSON_FORCE_OBJECT)) ?>" data-history="<?= e((string) json_encode($history)) ?>">
    <div class="card mb-3">
        <div class="card-header d-flex flex-wrap align-items-center gap-2 py-2">
            <div class="btn-group btn-group-sm" role="group" aria-label="Mode">
                <input type="radio" class="btn-check" name="pgsql-mode" id="pgsql-read" value="read" checked><label class="btn btn-outline-secondary" for="pgsql-read" title="Can only read: enforced by PostgreSQL privileges"><i class="bi bi-eye me-1"></i>Read only</label>
                <?php if ($canWrite): ?>
                    <input type="radio" class="btn-check" name="pgsql-mode" id="pgsql-write" value="write"><label class="btn btn-outline-warning" for="pgsql-write" title="Runs as the database owner and can change data and structure"><i class="bi bi-pencil me-1"></i>Read and write</label>
                <?php endif; ?>
            </div>
            <button type="button" class="btn btn-sm btn-primary" data-pgsql="run" title="Run the selected text, or everything (Ctrl+Enter)"><i class="bi bi-play-fill me-1"></i>Run</button>
            <button type="button" class="btn btn-sm btn-outline-danger d-none" data-pgsql="cancel"><i class="bi bi-stop-fill me-1"></i>Cancel</button>
            <div class="btn-group btn-group-sm">
                <button type="button" class="btn btn-outline-secondary" data-pgsql="explain" title="Show the query plan without running the statement">Explain</button>
                <button type="button" class="btn btn-outline-secondary" data-pgsql="analyze" title="Runs the statement and shows the real timings">Explain analyze</button>
            </div>
            <button type="button" class="btn btn-sm btn-outline-secondary" data-pgsql="format">Format</button>
            <span class="me-auto"></span>
            <label class="form-check form-check-inline small mb-0" title="Several statements; otherwise exactly one is run"><input class="form-check-input" type="checkbox" id="pgsql-script"> <span class="form-check-label">Script</span></label>
            <select class="form-select form-select-sm w-auto" id="pgsql-limit" aria-label="Rows to keep"><option value="100">100 rows</option><option value="500" selected>500 rows</option><option value="1000">1,000 rows</option></select>
            <select class="form-select form-select-sm w-auto" id="pgsql-timeout" aria-label="Time limit"><option value="5">5 s</option><option value="15" selected>15 s</option><option value="30">30 s</option><option value="60">60 s</option></select>
        </div>
        <div id="pgsql-editor" style="height:14rem;resize:vertical;overflow:auto"></div>
        <div class="card-footer small text-body-secondary py-1">Ctrl+Enter runs the selection (or everything). Read only mode is enforced by PostgreSQL; write mode runs as the database owner.</div>
    </div>

    <div id="pgsql-status" class="small mb-2" aria-live="polite"></div>
    <div id="pgsql-error" class="alert alert-danger small d-none"></div>

    <div class="card mb-3 d-none" id="pgsql-results">
        <div class="card-header d-flex flex-wrap align-items-center gap-2 py-2">
            <ul class="nav nav-pills small" id="pgsql-tabs"></ul>
            <span class="me-auto"></span>
            <span class="small text-body-secondary" id="pgsql-pageinfo"></span>
            <div class="btn-group btn-group-sm"><button type="button" class="btn btn-outline-secondary" data-pgsql="prev">Previous</button><button type="button" class="btn btn-outline-secondary" data-pgsql="next">Next</button></div>
            <button type="button" class="btn btn-sm btn-outline-secondary" data-pgsql="csv"><i class="bi bi-filetype-csv me-1"></i>Export CSV</button>
        </div>
        <div class="table-responsive"><table class="table table-sm align-middle mb-0 small" id="pgsql-grid"><thead></thead><tbody></tbody></table></div>
    </div>

    <div class="row g-3">
        <div class="col-md-6"><div class="card"><div class="card-header small fw-medium">Recent in this browser tab</div><div class="list-group list-group-flush small" id="pgsql-recent"><div class="list-group-item text-body-secondary">Nothing yet.</div></div></div></div>
        <div class="col-md-6"><div class="card"><div class="card-header small fw-medium">History <span class="text-body-secondary fw-normal">(the shape of each statement, never its values)</span></div><div class="list-group list-group-flush small" id="pgsql-history"></div></div></div>
    </div>
</div>
<?php $this->start(); ?><script src="<?= e($this->asset('assets/vendor/codemirror/sql-editor.min.js')) ?>" defer></script><script src="<?= e($this->asset('assets/js/postgres-sql.js')) ?>" defer></script><?php $this->end('scripts'); ?>
