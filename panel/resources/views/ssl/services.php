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
        <form class="ms-auto" method="post" action="<?= e($this->url('ssl.services.hostname')) ?>" data-wcp-busy data-wcp-confirm="Ask Let's Encrypt for a certificate for <?= e($hostname) ?>? The name must point to this server and port 80 must be reachable.">
            <?= $this->csrfField() ?><button class="btn btn-sm btn-primary"<?= empty($svc['certbot_installed']) ? ' disabled title="Install certbot on the Packages page first"' : '' ?>><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Get a certificate for the host name</button>
        </form>
    <?php endif; ?>
</div>

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
    <div class="col-xl-6"><?php $serviceCard('panel', 'WhaCP panel (web services)', 'window', $svc['panel'] ?? null, 'The panel is not served over HTTPS by Nginx on this server (it was installed without --public-https; reach it through an SSH tunnel).', 'The panel\'s HTTPS uses the server-wide TLS configuration (protocols and ciphers on the TLS configuration tab) once a certificate is chosen here.'); ?></div>
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
                <dl class="row mb-0">
                    <dt class="col-sm-5">Websites' SSL ciphers</dt><dd class="col-sm-7">TLS configuration tab (Nginx serves every website and webmail; there is no Apache).</dd>
                    <dt class="col-sm-5">Web services (panel) SSL</dt><dd class="col-sm-7">The panel card on this page; its ciphers follow the TLS configuration tab.</dd>
                    <dt class="col-sm-5">Mail server SSL and ciphers</dt><dd class="col-sm-7">The mail cards on this page.</dd>
                    <dt class="col-sm-5">Host name SSL</dt><dd class="col-sm-7">The host name bar above; renewed automatically like every Let's Encrypt certificate.</dd>
                    <dt class="col-sm-5">Web Disk SSL</dt><dd class="col-sm-7">WhaCP has no Web Disk (WebDAV) service; files are reached over SFTP, FTPS and the File Manager.</dd>
                </dl>
            </div>
        </div>
    </div>
</div>
