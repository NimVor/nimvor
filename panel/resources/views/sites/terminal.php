<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var array<string, mixed>|null $status
 * @var string|null $mode the account's shell access in effect
 */
use NimVor\Security\Permissions as P;

$enabled = (bool) ($status['enabled'] ?? false);
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'terminal']) ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php return; endif; ?>

<?php if (!$enabled): ?>
    <div class="card"><div class="nim-empty-state">
        <i class="bi bi-terminal" aria-hidden="true"></i>
        <div class="fw-medium">The web terminal is not enabled on this server</div>
        <div class="small">It needs the panel served over HTTPS by Nginx (<code>install.sh --public-https</code>), which turns it on.<?php if ($this->can(P::SERVER_MANAGE)): ?> Until then, use SSH with the account's key or password (FTP / SFTP page).<?php endif; ?></div>
    </div></div>
    <?php return; ?>
<?php endif; ?>

<?php if ($mode !== 'jailed' && $mode !== 'normal'): ?>
    <div class="card"><div class="nim-empty-state">
        <i class="bi bi-terminal-x" aria-hidden="true"></i>
        <div class="fw-medium">Shell access is off for this website</div>
        <?php if ($this->can(P::SECURITY_MANAGE) && $this->hasRoute('security.shell')): ?>
            <div class="small mb-3">Give the account <code><?= e($site['system_user']) ?></code> a jailed or normal shell under Security → Shell access, and turn on its SSH/SFTP login (FTP / SFTP page).</div>
            <a class="btn btn-primary" href="<?= e($this->url('security.shell')) ?>">Open Shell access</a>
        <?php else: ?>
            <div class="small">Please ask your hosting provider to enable shell access for this website.</div>
        <?php endif; ?>
    </div></div>
    <?php return; ?>
<?php endif; ?>

<div class="nim-infobar">
    <i class="bi bi-info-circle" aria-hidden="true"></i>
    <span>A shell as <code><?= e($site['system_user']) ?></code><?= $mode === 'jailed' ? ', in a sandbox that shows only this account\'s website folder' : ' on the server' ?>. Sign in with the account's Linux password (the same as for SSH and SFTP). The session closes after <?= (int) ($idle ?? 15) ?> minutes without input, when you sign out of the panel and when the account's password or shell access changes; it is logged.</span>
</div>
<?= $this->partial('terminal/_console', ['styleNonce' => $styleNonce, 'openUrl' => $openUrl, 'unlocked' => $unlocked, 'needsCode' => $needsCode, 'account' => (string) $site['system_user']]) ?>
