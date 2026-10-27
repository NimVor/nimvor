<?php
/** @var \WhaCP\View\View $this */
/** @var string $discoveredAt */
?>
<div class="wcp-discovery-bar">
    <span><i class="bi bi-binoculars me-1"></i>Discovered <?= e(time_ago($discoveredAt)) ?>
        <span class="text-body-secondary">(<?= e(gmdate('Y-m-d H:i', (int) strtotime($discoveredAt))) ?> UTC)</span>
        · read-only snapshot, nothing on the server was changed.</span>
    <?php if ($this->can(\WhaCP\Security\Permissions::DISCOVERY_RUN)): ?>
        <form method="post" action="<?= e($this->url('discovery.run')) ?>" class="d-inline" data-wcp-busy>
            <?= $this->csrfField() ?>
            <input type="hidden" name="return_to" value="<?= e($current_path ?? '/discovery') ?>">
            <button class="btn btn-sm btn-outline-primary" type="submit">
                <span class="spinner-border spinner-border-sm d-none" aria-hidden="true"></span>
                <i class="bi bi-arrow-repeat"></i> Re-scan
            </button>
        </form>
    <?php endif; ?>
</div>
