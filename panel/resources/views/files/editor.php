<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $site */
$id = (int) $site['id'];
$back = $this->url('sites.files', ['id' => $id], ['path' => $folder]);
?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($back) ?>" data-fe-close><i class="bi bi-arrow-left"></i> File manager</a><?php $this->end('back'); ?>
<?php $this->start(); ?><i class="bi bi-pencil-square me-1"></i><span class="font-monospace"><?= e($site['domain']) ?>/<?= e($path) ?></span><?php $this->end('title'); ?>
<?php $this->start(); ?>
<span class="small text-body-secondary d-none d-md-inline" data-fe-info></span>
<button type="button" class="btn btn-sm btn-outline-secondary" data-fe-reload><i class="bi bi-arrow-clockwise"></i> Reload</button>
<button type="button" class="btn btn-sm btn-primary" data-fe-save disabled><i class="bi bi-save"></i> Save</button>
<button type="button" class="btn btn-sm btn-outline-primary" data-fe-saveclose disabled>Save &amp; close</button>
<?php $this->end('actions'); ?>

<div class="nim-fe" id="nim-fe"
     data-api="<?= e($this->url('sites.files.api', ['id' => $id])) ?>"
     data-path="<?= e($path) ?>" data-back="<?= e($back) ?>"
     data-download="<?= e($this->url('sites.files.download', ['id' => $id], ['path' => $path])) ?>">
    <div class="nim-fe-body">
        <pre class="nim-fe-gutter" aria-hidden="true" data-fe-gutter>1</pre>
        <textarea class="nim-fe-text" spellcheck="false" wrap="off" data-fe-text aria-label="File content" disabled>Loading…</textarea>
    </div>
    <footer class="nim-fe-status small">
        <span data-fe-pos>Ln 1, Col 1</span>
        <span data-fe-state class="ms-3"></span>
        <label class="form-check form-switch ms-auto mb-0"><input class="form-check-input" type="checkbox" data-fe-wrap> <span class="form-check-label">Wrap lines</span></label>
        <span class="ms-3 text-body-secondary">Ctrl+S saves · Tab inserts a tab</span>
    </footer>
</div>
<?php $this->start(); ?><script src="<?= e($this->asset('assets/js/fileeditor.js')) ?>" defer></script><?php $this->end('scripts'); ?>
