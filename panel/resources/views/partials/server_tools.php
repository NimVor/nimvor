<?php
/**
 * Server tools for administrators (the dashboard's Server card): update
 * NimVor (only when a newer version exists), restart every service, restart
 * the server (its confirmation dialog is in the layout).
 *
 * @var \NimVor\View\View $this
 */
$user = $this->shared('user');
$serverAdmin = !empty($user) && \NimVor\Hosting\Scope::isAll($user);
$st = $this->shared('agent_status');
$topHost = (string) ((is_callable($st) ? $st() : null)['hostname'] ?? '');
$update_available = $this->shared('update_available');
?>
<div class="d-flex gap-1 nim-server-tools">
        <?php if ($serverAdmin && $this->can(\NimVor\Security\Permissions::SERVER_MANAGE)): ?>
            <a class="btn btn-sm btn-warning<?= !empty($update_available) && is_callable($update_available) && $update_available() ? '' : ' d-none' ?>" href="<?= e($this->url('nimvor.update')) ?>" title="A NimVor update is available" aria-label="A NimVor update is available" data-nim-topbar-update><i class="bi bi-cloud-arrow-down"></i></a>
        <?php endif; ?>
        <?php if ($serverAdmin && $this->can(\NimVor\Security\Permissions::SERVICES_MANAGE) && $this->hasRoute('services.restart_all')): ?>
            <form method="post" action="<?= e($this->url('services.restart_all')) ?>" class="d-flex" data-nim-noajax data-nim-confirm="Restart every running service now (databases, PHP, mail, DNS, FTP, Nginx; never SSH), then NimVor's agent and panel? Websites and mail pause for a few seconds.">
                <?= $this->csrfField() ?>
                <button class="btn btn-sm btn-outline-secondary" type="submit" title="Restart all services" aria-label="Restart all services"><i class="bi bi-arrow-repeat"></i></button>
            </form>
        <?php endif; ?>
        <?php if ($serverAdmin && $this->can(\NimVor\Security\Permissions::SERVER_MANAGE) && $topHost !== ''): ?>
            <button class="btn btn-sm btn-outline-danger" type="button" data-bs-toggle="modal" data-bs-target="#nim-reboot" title="Restart the server" aria-label="Restart the server"><i class="bi bi-power"></i></button>
        <?php endif; ?>
</div>
