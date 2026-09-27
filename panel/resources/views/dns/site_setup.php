<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $site
 * @var array<string, mixed>|null $status
 * @var list<string> $nameservers
 * @var bool $ready
 */
use WhaCP\Security\Permissions as P;

$errors ??= [];
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'dns']) ?>
<div class="card" id="dns-setup">
    <div class="card-body wcp-empty-state">
        <i class="bi bi-signpost" aria-hidden="true"></i>
        <div class="fw-medium fs-5 mb-1">DNS for <?= e($site['domain']) ?> is not hosted here</div>
        <?php if ($ready && $nameservers !== []): ?>
            <p class="small mb-3">Host the domain's DNS on this server to manage its records here.</p>
            <form method="post" action="<?= e($this->url('dns.store')) ?>" class="d-inline-block text-start" data-wcp-busy>
                <?= $this->csrfField() ?>
                <input type="hidden" name="website_id" value="<?= (int) $site['id'] ?>">
                <input type="hidden" name="return_to" value="/sites/<?= (int) $site['id'] ?>/dns">
                <div class="form-check mb-3"><input class="form-check-input" type="checkbox" name="template" value="1" id="dns-tpl" checked><label class="form-check-label small" for="dns-tpl">Add the usual records (@ and www to this server, SPF, CAA for Let's Encrypt)</label></div>
                <div class="text-center"><button class="btn btn-primary" type="submit"><i class="bi bi-plus-lg me-1"></i>Create DNS zone</button></div>
            </form>
            <?php if (isset($errors['website_id']) || isset($errors['name'])): ?><div class="text-danger small mt-2"><?= e((string) ($errors['website_id'] ?? $errors['name'])) ?></div><?php endif; ?>
            <p class="small mt-3 mb-0">Afterwards, set these name servers for the domain at its registrar: <?= e(implode(', ', $nameservers)) ?>.</p>
        <?php elseif ($this->can(P::SERVER_MANAGE) && $this->hasRoute('dns')): ?>
            <p class="small mb-3">The DNS server is not <?= $status === null || empty($status['installed']) ? 'installed' : 'running or set up' ?> on this server yet.</p>
            <a class="btn btn-primary" href="<?= e($this->url('dns')) ?>">Set up the DNS server</a>
        <?php else: ?>
            <p class="small mb-0">DNS hosting is not available on this server. You can keep managing the domain's records where it is registered: point an A record for <?= e($site['domain']) ?> to this server's IP address.</p>
        <?php endif; ?>
    </div>
</div>
