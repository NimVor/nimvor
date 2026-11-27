<?php
/**
 * Server → Nginx.
 *
 * @var \NimVor\View\View $this
 * @var array{installed: bool, running: bool, config_ok: bool, config_error: string, tuning: bool}|null $status
 * @var array<string, mixed> $tuning
 */
use NimVor\Security\Permissions as P;

$status ??= null;
$canManage = $this->can(P::SERVER_MANAGE);
$canReload = $this->can(P::SERVICES_MANAGE);
?>
<?php $this->start(); ?>Nginx<?php $this->end('title'); ?>
<?php $this->start(); ?>Web server state, configuration test and server-wide tuning<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
    <?php if ($status !== null && $status['installed']): ?>
        <form method="post" action="<?= e($this->url('nginx.test')) ?>" data-nim-busy class="d-inline"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-clipboard-check me-1"></i>Test config</button></form>
        <?php if ($canReload): ?><form method="post" action="<?= e($this->url('nginx.reload')) ?>" data-nim-busy class="d-inline"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-clockwise me-1"></i>Reload</button></form><?php endif; ?>
    <?php endif; ?>
<?php $this->end('actions'); ?>

<?php if ($status === null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError ?? null]) ?>
    <?php return; ?>
<?php endif; ?>

<?php if (!$status['installed']): ?>
    <div class="card"><div class="card-body small text-body-secondary">Nginx is not installed on this server.</div></div>
    <?php return; ?>
<?php endif; ?>

<?= $this->partial('server/_nginx_body', ['status' => $status, 'tuning' => $tuning]) ?>
