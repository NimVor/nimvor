<?php
/**
 * The Environment window of the Websites list (administrators and the website's owner).
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var string $text
 */
$id = (int) $site['id'];
?>
<form method="post" action="<?= e($this->url('sites.env.update', ['id' => $id])) ?>" data-nim-site-modal-form novalidate>
    <?= $this->csrfField() ?>
    <div class="modal-header">
        <h2 class="modal-title h5"><i class="bi bi-braces me-1"></i>Environment <span class="text-body-secondary fw-normal">· <?= e($site['domain']) ?></span></h2>
        <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
    </div>
    <div class="modal-body">
        <div class="alert alert-danger small d-none" data-nim-modal-error role="alert"></div>
        <?= $this->partial('sites/env_form', ['site' => $site, 'text' => $text, 'fieldId' => 'me-env']) ?>
    </div>
    <div class="modal-footer">
        <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
        <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save and apply</button>
    </div>
</form>
