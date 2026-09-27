<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $site */
$canManage = $this->can(\WhaCP\Security\Permissions::SSL_MANAGE);
$id = (int) $site['id'];
$st = null;
if ($site['https'] && $cert !== null) {
    $st = !empty($cert['error']) ? 'error' : (!empty($cert['expired']) ? 'expired' : ((int) $cert['days_remaining'] <= $warnDays ? 'expiring' : 'valid'));
}
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'ssl']) ?>

<?php if ($renewal !== null && empty($renewal['certbot_installed'])): ?>
    <div class="alert alert-warning small"><i class="bi bi-exclamation-triangle me-1"></i>certbot is not installed on this server. Install the <code>certbot</code> package to use Let's Encrypt.</div>
<?php endif; ?>

<?php if (!$site['https'] && in_array((string) ($site['auto_ssl'] ?? ''), ['pending', 'gave_up', 'failed'], true)): ?>
    <div class="alert <?= $site['auto_ssl'] === 'pending' ? 'alert-info' : 'alert-warning' ?> d-flex gap-2 small" role="status">
        <i class="bi bi-<?= $site['auto_ssl'] === 'pending' ? 'hourglass-split' : 'exclamation-triangle' ?>" aria-hidden="true"></i>
        <div><strong>Automatic SSL<?= $site['auto_ssl'] === 'pending' ? '' : ' stopped' ?>:</strong> <?= e((string) $site['auto_ssl_note']) ?>
            <?php if ($site['auto_ssl'] === 'pending'): ?> The certificate is issued automatically once the domain's name servers are this panel's or its address is this server's.<?php endif; ?></div>
    </div>
<?php elseif (($site['auto_ssl'] ?? '') === 'done' && $site['https']): ?>
    <div class="small text-body-secondary mb-2"><i class="bi bi-magic me-1"></i><?= e((string) $site['auto_ssl_note']) ?></div>
<?php endif; ?>
<div class="card mb-3" id="certificate">
    <div class="card-body">
        <div class="d-flex flex-wrap align-items-center gap-3">
            <span class="wcp-feature-icon" aria-hidden="true"><i class="bi bi-<?= $site['https'] ? 'shield-lock' : 'shield-exclamation' ?>"></i></span>
            <div class="flex-grow-1 wcp-minw-0">
                <?php if (!$site['https']): ?>
                    <div class="fw-semibold">HTTPS is not enabled</div>
                    <div class="small text-body-secondary">Visitors connect without encryption and browsers mark the site “Not secure”.</div>
                <?php elseif ($cert === null): ?>
                    <div class="fw-semibold">HTTPS is enabled, but the certificate could not be read</div>
                    <div class="small text-body-secondary">Issue or upload a certificate again below.</div>
                <?php else: ?>
                    <div class="fw-semibold">
                        <?php if ($st === 'valid'): ?><span class="wcp-state is-ok">Secure</span><?php elseif ($st === 'expiring'): ?><span class="wcp-state is-warn">Expires soon</span><?php else: ?><span class="wcp-state is-error"><?= $st === 'expired' ? 'Expired' : 'Certificate problem' ?></span><?php endif; ?>
                        <span class="fw-normal text-body-secondary small ms-2"><?= $site['ssl_mode'] === 'letsencrypt' ? "Let's Encrypt" : 'Uploaded certificate' ?> · expires <?= e(gmdate('Y-m-d', (int) strtotime((string) $cert['not_after']))) ?> (<?= (int) $cert['days_remaining'] ?> days)</span>
                    </div>
                    <div class="small text-body-secondary">Covers <?= e(implode(', ', (array) ($cert['dns_names'] ?? []))) ?></div>
                <?php endif; ?>
            </div>
            <?php if ($canManage && !$site['https']): ?>
                <a class="btn btn-primary" href="#issue"><i class="bi bi-magic me-1"></i>Install free certificate</a>
            <?php elseif ($canManage && $site['ssl_mode'] === 'letsencrypt'): ?>
                <form method="post" action="<?= e($this->url('sites.ssl.renew', ['id' => $id])) ?>" data-wcp-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><span class="spinner-border spinner-border-sm d-none" aria-hidden="true"></span> Renew now</button></form>
            <?php endif; ?>
        </div>
        <?php if ($site['https'] && $cert !== null): ?>
            <dl class="wcp-dl small mt-3 mb-0">
                <dt>Issuer</dt><dd><?= e($cert['issuer'] ?? '') ?></dd>
                <dt>Auto-renewal</dt><dd><?= $site['ssl_mode'] === 'letsencrypt' ? (!empty($renewal['timer_active']) ? $this->statusBadge('active', 'active (' . e($renewal['method']) . ')') : $this->statusBadge('warning', 'not scheduled')) : '<span class="text-body-secondary">manual (uploaded certificate)</span>' ?></dd>
            </dl>
        <?php endif; ?>
    </div>
</div>

<div class="row g-3">
    <?php if ($canManage): ?>
    <div class="col-lg-6">
        <div class="card mb-3" id="issue">
            <div class="card-header"><i class="bi bi-magic me-1"></i><?= $site['https'] ? 'Re-issue with' : 'Free certificate from' ?> Let's Encrypt</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('sites.ssl.issue', ['id' => $id])) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <p class="small mb-2"><i class="bi bi-envelope me-1 text-body-secondary" aria-hidden="true"></i>Expiry notices from Let's Encrypt go to <?= $defaultEmail !== '' ? '<strong>' . e($defaultEmail) . '</strong>' : 'nobody (your account has no email address)' ?>, the email address of your account.</p>
                    <?php if ($site['aliases'] !== []): ?>
                        <div class="form-check small"><input type="hidden" name="include_aliases" value="0"><input class="form-check-input" type="checkbox" name="include_aliases" value="1" id="le-al" checked><label class="form-check-label" for="le-al">Include aliases (<?= e(implode(', ', $site['aliases'])) ?>)</label></div>
                    <?php endif; ?>
                    <div class="form-check small mb-3"><input class="form-check-input" type="checkbox" name="staging" value="1" id="le-st"><label class="form-check-label" for="le-st">Use the staging environment (test, untrusted certificate)</label></div>
                    <button class="btn btn-sm btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Issue certificate</button>
                    <p class="small text-body-secondary mt-2 mb-0">All domains must point to this server and port 80 must be open. Issuing takes up to a minute.</p>
                </form>
            </div>
        </div>
        <?php if ($site['https']): ?>
            <div class="card mb-3" id="https-options">
                <div class="card-header"><i class="bi bi-sliders me-1"></i>HTTPS options</div>
                <div class="card-body">
                    <form method="post" action="<?= e($this->url('sites.ssl.options', ['id' => $id])) ?>" data-wcp-busy>
                        <?= $this->csrfField() ?>
                        <div class="form-check form-switch"><input type="hidden" name="force_https" value="0"><input class="form-check-input" type="checkbox" role="switch" name="force_https" value="1" id="o-force"<?= $site['force_https'] ? ' checked' : '' ?>><label class="form-check-label" for="o-force">Redirect HTTP to HTTPS</label></div>
                        <div class="form-check form-switch mb-3"><input type="hidden" name="hsts" value="0"><input class="form-check-input" type="checkbox" role="switch" name="hsts" value="1" id="o-hsts"<?= $site['hsts'] ? ' checked' : '' ?>><label class="form-check-label" for="o-hsts">HSTS (browsers refuse plain HTTP for one year)</label></div>
                        <button class="btn btn-sm btn-primary" type="submit">Save</button>
                    </form>
                </div>
            </div>
        <?php endif; ?>
    </div>
    <div class="col-lg-6">
        <details class="card mb-3" id="upload-certificate">
            <summary class="card-header"><i class="bi bi-upload me-1"></i>Upload your own certificate</summary>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('sites.ssl.custom', ['id' => $id])) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <label class="form-label small" for="c-cert">Certificate (PEM)</label>
                    <textarea class="form-control form-control-sm font-monospace mb-2" id="c-cert" name="certificate" rows="3" placeholder="-----BEGIN CERTIFICATE-----"></textarea>
                    <label class="form-label small" for="c-key">Private key (PEM)</label>
                    <textarea class="form-control form-control-sm font-monospace mb-2" id="c-key" name="private_key" rows="3" placeholder="-----BEGIN PRIVATE KEY-----" autocomplete="off"></textarea>
                    <label class="form-label small" for="c-chain">Intermediate chain (optional)</label>
                    <textarea class="form-control form-control-sm font-monospace mb-3" id="c-chain" name="chain" rows="2"></textarea>
                    <button class="btn btn-sm btn-outline-primary" type="submit">Validate and install</button>
                </form>
            </div>
        </details>
        <?php if ($site['https']): ?>
            <div class="card wcp-danger-card">
                <div class="card-header"><i class="bi bi-exclamation-octagon me-1"></i>Danger zone</div>
                <div class="card-body">
                    <p class="small text-body-secondary">Turn HTTPS off. Visitors using https:// get errors until it is on again.</p>
                    <form method="post" action="<?= e($this->url('sites.ssl.disable', ['id' => $id])) ?>" data-wcp-confirm="Disable HTTPS for <?= e($site['domain']) ?>? Visitors using https:// will get errors.">
                        <?= $this->csrfField() ?>
                        <?php if ($site['ssl_mode'] === 'letsencrypt'): ?><input type="hidden" name="delete_certificate" value="1"><?php endif; ?>
                        <button class="btn btn-sm btn-outline-danger">Disable HTTPS</button>
                    </form>
                </div>
            </div>
        <?php endif; ?>
    </div>
    <?php endif; ?>
</div>
