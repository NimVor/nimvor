<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed>|null $status
 * @var list<array<string, mixed>> $domains
 * @var list<array<string, mixed>> $websites
 * @var bool $admin
 * @var int $preselect
 */
use WhaCP\Security\Permissions as P;

$errors ??= [];
$old ??= [];
$installed = (array) ($status['installed'] ?? []);
$allInstalled = $installed !== [] && !in_array(false, $installed, true);
$running = (array) ($status['running'] ?? []);
$ready = !empty($status['configured']) && !empty($running['postfix']);
?>
<?php $this->start(); ?>Email<?php $this->end('title'); ?>
<?php $this->start(); ?>Mailboxes and aliases for your domains<?php $this->end('subtitle'); ?>
<?php if ($status === null): ?>
    <div class="alert alert-warning">The server agent is not reachable, so the mail server state is unknown.</div>
<?php elseif (!$ready): ?>
    <div class="card mb-3"><div class="card-body">
        <?php if (!$allInstalled): ?>
            <p>The mail server (Postfix, Dovecot and OpenDKIM) is not installed<?= $installed !== [] ? ' completely' : '' ?>.</p>
            <?php if ($this->can(P::PACKAGES_MANAGE)): ?><form method="post" action="<?= e($this->url('mail.install')) ?>"><?= $this->csrfField() ?><button class="btn btn-primary"><i class="bi bi-download me-1"></i>Install the mail server</button></form><?php endif; ?>
        <?php elseif ($this->can(P::SERVER_MANAGE)): ?>
            <p><?= empty($status['configured']) ? 'The mail server is installed but not set up for WhaCP yet.' : 'Email is set up but Postfix is not running.' ?></p>
            <form method="post" action="<?= e($this->url('mail.setup')) ?>" class="wcp-narrow" data-wcp-busy>
                <?= $this->csrfField() ?>
                <?= $this->partial('partials/field', ['name' => 'hostname', 'label' => 'Mail server host name', 'value' => (string) ($old['hostname'] ?? $status['hostname'] ?? ''), 'attrs' => ['placeholder' => 'mail.example.com', 'required' => 'required'], 'help' => 'The name MX records point to. It must resolve to this server; its reverse DNS (PTR) should match. A Let\'s Encrypt certificate for it is used when present.']) ?>
                <?php if (empty($status['configured'])): ?>
                    <div class="form-check mb-3"><input class="form-check-input" type="checkbox" name="confirm" value="1" id="mail-confirm" required><label class="form-check-label" for="mail-confirm">Change Postfix's configuration (main.cf, master.cf) and turn off Dovecot's sign-in for system accounts; backups are kept</label></div>
                <?php endif; ?>
                <button class="btn btn-primary"><i class="bi bi-play me-1"></i>Set up and start</button>
            </form>
        <?php else: ?>
            <p class="mb-0">Email is not set up on this server yet; ask the administrator.</p>
        <?php endif; ?>
    </div></div>
<?php endif; ?>
<div class="row g-3">
    <div class="col-lg-8">
        <div class="card">
            <div class="card-header d-flex align-items-center"><i class="bi bi-envelope me-1"></i>Mail domains
                <?php if ($ready): ?><span class="ms-auto small text-body-secondary">IMAP <code><?= e((string) $status['hostname']) ?>:993</code> · SMTP <code><?= e((string) $status['hostname']) ?>:587</code> (STARTTLS)</span><?php endif; ?></div>
            <div class="table-responsive">
                <table class="table mb-0 wcp-table align-middle">
                    <thead><tr><th>Domain</th><th>Website</th><th class="text-end">Mailboxes</th><th class="text-end">Aliases</th></tr></thead>
                    <tbody>
                    <?php foreach ($domains as $d): ?>
                        <tr>
                            <td><a class="font-monospace" href="<?= e($this->url('mail.domain', ['id' => (int) $d['id']])) ?>"><?= e((string) $d['domain']) ?></a></td>
                            <td><?= e((string) ($d['website'] ?? '—')) ?></td>
                            <td class="text-end"><?= (int) $d['mailboxes'] ?></td>
                            <td class="text-end"><?= (int) $d['aliases'] ?></td>
                        </tr>
                    <?php endforeach; ?>
                    <?php if ($domains === []): ?><tr><td colspan="4" class="text-body-secondary">No mail domains yet.</td></tr><?php endif; ?>
                    </tbody>
                </table>
            </div>
        </div>
    </div>
    <div class="col-lg-4">
        <?php if ($ready): ?>
        <div class="card">
            <div class="card-header"><i class="bi bi-plus-lg me-1"></i>Host email for a domain</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('mail.store')) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <div class="mb-3">
                        <label class="form-label" for="mail-site">Website</label>
                        <select class="form-select<?= isset($errors['website_id']) ? ' is-invalid' : '' ?>" id="mail-site" name="website_id">
                            <?php if ($admin): ?><option value="">— none (enter the domain below) —</option><?php endif; ?>
                            <?php foreach ($websites as $w): ?><option value="<?= (int) $w['id'] ?>"<?= (int) ($old['website_id'] ?? $preselect) === (int) $w['id'] ? ' selected' : '' ?>><?= e((string) $w['domain']) ?></option><?php endforeach; ?>
                        </select>
                        <?php if (isset($errors['website_id'])): ?><div class="invalid-feedback"><?= e($errors['website_id']) ?></div><?php endif; ?>
                    </div>
                    <?php if ($admin): ?><?= $this->partial('partials/field', ['name' => 'domain', 'label' => 'Domain (empty = the website\'s domain)', 'value' => (string) ($old['domain'] ?? ''), 'attrs' => ['placeholder' => 'example.com']]) ?><?php endif; ?>
                    <button class="btn btn-primary btn-sm">Add domain</button>
                    <div class="form-text">A DKIM key is created for the domain; the next page lists the DNS records to publish.</div>
                </form>
            </div>
        </div>
        <?php endif; ?>
    </div>
</div>
