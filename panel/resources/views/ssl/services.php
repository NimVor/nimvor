<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed>|null $svc
 */
use WhaCP\Security\Permissions as P;

$canManage = $this->can(P::SERVER_MANAGE);
$les = (array) ($svc['lets_encrypt'] ?? []);
$hostname = (string) ($svc['hostname'] ?? '');
$hasHostCert = false;
foreach ($les as $le) {
    $hasHostCert = $hasHostCert || in_array($hostname, (array) $le['names'], true);
}
$mt = (array) ($svc['mail_tls'] ?? []);
?>
<?= $this->partial('ssl/_tabs', get_defined_vars()) ?>
<?php if ($svc === null) { return; } ?>

<div class="wcp-infobar">
    <i class="bi bi-hdd-network" aria-hidden="true"></i>
    <span>Host name <strong class="font-monospace"><?= e($hostname !== '' ? $hostname : 'unknown') ?></strong>:
        <?php if ($hasHostCert): ?><span class="wcp-state is-ok">Let's Encrypt certificate present</span>
        <?php else: ?><span class="wcp-state is-warn">no Let's Encrypt certificate</span><?php endif; ?></span>
    <?php if ($canManage && !$hasHostCert && $hostname !== ''): ?>
        <form class="ms-auto" method="post" action="<?= e($this->url('ssl.services.hostname')) ?>" data-wcp-noajax data-wcp-busy data-wcp-confirm="Ask Let's Encrypt for a certificate for <?= e($hostname) ?>? The name must point to this server and port 80 must be reachable.">
            <?= $this->csrfField() ?><button class="btn btn-sm btn-primary"<?= empty($svc['certbot_installed']) ? ' disabled title="Install certbot on the Packages page first"' : '' ?>><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Get a certificate for the host name</button>
        </form>
    <?php endif; ?>
</div>

<?php $acct = $account ?? null; ?>
<?php if ($acct !== null): ?>
    <?php
    $registered = !empty($acct['registered']);
    $acctEmail = (string) ($acct['email'] ?? '');
    $providers = (array) ($acct['providers'] ?? []);
    $shown = (string) ($acct['provider'] ?? 'letsencrypt');
    $label = (string) ($acct['label'] ?? 'Let\'s Encrypt');
    $requiresEab = !empty($acct['requires_eab']);
    $active = ($activeProvider ?? 'letsencrypt') === $shown;
    $isLe = $shown === 'letsencrypt';
    ?>
    <div class="card mb-3" id="lets-encrypt-account">
        <div class="card-header d-flex align-items-center flex-wrap gap-2"><i class="bi bi-person-badge me-1"></i>Certificate authority
            <span class="ms-auto d-flex gap-2 align-items-center">
                <?= $registered ? $this->statusBadge('active', 'registered') : $this->statusBadge('warning', 'not registered') ?>
                <?php if ($active): ?><span class="badge rounded-pill text-bg-primary">active</span><?php endif; ?>
            </span>
        </div>
        <div class="card-body small">
            <?php if ($canManage && count($providers) > 1): ?>
                <form method="get" action="<?= e($this->url('ssl.services')) ?>" class="d-flex flex-wrap align-items-end gap-2 mb-3">
                    <div>
                        <label class="form-label" for="le-provider">Provider</label>
                        <select class="form-select form-select-sm" id="le-provider" name="provider" onchange="this.form.submit()">
                            <?php foreach ($providers as $p): $p = (array) $p; ?>
                                <option value="<?= e((string) $p['id']) ?>"<?= (string) $p['id'] === $shown ? ' selected' : '' ?>><?= e((string) $p['label']) ?><?= (string) $p['id'] === ($activeProvider ?? 'letsencrypt') ? ' (active)' : '' ?></option>
                            <?php endforeach; ?>
                        </select>
                    </div>
                    <noscript><button class="btn btn-sm btn-outline-secondary">Show</button></noscript>
                </form>
            <?php endif; ?>
            <?php if (!$registered): ?>
                <div class="alert alert-warning d-flex gap-2 mb-3" role="status">
                    <i class="bi bi-exclamation-triangle" aria-hidden="true"></i>
                    <div>The system has no saved <?= e($label) ?> account. Register one below before issuing certificates. Use a real email address — a reserved/example domain is refused, which otherwise makes every certificate fail.<?= $requiresEab ? ' ' . e($label) . ' also needs External Account Binding credentials from your ' . e($label) . ' account.' : '' ?></div>
                </div>
            <?php else: ?>
                <dl class="wcp-dl mb-3">
                    <dt>Contact email</dt><dd><?= $acctEmail !== '' ? e($acctEmail) : '<span class="text-body-secondary">none — expiry notices cannot be sent</span>' ?></dd>
                    <?php if (!empty($acct['account_uri'])): ?><dt>Account</dt><dd class="font-monospace text-truncate"><?= e((string) $acct['account_uri']) ?></dd><?php endif; ?>
                </dl>
            <?php endif; ?>
            <?php if ($canManage): ?>
                <form method="post" action="<?= e($this->url('ssl.services.account')) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <input type="hidden" name="provider" value="<?= e($shown) ?>">
                    <div class="row g-2 align-items-end">
                        <div class="col-sm-6">
                            <label class="form-label" for="le-acct-email"><?= $registered ? 'Contact email' : 'Account email' ?></label>
                            <input class="form-control form-control-sm" id="le-acct-email" type="email" name="email" maxlength="254" required value="<?= e($acctEmail !== '' ? $acctEmail : (string) ($defaultEmail ?? '')) ?>" placeholder="you@your-real-domain.com">
                        </div>
                        <div class="col-sm-6">
                            <div class="form-check"><input class="form-check-input" type="checkbox" value="1" name="agree_tos" id="le-acct-tos" required><label class="form-check-label" for="le-acct-tos">I agree to the <a href="<?= e((string) ($acct['terms_url'] ?? '#')) ?>" target="_blank" rel="noopener"><?= e($label) ?> Terms of Service</a></label></div>
                            <?php if ($isLe): ?><div class="form-check"><input class="form-check-input" type="checkbox" value="1" name="staging" id="le-acct-staging"><label class="form-check-label" for="le-acct-staging">Staging (test) environment</label></div><?php endif; ?>
                        </div>
                    </div>
                    <?php if ($requiresEab): ?>
                        <div class="row g-2 mt-1">
                            <div class="col-sm-6">
                                <label class="form-label" for="le-eab-kid">EAB key ID</label>
                                <input class="form-control form-control-sm font-monospace" id="le-eab-kid" name="eab_kid" autocomplete="off" placeholder="from your <?= e($label) ?> account"<?= $registered ? '' : ' required' ?>>
                            </div>
                            <div class="col-sm-6">
                                <label class="form-label" for="le-eab-hmac">EAB HMAC key</label>
                                <input class="form-control form-control-sm font-monospace" id="le-eab-hmac" name="eab_hmac_key" type="password" autocomplete="off" placeholder="kept only to register, never stored"<?= $registered ? '' : ' required' ?>>
                            </div>
                        </div>
                        <div class="form-text mb-1">Get these from <?= e($label) ?> → Developer → EAB credentials. They are used once to register and are not stored.</div>
                    <?php endif; ?>
                    <button class="btn btn-sm btn-primary mt-3"<?= empty($acct['certbot_installed']) ? ' disabled title="Install certbot on the Packages page first"' : '' ?>><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= $registered ? 'Update registration' : 'Register account' ?><?= $registered && !$active ? ' and use this provider' : '' ?></button>
                    <?php if ($registered): ?><span class="text-body-secondary ms-2">Registering makes this the active certificate authority for new and automatic certificates.</span><?php endif; ?>
                </form>
            <?php endif; ?>
        </div>
    </div>
<?php endif; ?>

<?php
$serviceCard = function (string $key, string $title, string $icon, ?array $s, string $missing, string $note) use ($les, $canManage, $hostname): void {
    $c = $s['certificate'] ?? null; ?>
    <div class="card h-100">
        <div class="card-header"><i class="bi bi-<?= e($icon) ?> me-1"></i><?= e($title) ?></div>
        <div class="card-body small">
            <?php if ($c === null): ?>
                <p class="wcp-muted mb-0"><?= e($missing) ?></p>
            <?php else: ?>
                <?= $this->partial('ssl/_cert', ['c' => $c, 'warnDays' => 14]) ?>
                <div class="font-monospace wcp-muted text-break mt-1"><?= e((string) $c['file']) ?></div>
                <?php if ($canManage): ?>
                    <form class="d-flex gap-2 mt-3" method="post" action="<?= e($this->url('ssl.services.cert')) ?>" data-wcp-busy>
                        <?= $this->csrfField() ?><input type="hidden" name="service" value="<?= e($key) ?>">
                        <select class="form-select form-select-sm" name="cert_name" aria-label="Certificate for <?= e($title) ?>" required>
                            <?php if ($les === []): ?><option value="">No Let's Encrypt certificate yet</option><?php endif; ?>
                            <?php foreach ($les as $le): ?><option value="<?= e((string) $le['name']) ?>"<?= in_array($hostname, (array) $le['names'], true) ? ' selected' : '' ?>><?= e((string) $le['name']) ?> (<?= e(implode(', ', array_slice(array_filter((array) $le['names']), 0, 3))) ?>; <?= (int) $le['days_remaining'] ?> days)</option><?php endforeach; ?>
                        </select>
                        <button class="btn btn-sm btn-outline-primary text-nowrap"<?= $les === [] ? ' disabled' : '' ?>><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Use</button>
                    </form>
                    <form class="mt-2" method="post" action="<?= e($this->url('ssl.services.cert')) ?>" data-wcp-confirm="Go back to the certificate this service used before?">
                        <?= $this->csrfField() ?><input type="hidden" name="service" value="<?= e($key) ?>"><input type="hidden" name="cert_name" value="">
                        <button class="btn btn-sm btn-link p-0">Use the previous certificate again</button>
                    </form>
                <?php endif; ?>
            <?php endif; ?>
            <p class="wcp-muted mt-3 mb-0"><?= e($note) ?></p>
        </div>
    </div>
<?php };
?>
<div class="row g-3">
    <div class="col-xl-6"><?php $serviceCard('panel', 'NimVor panel (web services)', 'window', $svc['panel'] ?? null, 'The panel is not served over HTTPS by Nginx on this server (it was installed without --public-https; reach it through an SSH tunnel).', 'The panel\'s HTTPS uses the server-wide TLS configuration (protocols and ciphers on the TLS configuration tab) once a certificate is chosen here.'); ?></div>
    <div class="col-xl-6"><?php $serviceCard('mail', 'Mail server (SMTP, submission, IMAP)', 'envelope', $svc['mail'] ?? null, 'Email is not set up on this server yet (Email → Mail server).', 'Postfix and Dovecot use the same certificate. Mail programs check that it covers the server name they connect to (usually the mail host name).'); ?></div>
    <?php if (isset($svc['mail'])): ?>
    <div class="col-xl-6">
        <form class="card h-100" method="post" action="<?= e($this->url('ssl.services.mail_tls')) ?>" data-wcp-busy data-wcp-confirm="Apply this TLS configuration to Postfix and Dovecot? Both are checked and reloaded; if a check fails nothing changes.">
            <?= $this->csrfField() ?>
            <div class="card-header"><i class="bi bi-shield-lock me-1"></i>Mail server TLS (protocols and ciphers)</div>
            <div class="card-body">
                <fieldset class="mb-3"><legend class="form-label small fw-semibold">Protocol versions (IMAP and submission)</legend>
                    <?php foreach (['tls12' => ['TLS 1.2 and 1.3', 'Works with every mail program still in use.'], 'tls13' => ['TLS 1.3 only', 'Strongest; older mail programs and phones can no longer sign in.']] as $v => [$l, $h]): ?>
                        <div class="form-check"><input class="form-check-input" type="radio" name="protocols" value="<?= $v ?>" id="mt-p-<?= $v ?>"<?= ($mt['protocols'] ?? 'tls12') === $v ? ' checked' : '' ?><?= $canManage ? '' : ' disabled' ?>><label class="form-check-label" for="mt-p-<?= $v ?>"><?= e($l) ?><div class="form-text mt-0"><?= e($h) ?></div></label></div>
                    <?php endforeach; ?>
                </fieldset>
                <fieldset><legend class="form-label small fw-semibold">Ciphers</legend>
                    <?php foreach (['default' => ['Programs\' defaults', 'Postfix\'s and Dovecot\'s own lists.'], 'intermediate' => ['Modern ciphers only (Mozilla "intermediate")', 'Forward secrecy and authenticated encryption only; recommended.']] as $v => [$l, $h]): ?>
                        <div class="form-check"><input class="form-check-input" type="radio" name="ciphers" value="<?= $v ?>" id="mt-c-<?= $v ?>"<?= ($mt['ciphers'] ?? 'default') === $v ? ' checked' : '' ?><?= $canManage ? '' : ' disabled' ?>><label class="form-check-label" for="mt-c-<?= $v ?>"><?= e($l) ?><div class="form-text mt-0"><?= e($h) ?></div></label></div>
                    <?php endforeach; ?>
                </fieldset>
                <p class="small wcp-muted mt-3 mb-0">Port 25 (mail from other servers) keeps accepting what the sending server offers: refusing it would make it deliver without encryption.</p>
            </div>
            <?php if ($canManage): ?><div class="card-footer text-end"><button class="btn btn-primary btn-sm"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Apply</button></div><?php endif; ?>
        </form>
    </div>
    <?php endif; ?>
    <div class="col-xl-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-signpost-split me-1"></i>Where each SSL setting is</div>
            <div class="card-body small">
                <table class="table table-sm align-top mb-0"><tbody>
                    <tr><th class="fw-semibold w-40" scope="row">Websites' SSL ciphers</th><td>TLS configuration tab (Nginx serves every website and webmail; there is no Apache).</td></tr>
                    <tr><th class="fw-semibold w-40" scope="row">Web services (panel) SSL</th><td>The panel card on this page; its ciphers follow the TLS configuration tab.</td></tr>
                    <tr><th class="fw-semibold w-40" scope="row">Mail server SSL and ciphers</th><td>The mail cards on this page.</td></tr>
                    <tr><th class="fw-semibold w-40" scope="row">Host name SSL</th><td>The host name bar above; renewed automatically like every Let's Encrypt certificate.</td></tr>
                    <tr><th class="fw-semibold w-40" scope="row">Web Disk SSL</th><td>NimVor has no Web Disk (WebDAV) service; files are reached over SFTP, FTPS and the File Manager.</td></tr>
                </tbody></table>
            </div>
        </div>
    </div>
</div>
