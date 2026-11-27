<?php
/**
 * The database server's settings in a window.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed>|null $status
 */
?>
<div class="modal-header">
    <h2 class="modal-title h5"><i class="bi bi-database me-2" aria-hidden="true"></i>Database server settings</h2>
    <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
</div>
<div class="modal-body">
    <?php if (empty($status['settings'])): ?>
        <div class="alert alert-warning small mb-0">The database server's settings could not be read. Is it running?</div>
    <?php else: ?>
        <?= $this->partial('databases/_server_settings', ['status' => $status, 'modal' => true]) ?>
    <?php endif; ?>
</div>
