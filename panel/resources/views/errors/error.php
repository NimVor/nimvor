<?php /** @var \WhaCP\View\View $this */ ?>
<?php $this->start(); ?><?= e($title ?? 'Error') ?><?php $this->end('title'); ?>
<div class="card wcp-error-card">
    <div class="card-body">
        <div class="wcp-error-code"><?= e($status ?? 500) ?></div>
        <h2 class="h5"><?= e($title ?? 'Error') ?></h2>
        <p class="mb-2"><?= e($message ?? '') ?></p>
        <?php if (!empty($reason)): ?>
            <p class="mb-1"><strong>Reason:</strong> <?= e($reason) ?></p>
        <?php endif; ?>
        <?php if (!empty($remedy)): ?>
            <p class="mb-1"><strong>Action:</strong> <?= e($remedy) ?></p>
        <?php endif; ?>
        <?php if (!empty($reference)): ?>
            <p class="small text-body-secondary mt-3 mb-0">Reference: <code><?= e($reference) ?></code></p>
        <?php endif; ?>
        <?php if (!empty($debug)): ?>
            <pre class="small mt-3 mb-0 text-danger"><?= e($debug) ?></pre>
        <?php endif; ?>
        <a class="btn btn-outline-primary btn-sm mt-3" href="/"><i class="bi bi-house me-1"></i>Back to dashboard</a>
    </div>
</div>
