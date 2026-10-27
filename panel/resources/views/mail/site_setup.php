<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var array<string, mixed>|null $status
 * @var bool $ready
 */
use NimVor\Security\Permissions as P;

$errors ??= [];
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'mail']) ?>
<div class="card" id="mail-setup">
    <div class="card-body nim-empty-state">
        <i class="bi bi-envelope" aria-hidden="true"></i>
        <div class="fw-medium fs-5 mb-1">Email is not set up for <?= e($site['domain']) ?></div>
        <?php if ($ready): ?>
            <p class="small mb-3">Host email for this domain to create mailboxes like info@<?= e($site['domain']) ?> and forwarders.</p>
            <form method="post" action="<?= e($this->url('mail.store')) ?>" data-nim-busy>
                <?= $this->csrfField() ?>
                <input type="hidden" name="website_id" value="<?= (int) $site['id'] ?>">
                <input type="hidden" name="return_to" value="/sites/<?= (int) $site['id'] ?>/mail">
                <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><i class="bi bi-envelope-plus me-1"></i>Set up email</button>
            </form>
            <?php if (isset($errors['website_id']) || isset($errors['domain'])): ?><div class="text-danger small mt-2"><?= e((string) ($errors['website_id'] ?? $errors['domain'])) ?></div><?php endif; ?>
            <p class="small mt-3 mb-0">A DKIM signing key is created; the next page lists the DNS records to publish.</p>
        <?php elseif ($this->can(P::SERVER_MANAGE) && $this->hasRoute('mail')): ?>
            <p class="small mb-3">The mail server is not <?= $status === null || empty($status['installed']) ? 'installed' : 'running or set up' ?> on this server yet.</p>
            <a class="btn btn-primary" href="<?= e($this->url('mail')) ?>">Set up the mail server</a>
        <?php else: ?>
            <p class="small mb-0">Email hosting is not available on this server yet. Please ask your hosting provider or administrator.</p>
        <?php endif; ?>
    </div>
</div>
