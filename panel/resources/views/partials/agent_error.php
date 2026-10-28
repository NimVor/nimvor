<?php
/** @var \NimVor\Agent\AgentException $error */
?>
<div class="alert alert-warning" role="alert">
    <div class="d-flex gap-2">
        <i class="bi bi-plug fs-5"></i>
        <div>
            <div class="fw-semibold"><?= e($error->getMessage()) ?></div>
            <?php if ($error->reason() !== ''): ?><div class="small"><strong>Reason:</strong> <?= e($error->reason()) ?></div><?php endif; ?>
            <?php if ($error->remedy() !== ''): ?><div class="small"><strong>Action:</strong> <?= e($error->remedy()) ?></div><?php endif; ?>
        </div>
    </div>
</div>
