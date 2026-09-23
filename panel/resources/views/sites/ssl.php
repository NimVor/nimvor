<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $site */
$canManage = $this->can(\WhaCP\Security\Permissions::SSL_MANAGE);
$id = (int) $site['id'];
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'ssl']) ?>

<?php if ($renewal !== null && empty($renewal['certbot_installed'])): ?>
    <div class="alert alert-warning small"><i class="bi bi-exclamation-triangle me-1"></i>certbot is not installed on this server. Install the <code>certbot</code> package to use Let's Encrypt.</div>
<?php endif; ?>

<div class="row g-3">
    <div class="col-lg-6">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-shield-lock me-1"></i>Certificate</div>
            <div class="card-body">
                <?php if (!$site['https']): ?>
                    <p class="text-body-secondary mb-0">HTTPS is not enabled for this website.</p>
                <?php elseif ($cert === null): ?>
                    <p class="text-warning mb-0">HTTPS is enabled but the certificate could not be read.</p>
                <?php else: ?>
                    <?php $st = !empty($cert['error']) ? 'error' : (!empty($cert['expired']) ? 'expired' : ((int) $cert['days_remaining'] <= $warnDays ? 'expiring' : 'valid')); ?>
                    <dl class="wcp-dl">
                        <dt>Status</dt><dd><?= $this->statusBadge($st) ?></dd>
                        <dt>Type</dt><dd><?= $site['ssl_mode'] === 'letsencrypt' ? "Let's Encrypt" : 'Uploaded certificate' ?></dd>
                        <dt>Covers</dt><dd class="small"><?= e(implode(', ', (array) ($cert['dns_names'] ?? []))) ?></dd>
                        <dt>Issuer</dt><dd class="small"><?= e($cert['issuer'] ?? '') ?></dd>
                        <dt>Expires</dt><dd><?= e(gmdate('Y-m-d', (int) strtotime((string) $cert['not_after']))) ?> (<?= (int) $cert['days_remaining'] ?> days)</dd>
                        <dt>Auto-renewal</dt><dd><?= $site['ssl_mode'] === 'letsencrypt' ? (!empty($renewal['timer_active']) ? $this->statusBadge('active', 'active (' . e($renewal['method']) . ')') : $this->statusBadge('warning', 'not scheduled')) : '<span class="text-body-secondary">manual</span>' ?></dd>
                    </dl>
                <?php endif; ?>
            </div>
            <?php if ($canManage && $site['https']): ?>
                <div class="card-footer d-flex flex-wrap gap-2">
                    <?php if ($site['ssl_mode'] === 'letsencrypt'): ?>
                        <form method="post" action="<?= e($this->url('sites.ssl.renew', ['id' => $id])) ?>" data-wcp-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><span class="spinner-border spinner-border-sm d-none" aria-hidden="true"></span> Renew now</button></form>
                    <?php endif; ?>
                    <form method="post" action="<?= e($this->url('sites.ssl.disable', ['id' => $id])) ?>" data-wcp-confirm="Disable HTTPS for <?= e($site['domain']) ?>? Visitors using https:// will get errors.">
                        <?= $this->csrfField() ?>
                        <?php if ($site['ssl_mode'] === 'letsencrypt'): ?><input type="hidden" name="delete_certificate" value="1"><?php endif; ?>
                        <button class="btn btn-sm btn-outline-danger">Disable HTTPS</button>
                    </form>
                </div>
            <?php endif; ?>
        </div>
        <?php if ($canManage && $site['https']): ?>
            <div class="card">
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
    <?php if ($canManage): ?>
        <div class="col-lg-6">
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-magic me-1"></i><?= $site['https'] ? 'Re-issue with' : 'Enable HTTPS with' ?> Let's Encrypt</div>
                <div class="card-body">
                    <form method="post" action="<?= e($this->url('sites.ssl.issue', ['id' => $id])) ?>" data-wcp-busy>
                        <?= $this->csrfField() ?>
                        <label class="form-label small" for="le-email">Email for expiry notices (optional)</label>
                        <input class="form-control form-control-sm mb-2" type="email" id="le-email" name="email" value="<?= e($defaultEmail) ?>">
                        <?php if ($site['aliases'] !== []): ?>
                            <div class="form-check small"><input type="hidden" name="include_aliases" value="0"><input class="form-check-input" type="checkbox" name="include_aliases" value="1" id="le-al" checked><label class="form-check-label" for="le-al">Include aliases (<?= e(implode(', ', $site['aliases'])) ?>)</label></div>
                        <?php endif; ?>
                        <div class="form-check small mb-3"><input class="form-check-input" type="checkbox" name="staging" value="1" id="le-st"><label class="form-check-label" for="le-st">Use the staging environment (test, untrusted certificate)</label></div>
                        <button class="btn btn-sm btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Issue certificate</button>
                        <p class="small text-body-secondary mt-2 mb-0">All domains must point to this server and port 80 must be open. Issuing takes up to a minute.</p>
                    </form>
                </div>
            </div>
            <div class="card">
                <div class="card-header"><i class="bi bi-upload me-1"></i>Upload a certificate</div>
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
            </div>
        </div>
    <?php endif; ?>
</div>
