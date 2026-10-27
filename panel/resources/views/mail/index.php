<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed>|null $status
 * @var list<array<string, mixed>> $domains
 * @var list<array<string, mixed>> $websites
 * @var bool $admin
 * @var int $preselect
 */
use NimVor\Security\Permissions as P;

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
            <p><?= empty($status['configured']) ? 'The mail server is installed but not set up for NimVor yet.' : 'Email is set up but Postfix is not running.' ?></p>
            <form method="post" action="<?= e($this->url('mail.setup')) ?>" class="nim-narrow" data-nim-busy>
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
<?php $spam = (array) ($status['spam'] ?? []); $wm = (array) ($status['webmail'] ?? []); $srvManage = $ready && $this->can(P::SERVER_MANAGE); ?>
<div class="nim-panel mb-3">
    <div class="nim-actionbar">
        <div class="fw-semibold"><i class="bi bi-envelope me-1" aria-hidden="true"></i>Mail domains <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($domains) ?></span>
            <?php if ($ready): ?><span class="small text-body-secondary fw-normal ms-2">IMAP <code><?= e((string) $status['hostname']) ?>:993</code> · SMTP <code><?= e((string) $status['hostname']) ?>:587</code> (STARTTLS)</span><?php endif; ?></div>
        <div class="nim-actionbar-end">
            <?php if ($srvManage): ?>
                <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#spam-filter"><i class="bi bi-shield-exclamation me-1" aria-hidden="true"></i>Spam filter <?= !empty($spam['enabled']) ? $this->statusBadge(!empty($status['spam_running']) ? 'active' : 'failed', !empty($status['spam_running']) ? 'on' : 'not running') : $this->statusBadge('inactive', 'off') ?></button>
                <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#webmail"><i class="bi bi-envelope-open me-1" aria-hidden="true"></i>Webmail <?= !empty($wm['enabled']) ? $this->statusBadge(!empty($wm['running']) ? 'active' : 'failed', !empty($wm['running']) ? 'on' : 'not running') : $this->statusBadge('inactive', 'off') ?></button>
            <?php endif; ?>
            <?php if ($ready): ?><button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#mail-add"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Host email for a domain</button><?php endif; ?>
        </div>
    </div>
    <div class="nim-dtable">
<div class="table-responsive">
                <table class="table mb-0 nim-table align-middle">
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

<?php if ($srvManage): ?>
    <?= $this->modalStart('spam-filter', 'Spam filter', isset($errors['add_header']), 'modal-md') ?>
        <div class="small">
                <?php if (empty($status['spam_installed'])): ?>
                    <p class="text-body-secondary">Rspamd checks incoming mail; spam is filed into each mailbox's Junk folder or refused.</p>
                    <?php if ($this->can(P::PACKAGES_MANAGE)): ?><form method="post" action="<?= e($this->url('mail.spam.install')) ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-primary">Install Rspamd</button></form><?php endif; ?>
                <?php else: ?>
                    <form method="post" action="<?= e($this->url('mail.spam')) ?>" data-nim-busy>
                        <?= $this->csrfField() ?>
                        <div class="form-check form-switch mb-2"><input class="form-check-input" type="checkbox" role="switch" name="enabled" value="1" id="spam-on"<?= !empty($spam['enabled']) ? ' checked' : '' ?>><label class="form-check-label" for="spam-on">Filter incoming mail</label></div>
                        <div class="row g-2 mb-2">
                            <div class="col-6"><label class="form-label small" for="spam-mark">Junk from score</label><input class="form-control form-control-sm<?= isset($errors['add_header']) ? ' is-invalid' : '' ?>" id="spam-mark" type="number" step="0.5" min="1" max="50" name="add_header" value="<?= e((string) ($spam['add_header'] ?? 6) ?: '6') ?>"></div>
                            <div class="col-6"><label class="form-label small" for="spam-rej">Refuse from score</label><input class="form-control form-control-sm" id="spam-rej" type="number" step="0.5" min="2" max="100" name="reject" value="<?= e((string) ($spam['reject'] ?? 15) ?: '15') ?>"></div>
                        </div>
                        <?php if (isset($errors['add_header'])): ?><div class="text-danger mb-2"><?= e($errors['add_header']) ?></div><?php endif; ?>
                        <div class="form-check mb-2"><input class="form-check-input" type="checkbox" name="confirm_replace" value="1" id="spam-replace"><label class="form-check-label" for="spam-replace">Replace existing Rspamd settings in local.d not made by NimVor</label></div>
                        <button class="btn btn-sm btn-primary">Save</button>
                        <div class="form-text">Lower scores catch more spam but risk false positives. Refused mail bounces to its sender while connected, so nothing is lost silently.</div>
                    </form>
                <?php endif; ?>
            </div>
    <?= $this->modalEnd() ?>
    <?= $this->modalStart('webmail', 'Webmail', isset($errors['webmail_hostname']), 'modal-md') ?>
        <div class="small">
                <?php if (empty($wm['installed'])): ?>
                    <p class="text-body-secondary">Roundcube lets users read and send mail in the browser with their mailbox address and password.</p>
                    <?php if ($this->can(P::PACKAGES_MANAGE)): ?><form method="post" action="<?= e($this->url('mail.webmail.install')) ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-primary">Install Roundcube</button></form><?php endif; ?>
                <?php else: ?>
                    <?php if (!empty($wm['enabled']) && !empty($wm['hostname'])): ?>
                        <p class="mb-2"><a href="https://<?= e((string) $wm['hostname']) ?>/" target="_blank" rel="noopener">https://<?= e((string) $wm['hostname']) ?>/</a><?php if (!empty($wm['php'])): ?> <span class="text-body-secondary">(PHP <?= e((string) $wm['php']) ?>)</span><?php endif; ?></p>
                    <?php endif; ?>
                    <form method="post" action="<?= e($this->url('mail.webmail')) ?>" data-nim-busy>
                        <?= $this->csrfField() ?>
                        <div class="form-check form-switch mb-2"><input class="form-check-input" type="checkbox" role="switch" name="enabled" value="1" id="webmail-on"<?= !empty($wm['enabled']) ? ' checked' : '' ?>><label class="form-check-label" for="webmail-on">Webmail on</label></div>
                        <label class="form-label small" for="webmail-host">Host name</label>
                        <input class="form-control form-control-sm mb-2<?= isset($errors['webmail_hostname']) ? ' is-invalid' : '' ?>" id="webmail-host" name="hostname" placeholder="webmail.example.com" value="<?= e((string) ($old['hostname'] ?? ($wm['hostname'] ?? '') ?: ($status['hostname'] ?? ''))) ?>">
                        <?php if (isset($errors['webmail_hostname'])): ?><div class="text-danger mb-2"><?= e($errors['webmail_hostname']) ?></div><?php endif; ?>
                        <button class="btn btn-sm btn-primary">Save</button>
                        <div class="form-text">Point the host name's DNS at this server. A Let's Encrypt certificate for it is used when one exists; otherwise a self-signed one until you issue it and save again.</div>
                    </form>
                <?php endif; ?>
            </div>
    <?= $this->modalEnd() ?>
<?php endif; ?>
<?php if ($ready): ?>
    <?= $this->modalStart('mail-add', 'Host email for a domain', isset($errors['website_id']) || isset($errors['domain']), 'modal-md') ?>
                <form method="post" action="<?= e($this->url('mail.store')) ?>" data-nim-busy>
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
    <?= $this->modalEnd() ?>
<?php endif; ?>
