<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $site */
/** @var int $retention days deleted files stay in the trash (0 = until emptied) */
$id = (int) $site['id'];
$btn = static fn (string $cmd, string $icon, string $label, string $extra = ''): string =>
    '<button type="button" class="btn btn-sm btn-light nim-fm-tool" data-fm-cmd="' . $cmd . '"' . $extra . '><i class="bi ' . $icon . '"></i><span>' . $label . '</span></button>';
?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.show', ['id' => $id])) ?>"><i class="bi bi-arrow-left"></i> <?= e($site['domain']) ?></a><?php $this->end('back'); ?>
<?php $this->start(); ?><i class="bi bi-folder2-open me-1"></i>File manager<?php $this->end('title'); ?>
<?php $this->start(); ?>
<div class="form-check form-switch mb-0 small">
    <input class="form-check-input" type="checkbox" id="fm-hidden" data-fm-hidden>
    <label class="form-check-label" for="fm-hidden">Show hidden files</label>
</div>
<?php $this->end('actions'); ?>

<div class="nim-fm" id="nim-fm"
     data-api="<?= e($this->url('sites.files.api', ['id' => $id])) ?>"
     data-upload="<?= e($this->url('sites.files.upload', ['id' => $id])) ?>"
     data-download="<?= e($this->url('sites.files.download', ['id' => $id])) ?>"
     data-edit="<?= e($this->url('sites.files.edit', ['id' => $id])) ?>"
     data-start="<?= e($start) ?>" data-site="<?= e($site['domain']) ?>">

    <div class="nim-fm-toolbar" role="toolbar" aria-label="File actions">
        <?= $btn('newfile', 'bi-file-earmark-plus', 'File') ?>
        <?= $btn('newfolder', 'bi-folder-plus', 'Folder') ?>
        <?= $btn('copy', 'bi-files', 'Copy', ' data-fm-needs="1+"') ?>
        <?= $btn('move', 'bi-arrows-move', 'Move', ' data-fm-needs="1+"') ?>
        <?= $btn('upload', 'bi-upload', 'Upload') ?>
        <?= $btn('download', 'bi-download', 'Download', ' data-fm-needs="file"') ?>
        <?= $btn('delete', 'bi-trash', 'Delete', ' data-fm-needs="1+"') ?>
        <?= $btn('rename', 'bi-input-cursor-text', 'Rename', ' data-fm-needs="1"') ?>
        <?= $btn('edit', 'bi-pencil-square', 'Edit', ' data-fm-needs="file"') ?>
        <?= $btn('perms', 'bi-shield-lock', 'Permissions', ' data-fm-needs="1+"') ?>
        <?= $btn('extract', 'bi-box-arrow-up', 'Extract', ' data-fm-needs="archive"') ?>
        <?= $btn('compress', 'bi-file-zip', 'Compress', ' data-fm-needs="1+"') ?>
        <?= $btn('trash', 'bi-recycle', 'Trash') ?>
        <div class="nim-fm-search ms-auto">
            <form class="input-group input-group-sm" data-fm-search-form role="search">
                <input class="form-control" type="search" placeholder="Search names…" data-fm-search aria-label="Search file names">
                <button class="btn btn-outline-secondary" type="submit" aria-label="Search"><i class="bi bi-search"></i></button>
            </form>
        </div>
        <input type="file" multiple hidden data-fm-upload-input>
    </div>

    <div class="nim-fm-body">
        <aside class="nim-fm-tree" aria-label="Folders">
            <div class="nim-fm-tree-head">
                <button type="button" class="btn btn-sm btn-link p-0" data-fm-cmd="home"><i class="bi bi-house"></i> <?= e($site['domain']) ?></button>
                <button type="button" class="btn btn-sm btn-link p-0 ms-auto" data-fm-cmd="collapse" title="Collapse all" aria-label="Collapse all"><i class="bi bi-arrows-collapse"></i></button>
            </div>
            <ul class="nim-fm-tree-list" role="tree" data-fm-tree></ul>
        </aside>

        <section class="nim-fm-pane" aria-label="Folder contents">
            <div class="nim-fm-nav">
                <div class="btn-group btn-group-sm">
                    <button type="button" class="btn btn-outline-secondary" data-fm-cmd="back" title="Back" aria-label="Back"><i class="bi bi-arrow-left"></i></button>
                    <button type="button" class="btn btn-outline-secondary" data-fm-cmd="forward" title="Forward" aria-label="Forward"><i class="bi bi-arrow-right"></i></button>
                    <button type="button" class="btn btn-outline-secondary" data-fm-cmd="up" title="Up one level" aria-label="Up one level"><i class="bi bi-arrow-90deg-up"></i></button>
                    <button type="button" class="btn btn-outline-secondary" data-fm-cmd="reload" title="Reload" aria-label="Reload"><i class="bi bi-arrow-clockwise"></i></button>
                </div>
                <form class="input-group input-group-sm flex-grow-1" data-fm-path-form>
                    <span class="input-group-text font-monospace">/</span>
                    <input class="form-control font-monospace" data-fm-path aria-label="Current folder" spellcheck="false">
                    <button class="btn btn-outline-secondary" type="submit">Go</button>
                </form>
                <div class="btn-group btn-group-sm">
                    <button type="button" class="btn btn-outline-secondary" data-fm-cmd="selectall">Select all</button>
                    <button type="button" class="btn btn-outline-secondary" data-fm-cmd="selectnone">Unselect</button>
                </div>
            </div>
            <div class="nim-fm-banner d-none" data-fm-banner></div>
            <div class="nim-fm-list" data-fm-drop tabindex="0">
                <table class="table table-sm table-hover align-middle mb-0 nim-fm-table">
                    <thead>
                    <tr>
                        <th class="nim-fm-check"><input class="form-check-input" type="checkbox" data-fm-all aria-label="Select all"></th>
                        <th data-fm-sort="name" class="w-50">Name</th>
                        <th data-fm-sort="size" class="text-end">Size</th>
                        <th data-fm-sort="mtime" class="nim-fm-optional">Last modified</th>
                        <th data-fm-sort="type" class="nim-fm-optional">Type</th>
                        <th data-fm-sort="mode" class="nim-fm-optional">Permissions</th>
                    </tr>
                    </thead>
                    <tbody data-fm-rows><tr><td colspan="6" class="text-body-secondary p-3">Loading…</td></tr></tbody>
                </table>
                <div class="nim-fm-dropzone" aria-hidden="true"><i class="bi bi-cloud-arrow-up"></i><div>Drop files or folders to upload here</div></div>
            </div>
            <div class="nim-fm-uploads d-none" data-fm-uploads>
                <div class="d-flex align-items-center mb-1"><strong class="small me-auto">Uploads</strong><button type="button" class="btn btn-sm btn-link p-0" data-fm-cmd="clearuploads">Clear finished</button></div>
                <ul class="list-unstyled mb-0 small" data-fm-upload-list></ul>
            </div>
            <footer class="nim-fm-status small" data-fm-status aria-live="polite"></footer>
        </section>
    </div>
    <ul class="dropdown-menu nim-fm-menu" data-fm-menu role="menu"></ul>
</div>

<div class="modal fade" id="fm-dialog" tabindex="-1" aria-labelledby="fm-dialog-title" aria-hidden="true">
    <div class="modal-dialog modal-dialog-centered">
        <form class="modal-content" data-fm-dialog-form novalidate>
            <div class="modal-header"><h2 class="modal-title h6" id="fm-dialog-title"></h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body" data-fm-dialog-body></div>
            <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button type="submit" class="btn btn-primary" data-fm-dialog-ok>OK</button></div>
        </form>
    </div>
</div>
<div class="modal fade" id="fm-trash" tabindex="-1" aria-labelledby="fm-trash-title" aria-hidden="true">
    <div class="modal-dialog modal-lg modal-dialog-scrollable">
        <div class="modal-content">
            <div class="modal-header"><h2 class="modal-title h6" id="fm-trash-title"><i class="bi bi-recycle me-1"></i>Trash</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body p-0">
                <p class="small text-body-secondary px-3 pt-3 mb-2">Deleted files and folders stay here until you delete them permanently or empty the trash<?= $retention > 0 ? '; after ' . (int) $retention . ' days they are deleted automatically' : '' ?>. They still count towards the disk space.</p>
                <div class="table-responsive">
                    <table class="table table-sm table-hover align-middle mb-0">
                        <thead><tr><th class="nim-fm-check"><input class="form-check-input" type="checkbox" data-fm-trash-all aria-label="Select all"></th><th>Name</th><th>Was in</th><th class="text-end">Size</th><th>Deleted</th></tr></thead>
                        <tbody data-fm-trash-rows></tbody>
                    </table>
                </div>
            </div>
            <div class="modal-footer">
                <button type="button" class="btn btn-sm btn-outline-danger me-auto" data-fm-trash-empty>Empty trash</button>
                <button type="button" class="btn btn-sm btn-outline-danger" data-fm-trash-delete disabled>Delete permanently</button>
                <button type="button" class="btn btn-sm btn-primary" data-fm-trash-restore disabled><i class="bi bi-arrow-counterclockwise me-1"></i>Restore</button>
            </div>
        </div>
    </div>
</div>
<?php $this->start(); ?><script src="<?= e($this->asset('assets/js/filemanager.js')) ?>" defer></script><?php $this->end('scripts'); ?>
