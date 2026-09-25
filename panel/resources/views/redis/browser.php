<?php
/** @var \WhaCP\View\View $this */
?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($back) ?>"><i class="bi bi-arrow-left"></i> <?= e($backLabel) ?></a><?php $this->end('back'); ?>
<?php $this->start(); ?><i class="bi bi-lightning me-1"></i><?= e($heading) ?><?php $this->end('title'); ?>

<div class="wcp-rb" id="wcp-rb" data-api="<?= e($api) ?>" data-prefix="<?= e($prefix) ?>" data-fixed="<?= $fixedPrefix ? '1' : '0' ?>">
    <div class="wcp-rb-bar">
        <label class="small text-body-secondary" for="rb-db">Database</label>
        <select class="form-select form-select-sm w-auto" id="rb-db" data-rb-db>
            <?php for ($i = 0; $i < 16; $i++): ?><option value="<?= $i ?>"><?= $i ?></option><?php endfor; ?>
        </select>
        <form class="input-group input-group-sm wcp-rb-search" data-rb-search>
            <?php if ($prefix !== ''): ?><span class="input-group-text font-monospace" title="Keys are limited to this prefix"><?= e($prefix) ?></span><?php endif; ?>
            <?php if (!$fixedPrefix): ?><input class="form-control font-monospace wcp-rb-prefix" data-rb-prefix placeholder="prefix (optional)" aria-label="Key prefix" spellcheck="false"><?php endif; ?>
            <input class="form-control font-monospace" data-rb-pattern value="*" aria-label="Key pattern (* and ? are wildcards)" spellcheck="false">
            <button class="btn btn-outline-secondary" type="submit"><i class="bi bi-search"></i> Scan</button>
        </form>
        <button type="button" class="btn btn-sm btn-primary ms-auto" data-rb-cmd="newkey"><i class="bi bi-plus-lg"></i> New key</button>
    </div>
    <div class="wcp-rb-body">
        <aside class="wcp-rb-keys">
            <ul class="list-group list-group-flush" data-rb-keys role="listbox" aria-label="Keys"></ul>
            <div class="p-2 d-grid"><button type="button" class="btn btn-sm btn-outline-secondary d-none" data-rb-cmd="more">Load more</button></div>
            <div class="wcp-rb-count small text-body-secondary px-2 pb-2" data-rb-count></div>
        </aside>
        <section class="wcp-rb-detail" data-rb-detail aria-live="polite">
            <div class="text-body-secondary p-4">Select a key to see its value.</div>
        </section>
    </div>
</div>

<div class="modal fade" id="rb-dialog" tabindex="-1" aria-labelledby="rb-dialog-title" aria-hidden="true">
    <div class="modal-dialog modal-dialog-centered modal-lg">
        <form class="modal-content" data-rb-dialog-form novalidate>
            <div class="modal-header"><h2 class="modal-title h6" id="rb-dialog-title"></h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body" data-rb-dialog-body></div>
            <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button type="submit" class="btn btn-primary" data-rb-dialog-ok>OK</button></div>
        </form>
    </div>
</div>
<?php $this->start(); ?><script src="<?= e($this->asset('assets/js/redisbrowser.js')) ?>" defer></script><?php $this->end('scripts'); ?>
