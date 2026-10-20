<?php
/**
 * Server → MongoDB → Browser: a full-page database browser. A collections
 * sidebar on the left; the chosen collection's documents, with a JSON filter
 * and paging, on the right. Mirrors the SQL web browser (databases/browser).
 *
 * @var \WhaCP\View\View $this
 * @var string $api        the JSON endpoint (mongo.browse)
 * @var string $export     the download endpoint (mongo.export)
 * @var string $back       the MongoDB page URL
 * @var list<string> $databases
 * @var string $current    the database selected on load ('' for none)
 * @var bool $canDelete
 */
?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($back) ?>"><i class="bi bi-arrow-left"></i> MongoDB</a><?php $this->end('back'); ?>
<?php $this->start(); ?><i class="bi bi-table me-1"></i>MongoDB browser<?php $this->end('title'); ?>

<div class="wcp-db" id="wcp-mongo"
     data-api="<?= e($api) ?>"
     data-export="<?= e($export) ?>"
     data-current="<?= e($current) ?>"
     data-can-delete="<?= $canDelete ? '1' : '0' ?>">
    <aside class="wcp-db-side">
        <div class="p-2 border-bottom">
            <label class="form-label small mb-1" for="mb-db">Database</label>
            <select class="form-select form-select-sm" id="mb-db" data-mb-db>
                <option value="">Choose a database…</option>
                <?php foreach ($databases as $name): ?>
                    <option value="<?= e($name) ?>"<?= $name === $current ? ' selected' : '' ?>><?= e($name) ?></option>
                <?php endforeach; ?>
            </select>
        </div>
        <div class="p-2 border-bottom d-flex gap-2">
            <input class="form-control form-control-sm" type="search" placeholder="Filter collections" aria-label="Filter collections" data-mb-coll-filter>
            <?php if ($canDelete): ?>
            <button class="btn btn-sm btn-outline-primary text-nowrap" type="button" data-mb-newcoll disabled title="Create a collection"><i class="bi bi-plus-lg"></i></button>
            <?php endif; ?>
        </div>
        <ul class="list-group list-group-flush" data-mb-colls aria-label="Collections"></ul>
        <div class="small text-body-secondary px-2 py-2" data-mb-colls-count></div>
    </aside>
    <section class="wcp-db-main">
        <div class="wcp-db-tabs">
            <span class="fw-semibold font-monospace text-truncate me-2" data-mb-current>No collection selected</span>
            <div class="btn-group btn-group-sm" role="tablist" aria-label="View">
                <button class="btn btn-outline-secondary active" type="button" data-mb-tab="documents" disabled>Documents</button>
                <button class="btn btn-outline-secondary" type="button" data-mb-tab="aggregate" disabled>Aggregation</button>
                <button class="btn btn-outline-secondary" type="button" data-mb-tab="schema" disabled>Schema</button>
                <button class="btn btn-outline-secondary" type="button" data-mb-tab="indexes" disabled>Indexes</button>
                <button class="btn btn-outline-secondary" type="button" data-mb-tab="users" disabled>Users</button>
            </div>
        </div>
        <div class="wcp-db-bar">
            <span data-mb-doc-tools class="wcp-db-tools">
                <div class="input-group input-group-sm wcp-db-filter">
                    <span class="input-group-text"><i class="bi bi-funnel"></i></span>
                    <input class="form-control font-monospace" type="text" placeholder='Filter, e.g. { "status": "active" }' aria-label="JSON filter" data-mb-filter autocomplete="off" spellcheck="false" disabled>
                    <button class="btn btn-primary" type="button" data-mb-run disabled>Run</button>
                </div>
                <div class="input-group input-group-sm wcp-db-mini">
                    <span class="input-group-text" title="Sort">⇅</span>
                    <input class="form-control font-monospace" type="text" placeholder='Sort, e.g. { "age": -1 }' aria-label="JSON sort" data-mb-sort autocomplete="off" spellcheck="false" disabled>
                </div>
                <div class="input-group input-group-sm wcp-db-mini">
                    <span class="input-group-text" title="Fields (projection)"><i class="bi bi-list-columns-reverse"></i></span>
                    <input class="form-control font-monospace" type="text" placeholder='Fields, e.g. { "name": 1 }' aria-label="JSON projection" data-mb-projection autocomplete="off" spellcheck="false" disabled>
                </div>
                <span class="wcp-db-tools-actions">
                    <?php if ($canDelete): ?>
                    <button class="btn btn-sm btn-outline-primary text-nowrap" type="button" data-mb-new disabled><i class="bi bi-plus-lg me-1"></i>New document</button>
                    <?php endif; ?>
                    <div class="btn-group btn-group-sm">
                        <button class="btn btn-outline-secondary dropdown-toggle" type="button" data-bs-toggle="dropdown" aria-expanded="false" data-mb-export disabled><i class="bi bi-download me-1"></i>Export</button>
                        <ul class="dropdown-menu dropdown-menu-end">
                            <li><button class="dropdown-item" type="button" data-mb-export-format="json">JSON array (.json)</button></li>
                            <li><button class="dropdown-item" type="button" data-mb-export-format="jsonl">One per line (.jsonl)</button></li>
                        </ul>
                    </div>
                    <?php if ($canDelete): ?>
                    <button class="btn btn-sm btn-outline-secondary text-nowrap" type="button" data-mb-import disabled><i class="bi bi-upload me-1"></i>Import</button>
                    <?php endif; ?>
                </span>
            </span>
            <?php if ($canDelete): ?>
            <button class="btn btn-sm btn-outline-primary d-none" type="button" data-mb-newindex disabled><i class="bi bi-plus-lg me-1"></i>New index</button>
            <button class="btn btn-sm btn-outline-primary d-none" type="button" data-mb-newuser disabled><i class="bi bi-person-plus me-1"></i>New user</button>
            <?php endif; ?>
            <div class="wcp-db-paging ms-auto">
                <div class="btn-group btn-group-sm" role="group" aria-label="Paging" data-mb-pager>
                    <button class="btn btn-outline-secondary" type="button" data-mb-prev disabled><i class="bi bi-chevron-left"></i></button>
                    <button class="btn btn-outline-secondary" type="button" data-mb-next disabled><i class="bi bi-chevron-right"></i></button>
                </div>
                <span class="small text-body-secondary text-nowrap" data-mb-range></span>
            </div>
        </div>
        <div class="wcp-db-pane" data-mb-pane aria-live="polite">
            <div class="text-body-secondary p-4">Choose a database, then a collection to browse its documents.</div>
        </div>
    </section>
</div>

<?php if ($canDelete): ?>
<div class="modal fade" id="mb-editor" tabindex="-1" aria-labelledby="mb-editor-title" aria-hidden="true">
    <div class="modal-dialog modal-lg modal-dialog-scrollable">
        <form class="modal-content" data-mb-editor-form novalidate>
            <div class="modal-header"><h2 class="modal-title h6" id="mb-editor-title">Document</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body">
                <label class="form-label small" for="mb-editor-json">Document (JSON / Extended JSON)</label>
                <textarea class="form-control font-monospace" id="mb-editor-json" rows="16" spellcheck="false" data-mb-editor-json></textarea>
                <div class="invalid-feedback" data-mb-editor-err></div>
                <div class="form-text" data-mb-editor-help></div>
            </div>
            <div class="modal-footer">
                <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
                <button type="submit" class="btn btn-primary" data-mb-editor-ok>Save</button>
            </div>
        </form>
    </div>
</div>

<div class="modal fade" id="mb-index" tabindex="-1" aria-labelledby="mb-index-title" aria-hidden="true">
    <div class="modal-dialog modal-dialog-centered">
        <form class="modal-content" data-mb-index-form novalidate>
            <div class="modal-header"><h2 class="modal-title h6" id="mb-index-title">New index</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body">
                <label class="form-label small" for="mb-index-keys">Keys (JSON)</label>
                <input class="form-control font-monospace" id="mb-index-keys" data-mb-index-keys placeholder='{ "email": 1 }' autocomplete="off" spellcheck="false">
                <div class="form-text">1 ascending, -1 descending, or "text". Compound: <code>{ "a": 1, "b": -1 }</code>.</div>
                <div class="row g-2 mt-1">
                    <div class="col-6 form-check ms-2"><input class="form-check-input" type="checkbox" id="mb-index-unique" data-mb-index-unique><label class="form-check-label small" for="mb-index-unique">Unique</label></div>
                    <div class="col-6 form-check"><input class="form-check-input" type="checkbox" id="mb-index-sparse" data-mb-index-sparse><label class="form-check-label small" for="mb-index-sparse">Sparse</label></div>
                </div>
                <div class="row g-2 mt-1">
                    <div class="col-6"><label class="form-label small mb-0" for="mb-index-ttl">TTL (seconds)</label><input class="form-control form-control-sm" type="number" min="0" id="mb-index-ttl" data-mb-index-ttl placeholder="optional"></div>
                    <div class="col-6"><label class="form-label small mb-0" for="mb-index-name">Name</label><input class="form-control form-control-sm" id="mb-index-name" data-mb-index-name placeholder="optional" autocomplete="off"></div>
                </div>
                <div class="invalid-feedback d-block" data-mb-index-err></div>
            </div>
            <div class="modal-footer">
                <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
                <button type="submit" class="btn btn-primary" data-mb-index-ok>Create index</button>
            </div>
        </form>
    </div>
</div>

<div class="modal fade" id="mb-import" tabindex="-1" aria-labelledby="mb-import-title" aria-hidden="true">
    <div class="modal-dialog modal-lg modal-dialog-scrollable">
        <form class="modal-content" data-mb-import-form novalidate>
            <div class="modal-header"><h2 class="modal-title h6" id="mb-import-title">Import documents</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body">
                <div class="row g-2 align-items-end mb-2">
                    <div class="col-sm-6">
                        <label class="form-label small mb-1" for="mb-import-format">Format</label>
                        <select class="form-select form-select-sm" id="mb-import-format" data-mb-import-format>
                            <option value="json">JSON array</option>
                            <option value="jsonl">One document per line (JSONL)</option>
                        </select>
                    </div>
                    <div class="col-sm-6">
                        <label class="form-label small mb-1" for="mb-import-file">Choose a file (optional)</label>
                        <input class="form-control form-control-sm" type="file" id="mb-import-file" accept=".json,.jsonl,.ndjson,application/json,text/plain" data-mb-import-file>
                    </div>
                </div>
                <label class="form-label small" for="mb-import-text">Documents</label>
                <textarea class="form-control font-monospace" id="mb-import-text" rows="14" spellcheck="false" data-mb-import-text placeholder='[ { "name": "a" }, { "name": "b" } ]'></textarea>
                <div class="invalid-feedback d-block" data-mb-import-err></div>
                <div class="form-text">Up to 5000 documents (4&nbsp;MB). Each must be a JSON object; a document whose <code>_id</code> already exists is counted as an error and skipped.</div>
            </div>
            <div class="modal-footer">
                <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
                <button type="submit" class="btn btn-primary" data-mb-import-ok>Import</button>
            </div>
        </form>
    </div>
</div>

<div class="modal fade" id="mb-user" tabindex="-1" aria-labelledby="mb-user-title" aria-hidden="true">
    <div class="modal-dialog modal-dialog-centered">
        <form class="modal-content" data-mb-user-form novalidate>
            <div class="modal-header"><h2 class="modal-title h6" id="mb-user-title">New user</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body">
                <div class="mb-2" data-mb-user-namewrap>
                    <label class="form-label small mb-1" for="mb-user-name">User name</label>
                    <input class="form-control form-control-sm font-monospace" id="mb-user-name" data-mb-user-name autocomplete="off" spellcheck="false">
                </div>
                <div class="mb-2">
                    <label class="form-label small mb-1" for="mb-user-pass">Password</label>
                    <input class="form-control form-control-sm font-monospace" type="text" id="mb-user-pass" data-mb-user-pass autocomplete="off" spellcheck="false">
                    <div class="form-text">8 to 256 characters.</div>
                </div>
                <div class="mb-1" data-mb-user-rolewrap>
                    <label class="form-label small mb-1" for="mb-user-role">Role on this database</label>
                    <select class="form-select form-select-sm" id="mb-user-role" data-mb-user-role>
                        <option value="read">read (read-only)</option>
                        <option value="readWrite" selected>readWrite</option>
                        <option value="dbAdmin">dbAdmin</option>
                        <option value="dbOwner">dbOwner</option>
                    </select>
                </div>
                <div class="invalid-feedback d-block" data-mb-user-err></div>
            </div>
            <div class="modal-footer">
                <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
                <button type="submit" class="btn btn-primary" data-mb-user-ok>Save</button>
            </div>
        </form>
    </div>
</div>
<?php endif; ?>

<?php $this->start(); ?><script src="<?= e($this->asset('assets/js/mongobrowser.js')) ?>" defer></script><?php $this->end('scripts'); ?>
