<?php /** @var \WhaCP\View\View $this */ ?>
<?php if (!empty($flash_success)): ?>
    <div class="alert alert-success d-flex align-items-start gap-2" role="status">
        <i class="bi bi-check-circle-fill mt-1"></i><div><?= e($flash_success) ?></div>
    </div>
<?php endif; ?>
<?php if (!empty($flash_error)): ?>
    <div class="alert alert-danger d-flex align-items-start gap-2" role="alert">
        <i class="bi bi-exclamation-triangle-fill mt-1"></i><div><?= e($flash_error) ?>
            <?php if (!empty($flash_details)): ?>
                <details class="mt-2 small"><summary>Technical details</summary><pre class="mb-0 mt-1 wcp-pre"><?= e($flash_details) ?></pre></details>
            <?php endif; ?></div>
    </div>
<?php endif; ?>
<?php if (!empty($flash_job)): ?><div data-wcp-autojob="<?= e($flash_job) ?>" hidden></div><?php endif; ?>
