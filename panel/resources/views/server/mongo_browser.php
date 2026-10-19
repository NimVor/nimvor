<?php
/**
 * Server → MongoDB → Browser: a full-page database browser. A collections
 * sidebar on the left; the chosen collection's documents, with a JSON filter
 * and paging, on the right. Mirrors the SQL web browser (databases/browser).
 *
 * @var \WhaCP\View\View $this
 * @var string $api        the JSON endpoint (mongo.browse)
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
        <div class="p-2 border-bottom">
            <input class="form-control form-control-sm" type="search" placeholder="Filter collections" aria-label="Filter collections" data-mb-coll-filter>
        </div>
        <ul class="list-group list-group-flush" data-mb-colls aria-label="Collections"></ul>
        <div class="small text-body-secondary px-2 py-2" data-mb-colls-count></div>
    </aside>
    <section class="wcp-db-main">
        <div class="wcp-db-bar">
            <span class="font-monospace small text-truncate me-2" data-mb-current>No collection selected</span>
            <div class="input-group input-group-sm wcp-db-filter">
                <span class="input-group-text"><i class="bi bi-funnel"></i></span>
                <input class="form-control font-monospace" type="text" placeholder='Filter, e.g. { "status": "active" }' aria-label="JSON filter" data-mb-filter autocomplete="off" spellcheck="false" disabled>
                <button class="btn btn-primary" type="button" data-mb-run disabled>Run</button>
            </div>
            <div class="btn-group btn-group-sm ms-auto" role="group" aria-label="Paging">
                <button class="btn btn-outline-secondary" type="button" data-mb-prev disabled><i class="bi bi-chevron-left"></i></button>
                <button class="btn btn-outline-secondary" type="button" data-mb-next disabled><i class="bi bi-chevron-right"></i></button>
            </div>
            <span class="small text-body-secondary ms-2 text-nowrap" data-mb-range></span>
        </div>
        <div class="wcp-db-pane" data-mb-pane aria-live="polite">
            <div class="text-body-secondary p-4">Choose a database, then a collection to browse its documents.</div>
        </div>
    </section>
</div>

<?php $this->start(); ?><script src="<?= e($this->asset('assets/js/mongobrowser.js')) ?>" defer></script><?php $this->end('scripts'); ?>
