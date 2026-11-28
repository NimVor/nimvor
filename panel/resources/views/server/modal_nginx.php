<?php
/**
 * Nginx in a window: Start / Stop / Restart / Reload / Test at the top, then the state and the server-wide tuning.
 *
 * @var \NimVor\View\View $this
 * @var array{installed: bool, running: bool, config_ok: bool, config_error: string, tuning: bool}|null $status
 * @var array<string, mixed> $tuning
 * @var string $version
 * @var \NimVor\Agent\AgentException|null $agentError
 */
use NimVor\Security\Permissions as P;

$status ??= null;
$canService = $this->can(P::SERVICES_MANAGE);
$unit = 'nginx.service';
$op = function (string $op, string $label, string $icon, string $cls = 'btn-outline-secondary') use ($unit): string {
    return '<form method="post" action="' . e($this->url('services.control')) . '" data-nim-site-modal-form class="d-inline">' . $this->csrfField()
        . '<input type="hidden" name="unit" value="' . e($unit) . '"><input type="hidden" name="op" value="' . e($op) . '">'
        . '<button class="btn btn-sm ' . $cls . '" type="submit"><i class="bi bi-' . e($icon) . ' me-1" aria-hidden="true"></i>' . e($label) . '</button></form>';
};
$post = function (string $route, string $label, string $icon) : string {
    return '<form method="post" action="' . e($this->url($route)) . '" data-nim-site-modal-form class="d-inline">' . $this->csrfField()
        . '<button class="btn btn-sm btn-outline-secondary" type="submit"><i class="bi bi-' . e($icon) . ' me-1" aria-hidden="true"></i>' . e($label) . '</button></form>';
};
?>
<div class="modal-header flex-wrap gap-2">
    <h2 class="modal-title h5 me-auto"><?= $this->logo('nginx', 28, 'me-2') ?: '<i class="bi bi-hdd-rack me-2" aria-hidden="true"></i>' ?>Nginx<?= $version !== '' ? ' ' . e($version) : '' ?>
        <?php if ($status !== null && $status['installed']): ?><span class="badge text-bg-<?= $status['running'] ? 'success' : 'danger' ?> ms-2 align-middle"><?= $status['running'] ? 'Running' : 'Stopped' ?></span><?php endif; ?></h2>
    <?php if ($status !== null && $status['installed']): ?>
        <div class="d-flex flex-wrap gap-1" role="group" aria-label="Nginx service">
            <?php if ($canService): ?>
                <?php if ($status['running']): ?>
                    <?= $op('stop', 'Stop', 'stop-fill') ?>
                    <?= $op('restart', 'Restart', 'arrow-clockwise') ?>
                    <?= $post('nginx.reload', 'Reload', 'arrow-repeat') ?>
                <?php else: ?>
                    <?= $op('start', 'Start', 'play-fill', 'btn-success') ?>
                <?php endif; ?>
            <?php endif; ?>
            <?= $post('nginx.test', 'Test configuration', 'clipboard-check') ?>
        </div>
    <?php endif; ?>
    <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
</div>
<div class="modal-body">
    <?php if ($status === null): ?>
        <?= $this->partial('partials/agent_error', ['error' => $agentError ?? null]) ?>
    <?php elseif (!$status['installed']): ?>
        <div class="small text-body-secondary">Nginx is not installed on this server.</div>
    <?php else: ?>
        <?= $this->partial('server/_nginx_body', ['status' => $status, 'tuning' => $tuning, 'modal' => true]) ?>
    <?php endif; ?>
</div>
