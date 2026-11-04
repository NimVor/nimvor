<?php
/**
 * Server → MongoDB → Browser: a full-page, three-pane database browser.
 * Left: the databases selector and a collections list. Centre: a header with
 * the breadcrumb and collection actions, a query bar, the view tabs and the
 * documents table (or JSON view) with paging. Right: the selected document,
 * the collection's indexes, its statistics and quick actions.
 *
 * The same view backs the per-website browser (SiteMongoController::browser);
 * $canDelete is false for a read-only viewer.
 *
 * @var \NimVor\View\View $this
 * @var string $api        the JSON endpoint (mongo.browse)
 * @var string $export     the download endpoint (mongo.export)
 * @var string $back       the MongoDB page URL
 * @var list<string> $databases
 * @var string $current    the database selected on load ('' for none)
 * @var bool $canDelete
 */
?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($back) ?>"><i class="bi bi-arrow-left"></i> MongoDB</a><?php $this->end('back'); ?>
<?php $this->start(); ?><i class="bi bi-hdd-network me-1"></i>MongoDB browser<?php $this->end('title'); ?>

<div class="nim-mongo3" id="nim-mongo"
     data-api="<?= e($api) ?>"
     data-export="<?= e($export) ?>"
     data-current="<?= e($current) ?>"
     data-can-delete="<?= $canDelete ? '1' : '0' ?>">

    <!-- Left: databases + collections -->
    <aside class="nim-mongo-side">
        <div class="nim-mongo-side-head"><i class="bi bi-hdd-stack me-1" aria-hidden="true"></i>Databases</div>
        <div class="p-2 border-bottom">
            <select class="form-select form-select-sm" id="mb-db" aria-label="Database" data-mb-db>
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
        <ul class="list-group list-group-flush nim-mongo-colls" data-mb-colls aria-label="Collections"></ul>
        <div class="small text-body-secondary px-2 py-2" data-mb-colls-count></div>
    </aside>

    <!-- Centre: header, query, tabs, documents -->
    <section class="nim-mongo-center">
        <div class="nim-mongo-head">
            <nav class="nim-mongo-bc small text-body-secondary" aria-label="Breadcrumb">
                <span data-mb-bc-db>—</span>
                <i class="bi bi-chevron-right mx-1" aria-hidden="true"></i>
                <span>Collections</span>
                <i class="bi bi-chevron-right mx-1" aria-hidden="true"></i>
                <span class="text-body-emphasis" data-mb-bc-coll>—</span>
            </nav>
            <div class="nim-mongo-title">
                <h1 class="h4 mb-0 font-monospace text-truncate" data-mb-title>No collection selected</h1>
                <span class="badge rounded-pill text-bg-light border" data-mb-kind hidden>Collection</span>
                <div class="nim-mongo-actions ms-auto">
                    <?php if ($canDelete): ?>
                    <button class="btn btn-sm btn-primary" type="button" data-mb-new disabled><i class="bi bi-plus-lg me-1"></i>Insert Document</button>
                    <button class="btn btn-sm btn-outline-secondary" type="button" data-mb-import disabled><i class="bi bi-upload me-1"></i>Import</button>
                    <?php endif; ?>
                    <div class="btn-group btn-group-sm">
                        <button class="btn btn-outline-secondary dropdown-toggle" type="button" data-bs-toggle="dropdown" aria-expanded="false" data-mb-export disabled><i class="bi bi-download me-1"></i>Export</button>
                        <ul class="dropdown-menu dropdown-menu-end">
                            <li><button class="dropdown-item" type="button" data-mb-export-format="json">JSON array (.json)</button></li>
                            <li><button class="dropdown-item" type="button" data-mb-export-format="jsonl">One per line (.jsonl)</button></li>
                        </ul>
                    </div>
                    <?php if ($canDelete): ?>
                    <div class="btn-group btn-group-sm">
                        <button class="btn btn-outline-secondary" type="button" data-bs-toggle="dropdown" aria-expanded="false" data-mb-more disabled aria-label="More actions"><i class="bi bi-three-dots"></i></button>
                        <ul class="dropdown-menu dropdown-menu-end">
                            <li><button class="dropdown-item" type="button" data-mb-coll-op="rename"><i class="bi bi-pencil me-2"></i>Rename collection…</button></li>
                            <li><button class="dropdown-item" type="button" data-mb-coll-op="empty"><i class="bi bi-eraser me-2"></i>Empty collection…</button></li>
                            <li><hr class="dropdown-divider"></li>
                            <li><button class="dropdown-item text-danger" type="button" data-mb-coll-op="drop"><i class="bi bi-trash me-2"></i>Drop collection…</button></li>
                        </ul>
                    </div>
                    <?php endif; ?>
                </div>
            </div>
        </div>

        <div class="nim-mongo-query">
            <div class="input-group input-group-sm nim-mongo-queryin">
                <span class="input-group-text"><i class="bi bi-funnel"></i></span>
                <input class="form-control font-monospace" type="text" placeholder='Enter query (JSON), e.g. { "status": "active" }' aria-label="JSON filter" data-mb-filter autocomplete="off" spellcheck="false" disabled>
                <button class="btn btn-primary" type="button" data-mb-run disabled><i class="bi bi-play-fill me-1"></i>Run</button>
            </div>
            <div class="nim-mongo-limit">
                <label class="form-label small mb-0 me-1" for="mb-limit">Show</label>
                <select class="form-select form-select-sm" id="mb-limit" data-mb-pagesize aria-label="Documents per page">
                    <option>10</option>
                    <option selected>25</option>
                    <option>50</option>
                    <option>100</option>
                </select>
            </div>
        </div>

        <ul class="nav nav-tabs nim-tabs nim-mongo-tabs" role="tablist">
            <?php foreach ([
                'documents' => ['Documents', 'grid-3x3'],
                'aggregate' => ['Aggregation', 'diagram-3'],
                'indexes' => ['Indexes', 'key'],
                'schema' => ['Schema', 'diagram-2'],
                'validation' => ['Validation', 'shield-check'],
                'stats' => ['Stats', 'bar-chart'],
                'users' => ['Users', 'people'],
            ] as $key => $t): ?>
                <li class="nav-item" role="presentation"><button class="nav-link<?= $key === 'documents' ? ' active' : '' ?>" type="button" role="tab" data-mb-tab="<?= e($key) ?>" disabled><i class="bi bi-<?= e($t[1]) ?> me-1" aria-hidden="true"></i><?= e($t[0]) ?></button></li>
            <?php endforeach; ?>
        </ul>

        <div class="nim-mongo-toolbar d-none" data-mb-toolbar>
            <span class="small text-body-secondary text-nowrap" data-mb-count></span>
            <div class="nim-mongo-toolbar-end">
                <span class="nim-mongo-docfilters" data-mb-docfilters>
                    <div class="input-group input-group-sm nim-mongo-mini">
                        <span class="input-group-text" title="Sort">⇅</span>
                        <input class="form-control font-monospace" type="text" placeholder='{ "age": -1 }' aria-label="JSON sort" title="Sort" data-mb-sort autocomplete="off" spellcheck="false" disabled>
                    </div>
                    <div class="input-group input-group-sm nim-mongo-mini">
                        <span class="input-group-text" title="Fields (projection)"><i class="bi bi-list-columns-reverse"></i></span>
                        <input class="form-control font-monospace" type="text" placeholder='{ "name": 1 }' aria-label="JSON projection" title="Fields (projection)" data-mb-projection autocomplete="off" spellcheck="false" disabled>
                    </div>
                </span>
                <?php if ($canDelete): ?>
                <button class="btn btn-sm btn-outline-primary d-none" type="button" data-mb-newindex disabled><i class="bi bi-plus-lg me-1"></i>New index</button>
                <button class="btn btn-sm btn-outline-primary d-none" type="button" data-mb-newuser disabled><i class="bi bi-person-plus me-1"></i>New user</button>
                <?php endif; ?>
                <div class="btn-group btn-group-sm" role="group" aria-label="View" data-mb-viewtoggle>
                    <button class="btn btn-outline-secondary active" type="button" data-mb-view="table" title="Table view"><i class="bi bi-table"></i></button>
                    <button class="btn btn-outline-secondary" type="button" data-mb-view="json" title="JSON view"><i class="bi bi-braces"></i></button>
                </div>
            </div>
        </div>

        <div class="nim-mongo-pane" data-mb-pane aria-live="polite">
            <div class="nim-db-empty"><i class="bi bi-hdd-stack"></i><div class="fw-semibold">Choose a database, then a collection</div><div class="small text-body-secondary">Pick one on the left to browse its documents.</div></div>
        </div>

        <div class="nim-mongo-pager" data-mb-pager hidden>
            <div class="btn-group btn-group-sm" role="group" aria-label="Pages" data-mb-pages></div>
            <span class="small text-body-secondary ms-auto text-nowrap" data-mb-range></span>
        </div>
    </section>

    <!-- Right: document preview, indexes, stats, quick actions -->
    <aside class="nim-mongo-rail">
        <div class="nim-mongo-card">
            <div class="nim-mongo-card-head"><i class="bi bi-file-earmark-text me-1"></i>Document
                <button class="btn btn-sm btn-outline-secondary ms-auto py-0" type="button" data-mb-doc-copy disabled><i class="bi bi-clipboard me-1"></i>Copy</button>
            </div>
            <pre class="nim-mongo-rail-json" data-mb-doc-json><span class="text-body-secondary">Select a document to preview it here.</span></pre>
        </div>
        <div class="nim-mongo-card">
            <div class="nim-mongo-card-head"><i class="bi bi-key me-1"></i>Indexes
                <button class="btn btn-sm btn-link ms-auto p-0 text-decoration-none" type="button" data-mb-rail-indexes-all>View all</button>
            </div>
            <div class="nim-mongo-rail-body" data-mb-rail-indexes><span class="small text-body-secondary">—</span></div>
        </div>
        <div class="nim-mongo-card">
            <div class="nim-mongo-card-head"><i class="bi bi-bar-chart me-1"></i>Collection stats</div>
            <div class="nim-mongo-rail-body" data-mb-rail-stats><span class="small text-body-secondary">—</span></div>
        </div>
        <div class="nim-mongo-card">
            <div class="nim-mongo-card-head"><i class="bi bi-lightning-charge me-1"></i>Quick actions</div>
            <div class="list-group list-group-flush nim-mongo-qa">
                <?php if ($canDelete): ?>
                <button class="list-group-item list-group-item-action d-flex align-items-center" type="button" data-mb-qa="index" disabled><i class="bi bi-plus-square me-2"></i>Create index<i class="bi bi-chevron-right ms-auto"></i></button>
                <?php endif; ?>
                <button class="list-group-item list-group-item-action d-flex align-items-center" type="button" data-mb-qa="validate" disabled><i class="bi bi-shield-check me-2"></i>Validation rules<i class="bi bi-chevron-right ms-auto"></i></button>
                <button class="list-group-item list-group-item-action d-flex align-items-center" type="button" data-mb-qa="schema" disabled><i class="bi bi-diagram-2 me-2"></i>View schema<i class="bi bi-chevron-right ms-auto"></i></button>
            </div>
        </div>
    </aside>
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

<div class="modal fade" id="mb-newcoll" tabindex="-1" aria-labelledby="mb-newcoll-title" aria-hidden="true">
    <div class="modal-dialog modal-dialog-centered">
        <form class="modal-content" data-mb-newcoll-form novalidate>
            <div class="modal-header"><h2 class="modal-title h6" id="mb-newcoll-title">New collection</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body">
                <label class="form-label small" for="mb-nc-name">Name</label>
                <input class="form-control font-monospace mb-2" id="mb-nc-name" data-mb-nc-name maxlength="120" autocomplete="off" spellcheck="false">
                <label class="form-label small" for="mb-nc-kind">Type</label>
                <select class="form-select form-select-sm mb-2" id="mb-nc-kind" data-mb-nc-kind>
                    <option value="collection">Collection</option>
                    <option value="capped">Capped collection (fixed size, oldest documents are replaced)</option>
                    <option value="timeseries">Time-series collection (MongoDB 5.0 or newer)</option>
                    <option value="view">View (read-only, from an aggregation pipeline)</option>
                </select>
                <div class="row g-2 d-none" data-mb-nc-capped>
                    <div class="col-6"><label class="form-label small mb-0" for="mb-nc-size">Size (bytes)</label><input class="form-control form-control-sm" type="number" min="4096" id="mb-nc-size" data-mb-nc-size value="1048576"></div>
                    <div class="col-6"><label class="form-label small mb-0" for="mb-nc-max">Max documents</label><input class="form-control form-control-sm" type="number" min="0" id="mb-nc-max" data-mb-nc-max placeholder="optional"></div>
                </div>
                <div class="row g-2 d-none" data-mb-nc-ts>
                    <div class="col-6"><label class="form-label small mb-0" for="mb-nc-time">Time field</label><input class="form-control form-control-sm" id="mb-nc-time" data-mb-nc-time placeholder="timestamp" autocomplete="off"></div>
                    <div class="col-6"><label class="form-label small mb-0" for="mb-nc-meta">Meta field</label><input class="form-control form-control-sm" id="mb-nc-meta" data-mb-nc-meta placeholder="optional" autocomplete="off"></div>
                    <div class="col-6"><label class="form-label small mb-0" for="mb-nc-gran">Granularity</label><select class="form-select form-select-sm" id="mb-nc-gran" data-mb-nc-gran><option value="">default</option><option value="seconds">seconds</option><option value="minutes">minutes</option><option value="hours">hours</option></select></div>
                    <div class="col-6"><label class="form-label small mb-0" for="mb-nc-expire">Expire after (seconds)</label><input class="form-control form-control-sm" type="number" min="0" id="mb-nc-expire" data-mb-nc-expire placeholder="keep forever"></div>
                </div>
                <div class="d-none" data-mb-nc-view>
                    <label class="form-label small mb-0" for="mb-nc-on">Based on collection</label><input class="form-control form-control-sm font-monospace mb-1" id="mb-nc-on" data-mb-nc-on autocomplete="off">
                    <label class="form-label small mb-0" for="mb-nc-pipe">Pipeline (JSON array of stages)</label>
                    <textarea class="form-control form-control-sm font-monospace" id="mb-nc-pipe" data-mb-nc-pipe rows="4" spellcheck="false" placeholder='[ { "$match": { "status": "active" } } ]'></textarea>
                    <div class="form-text">A view cannot write: <code>$out</code> and <code>$merge</code> are not allowed.</div>
                </div>
                <div class="invalid-feedback d-block" data-mb-nc-err></div>
            </div>
            <div class="modal-footer">
                <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
                <button type="submit" class="btn btn-primary">Create</button>
            </div>
        </form>
    </div>
</div>

<div class="modal fade" id="mb-update" tabindex="-1" aria-labelledby="mb-update-title" aria-hidden="true">
    <div class="modal-dialog modal-lg modal-dialog-scrollable">
        <form class="modal-content" data-mb-update-form novalidate>
            <div class="modal-header"><h2 class="modal-title h6" id="mb-update-title">Update many documents</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body">
                <label class="form-label small" for="mb-up-filter">Documents that match (JSON filter)</label>
                <input class="form-control font-monospace mb-1" id="mb-up-filter" data-mb-up-filter placeholder='{ "status": "new" }' autocomplete="off" spellcheck="false">
                <div class="form-check small mb-2"><input class="form-check-input" type="checkbox" id="mb-up-all" data-mb-up-all><label class="form-check-label" for="mb-up-all">The filter is empty on purpose: change every document</label></div>
                <div class="small text-body-secondary mb-1">Then, for each matching document (leave a row empty to skip it):</div>
                <?php for ($i = 0; $i < 4; $i++): ?>
                <div class="row g-1 mb-1" data-mb-up-row>
                    <div class="col-3"><select class="form-select form-select-sm" data-mb-up-op aria-label="Operation <?= $i + 1 ?>"><option value="set">set</option><option value="unset">remove</option><option value="inc">add to number</option><option value="rename">rename field to</option></select></div>
                    <div class="col-4"><input class="form-control form-control-sm font-monospace" data-mb-up-field placeholder="field (a.b for nested)" autocomplete="off" aria-label="Field <?= $i + 1 ?>"></div>
                    <div class="col-5"><input class="form-control form-control-sm font-monospace" data-mb-up-value placeholder='value as JSON, e.g. "done", 5, true' autocomplete="off" aria-label="Value <?= $i + 1 ?>"></div>
                </div>
                <?php endfor; ?>
                <div class="form-text">Only these four operations are offered; expressions and JavaScript are not accepted. <code>_id</code> cannot be changed.</div>
                <div class="small mt-2" data-mb-up-count></div>
                <div class="invalid-feedback d-block" data-mb-up-err></div>
            </div>
            <div class="modal-footer">
                <button type="button" class="btn btn-outline-secondary me-auto" data-mb-up-preview>Count matching documents</button>
                <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
                <button type="submit" class="btn btn-primary">Apply</button>
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
