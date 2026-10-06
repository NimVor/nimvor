<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $plans */
/** @var list<string> $phpVersions */
/** @var bool $canReseller */
/** @var string $spf */
/** @var list<string> $nameservers */
/** @var array<string, mixed> $defaults */
$errors ??= [];
$old ??= [];
$sent = $old !== [];
$v = static fn (string $k, string $d = ''): string => (string) ($old[$k] ?? $d);
// Switches: the defaults on a new form, what was sent after an error.
$on = static fn (string $k): bool => $sent ? filter_var($old[$k] ?? false, FILTER_VALIDATE_BOOL) : (bool) ($defaults[$k] ?? false);
$err = static fn (string $k): string => isset($errors[$k]) ? '<div class="invalid-feedback d-block">' . e((string) $errors[$k]) . '</div>' : '';
$cls = static fn (string $k): string => isset($errors[$k]) ? ' is-invalid' : '';
$switch = function (string $name, string $label, string $help = '') use ($on): string {
    ob_start(); ?>
    <div class="form-check form-switch mb-2">
        <input type="hidden" name="<?= e($name) ?>" value="0">
        <input class="form-check-input" type="checkbox" role="switch" id="a-<?= e($name) ?>" name="<?= e($name) ?>" value="1"<?= $on($name) ? ' checked' : '' ?>>
        <label class="form-check-label" for="a-<?= e($name) ?>"><?= $label ?></label>
        <?php if ($help !== ''): ?><div class="form-text mt-0"><?= $help ?></div><?php endif; ?>
    </div>
    <?php return (string) ob_get_clean();
};
$routing = $v('mail_routing', (string) $defaults['mail_routing']);
?>
<?php $this->start(); ?>Create a new account<?php $this->end('title'); ?>
<?php $this->start(); ?>A hosting account with its first website, DNS, email and SSL<?php $this->end('subtitle'); ?>


<form method="post" action="<?= e($this->url('accounts.store')) ?>" data-wcp-busy novalidate id="new-account">
    <?= $this->csrfField() ?>
    <div class="row g-3">
        <div class="col-xl-7">
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-person-badge me-1"></i>Domain and login</div>
                <div class="card-body">
                    <div class="row g-3">
                        <div class="col-md-6">
                            <label class="form-label" for="a-domain">Domain</label>
                            <input class="form-control<?= $cls('domain') ?>" id="a-domain" name="domain" value="<?= e($v('domain')) ?>" placeholder="example.com" autocomplete="off" spellcheck="false">
                            <?= $err('domain') ?>
                            <div class="form-text">Optional: the account's first website (with www).</div>
                        </div>
                        <div class="col-md-6">
                            <label class="form-label" for="a-username">Username</label>
                            <input class="form-control<?= $cls('username') ?>" id="a-username" name="username" value="<?= e($v('username')) ?>" required maxlength="31" autocomplete="off" spellcheck="false">
                            <?= $err('username') ?>
                            <div class="form-text">For signing in; also the website's system account.</div>
                        </div>
                        <div class="col-md-6">
                            <label class="form-label" for="a-password">Password</label>
                            <div class="input-group">
                                <input class="form-control font-monospace<?= $cls('password') ?>" type="password" id="a-password" name="password" autocomplete="new-password" required data-wcp-password>
                                <button class="btn btn-outline-secondary" type="button" data-wcp-genpass="#a-password" data-wcp-genpass-confirm="#a-password2" title="Generate a strong password"><i class="bi bi-magic"></i> Generate</button>
                            </div>
                            <?= $err('password') ?>
                        </div>
                        <div class="col-md-6">
                            <label class="form-label" for="a-password2">Confirm password</label>
                            <input class="form-control font-monospace<?= $cls('password_confirmation') ?>" type="password" id="a-password2" name="password_confirmation" autocomplete="new-password" required>
                            <?= $err('password_confirmation') ?>
                        </div>
                        <div class="col-md-6">
                            <label class="form-label" for="a-email">Contact email</label>
                            <input class="form-control<?= $cls('email') ?>" type="email" id="a-email" name="email" value="<?= e($v('email')) ?>" required maxlength="190">
                            <?= $err('email') ?>
                            <div class="form-text">Also for Let's Encrypt expiry notices.</div>
                        </div>
                        <div class="col-md-6">
                            <label class="form-label" for="a-php">PHP version</label>
                            <select class="form-select" id="a-php" name="php_version">
                                <?php foreach ($phpVersions as $pv): ?><option value="<?= e($pv) ?>"<?= $v('php_version') === $pv ? ' selected' : '' ?>>PHP <?= e($pv) ?></option><?php endforeach; ?>
                                <?php if ($phpVersions === []): ?><option value="">No PHP-FPM installed</option><?php endif; ?>
                            </select>
                        </div>
                    </div>
                </div>
            </div>

            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-box-seam me-1"></i>Package</div>
                <div class="card-body">
                    <label class="form-label" for="a-plan">Hosting plan</label>
                    <select class="form-select<?= $cls('plan_id') ?>" id="a-plan" name="plan_id">
                        <option value="">No plan (unlimited)</option>
                        <?php foreach ($plans as $p): ?><option value="<?= (int) $p['id'] ?>"<?= $v('plan_id') === (string) $p['id'] ? ' selected' : '' ?>><?= e($p['name']) ?><?= $p['description'] !== '' ? ' — ' . e($p['description']) : '' ?></option><?php endforeach; ?>
                    </select>
                    <?= $err('plan_id') ?>
                    <div class="form-text">Disk, bandwidth, websites, domains, databases, email and FTP accounts. <a href="<?= e($this->url('plans')) ?>">Manage plans</a></div>
                </div>
            </div>

            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-envelope me-1"></i>Mail</div>
                <div class="card-body">
                    <?= $switch('spam_filter', 'Enable the spam filter (Rspamd)', 'Scans incoming mail for the account\'s domains when the server\'s spam filter is on.') ?>
                    <?= $switch('spam_box', 'Enable Spam Box', 'Mail marked as spam goes to the mailbox\'s Junk folder instead of the inbox.') ?>
                    <div class="fw-medium mt-3 mb-1">Mail routing</div>
                    <?php foreach (['auto' => ['Automatically detect configuration (recommended)', 'Local when the domain\'s MX record points here.'], 'local' => ['Local mail exchanger', 'This server receives the domain\'s mail.'], 'backup' => ['Backup mail exchanger', 'Another server receives the mail; nothing is stored here.'], 'remote' => ['Remote mail exchanger', 'Another server receives the mail; no mailboxes here.']] as $k => [$label, $help]): ?>
                        <div class="form-check">
                            <input class="form-check-input" type="radio" name="mail_routing" id="a-mr-<?= e($k) ?>" value="<?= e($k) ?>"<?= $routing === $k ? ' checked' : '' ?>>
                            <label class="form-check-label" for="a-mr-<?= e($k) ?>"><?= e($label) ?> <span class="small text-body-secondary">— <?= e($help) ?></span></label>
                        </div>
                    <?php endforeach; ?>
                </div>
            </div>
        </div>

        <div class="col-xl-5">
            <?php if ($canReseller): ?>
                <div class="card mb-3">
                    <div class="card-header"><i class="bi bi-people me-1"></i>Reseller settings</div>
                    <div class="card-body">
                        <div class="form-check form-switch">
                            <input type="hidden" name="reseller" value="0">
                            <input class="form-check-input" type="checkbox" role="switch" id="a-reseller" name="reseller" value="1"<?= filter_var($old['reseller'] ?? false, FILTER_VALIDATE_BOOL) ? ' checked' : '' ?>>
                            <label class="form-check-label" for="a-reseller">Make the account a reseller</label>
                            <div class="form-text mt-0">Resellers create and manage their own hosting customers and plans; their plan caps them and their customers together. A reseller always manages its own account.</div>
                        </div>
                        <?= $err('reseller') ?>
                    </div>
                </div>
            <?php endif; ?>
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-signpost me-1"></i>DNS settings</div>
                <div class="card-body">
                    <?= $switch('dns_dkim', 'Enable DKIM on this account', 'Signs the domain\'s mail; needs email hosted here.') ?>
                    <?= $switch('dns_spf', 'Enable SPF on this account', '<code class="text-break">' . e($spf) . '</code>') ?>
                    <?= $switch('dns_dmarc', 'Enable DMARC on this account', '<code>v=DMARC1; p=none;</code> — needs DKIM and SPF.') ?>
                    <?= $switch('dns_registrar_ns', 'Use the name servers at the domain\'s registrar', 'No DNS zone is created here (locally set name servers are ignored).') ?>
                    <?= $switch('dns_overwrite', 'Overwrite an existing DNS zone of the account', 'Replaces the records of a zone that exists for the domain.') ?>
                    <?php if ($nameservers === []): ?>
                        <div class="alert alert-warning small mb-0 mt-2">No name servers are set, so no DNS zone is created. Set them under Settings → DNS name servers.</div>
                    <?php else: ?>
                        <div class="form-text">Name servers: <?= e(implode(', ', $nameservers)) ?></div>
                    <?php endif; ?>
                </div>
            </div>
            <div class="small text-body-secondary mb-3">The website starts with a “coming soon” page and gets a Let's Encrypt certificate automatically once its domain points to this server (if automatic SSL is on under Settings).</div>
        </div>
    </div>
    <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Create account</button>
</form>
