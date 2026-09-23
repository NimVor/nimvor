<?php /** @var \WhaCP\View\View $this */ ?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'files']) ?>

<div class="card" id="wcp-files"
     data-api="<?= e($this->url('sites.files.api', ['id' => (int) $site['id']])) ?>"
     data-upload="<?= e($this->url('sites.files.upload', ['id' => (int) $site['id']])) ?>"
     data-download="<?= e($this->url('sites.files.download', ['id' => (int) $site['id']])) ?>"
     data-start="<?= e($start) ?>">
    <div class="card-header d-flex flex-wrap gap-2 align-items-center">
        <nav aria-label="Folder" class="me-auto"><ol class="breadcrumb mb-0 small" data-fm-crumbs></ol></nav>
        <div class="input-group input-group-sm w-auto">
            <input class="form-control" type="search" placeholder="Find by name" data-fm-search aria-label="Find files">
            <button class="btn btn-outline-secondary" type="button" data-fm-action="search"><i class="bi bi-search"></i></button>
        </div>
        <div class="btn-group btn-group-sm" role="group" aria-label="File actions">
            <button class="btn btn-outline-secondary" type="button" data-fm-action="refresh" title="Refresh"><i class="bi bi-arrow-clockwise"></i></button>
            <button class="btn btn-outline-secondary" type="button" data-fm-action="newfolder"><i class="bi bi-folder-plus"></i> Folder</button>
            <button class="btn btn-outline-secondary" type="button" data-fm-action="newfile"><i class="bi bi-file-earmark-plus"></i> File</button>
            <label class="btn btn-outline-primary mb-0"><i class="bi bi-upload"></i> Upload<input type="file" multiple hidden data-fm-upload></label>
        </div>
    </div>
    <div class="px-3 pt-2 d-none" data-fm-progress><div class="progress"><div class="progress-bar" data-fm-bar></div></div><div class="small text-body-secondary mt-1" data-fm-progress-text></div></div>
    <div class="table-responsive">
        <table class="table table-hover table-sm align-middle mb-0 wcp-table">
            <thead><tr><th class="w-50">Name</th><th class="text-end">Size</th><th>Permissions</th><th>Modified</th><th></th></tr></thead>
            <tbody data-fm-rows><tr><td colspan="5" class="text-body-secondary">Loading…</td></tr></tbody>
        </table>
    </div>
    <div class="card-footer small text-body-secondary">Files are confined to <code><?= e($site['root']) ?></code> and owned by <code><?= e($site['system_user']) ?></code>. Top-level folders (public_html, logs, tmp, backups) cannot be deleted.</div>
</div>

<div class="modal fade" id="fm-editor" tabindex="-1" aria-labelledby="fm-editor-title" aria-hidden="true">
    <div class="modal-dialog modal-xl modal-dialog-scrollable">
        <div class="modal-content">
            <div class="modal-header"><h2 class="modal-title h6 font-monospace" id="fm-editor-title"></h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body p-0"><textarea class="form-control font-monospace border-0 rounded-0 wcp-editor" spellcheck="false" data-fm-editor aria-label="File content"></textarea></div>
            <div class="modal-footer"><span class="small text-body-secondary me-auto" data-fm-editor-info></span><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Close</button><button type="button" class="btn btn-primary" data-fm-save>Save</button></div>
        </div>
    </div>
</div>

<div class="modal fade" id="fm-prompt" tabindex="-1" aria-labelledby="fm-prompt-title" aria-hidden="true">
    <div class="modal-dialog modal-dialog-centered">
        <form class="modal-content" data-fm-prompt-form>
            <div class="modal-header"><h2 class="modal-title h6" id="fm-prompt-title"></h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body"><label class="form-label small" for="fm-prompt-input" data-fm-prompt-label></label><input class="form-control" id="fm-prompt-input" autocomplete="off" required></div>
            <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button type="submit" class="btn btn-primary">OK</button></div>
        </form>
    </div>
</div>
<script src="<?= e($this->asset('assets/js/files.js')) ?>" defer></script>
