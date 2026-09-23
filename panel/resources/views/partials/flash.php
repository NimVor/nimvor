<?php /** @var \WhaCP\View\View $this */ ?>
<?php if (!empty($flash_success)): ?>
    <div class="alert alert-success d-flex align-items-start gap-2" role="status">
        <i class="bi bi-check-circle-fill mt-1"></i><div><?= e($flash_success) ?></div>
    </div>
<?php endif; ?>
<?php if (!empty($flash_error)): ?>
    <div class="alert alert-danger d-flex align-items-start gap-2" role="alert">
        <i class="bi bi-exclamation-triangle-fill mt-1"></i><div><?= e($flash_error) ?></div>
    </div>
<?php endif; ?>
