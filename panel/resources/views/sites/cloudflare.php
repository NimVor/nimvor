<?php
/**
 * Site → Cloudflare: connect the customer's own token and manage their zone.
 *
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $site
 * @var array<string, mixed>|null $status
 * @var array<string, string> $sslModes
 */
$id = (int) $site['id'];
$status ??= null;
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'cloudflare']) ?>

<?php if ($status === null): ?>
    <p class="small text-body-secondary">Put <strong><?= e($site['domain']) ?></strong> behind Cloudflare's free CDN, caching and SSL. Create a Cloudflare <a href="https://dash.cloudflare.com/profile/api-tokens" target="_blank" rel="noopener noreferrer">API token</a> for your zone with the permissions <code>Zone:Read</code>, <code>DNS:Edit</code>, <code>Zone Settings:Edit</code> and <code>Cache Purge:Purge</code>, then paste it here. WhaCP stores it encrypted and uses it only for your domain.</p>
    <div class="card">
        <div class="card-header"><i class="bi bi-cloud me-1" aria-hidden="true"></i>Connect Cloudflare</div>
        <form class="card-body" method="post" action="<?= e($this->url('sites.cloudflare.connect', ['id' => $id])) ?>" data-wcp-busy>
            <?= $this->csrfField() ?>
            <label class="form-label" for="cf-token">Cloudflare API token</label>
            <div class="input-group">
                <input class="form-control font-monospace" id="cf-token" name="token" type="password" autocomplete="off" required placeholder="your Cloudflare API token">
                <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Connect</button>
            </div>
            <div class="form-text">The domain must already be added to your Cloudflare account, and the token must have access to its zone.</div>
        </form>
    </div>
    <?php return; ?>
<?php endif; ?>

<div class="wcp-infobar mb-3">
    <i class="bi bi-cloud-check text-success" aria-hidden="true"></i>
    <span class="me-auto">Connected to the Cloudflare zone <strong><?= e($status['zone_name']) ?></strong>.</span>
    <form method="post" action="<?= e($this->url('sites.cloudflare.disconnect', ['id' => $id])) ?>" data-wcp-busy data-wcp-confirm="Disconnect Cloudflare for this website? Your zone's settings at Cloudflare are left as they are.">
        <?= $this->csrfField() ?>
        <button class="btn btn-sm btn-outline-danger"><i class="bi bi-plug me-1" aria-hidden="true"></i>Disconnect</button>
    </form>
</div>

<?php if (!empty($status['error'])): ?>
    <div class="alert alert-warning small"><i class="bi bi-exclamation-triangle-fill me-1" aria-hidden="true"></i>Cloudflare could not be reached with your token: <?= e((string) $status['error']) ?> Reconnect with a token that has access to this zone.</div>
    <?php return; ?>
<?php endif; ?>

<div class="row g-3">
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-shield-lock me-1" aria-hidden="true"></i>SSL / TLS</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('sites.cloudflare.ssl', ['id' => $id])) ?>" data-wcp-busy class="row g-2 align-items-end">
                    <?= $this->csrfField() ?>
                    <div class="col-sm-8">
                        <label class="form-label small" for="cf-ssl">Encryption mode</label>
                        <select class="form-select" id="cf-ssl" name="mode">
                            <?php foreach ($sslModes as $k => $label): ?><option value="<?= e($k) ?>"<?= ($status['ssl'] ?? '') === $k ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                        </select>
                    </div>
                    <div class="col-sm-4"><button class="btn btn-outline-primary w-100">Save</button></div>
                </form>
                <p class="small text-body-secondary mt-2 mb-0">Use <strong>Full (strict)</strong> when your site already has a valid certificate (WhaCP's Let's Encrypt), so traffic is encrypted end to end.</p>
                <hr>
                <form method="post" action="<?= e($this->url('sites.cloudflare.always_https', ['id' => $id])) ?>" data-wcp-busy class="form-check form-switch mb-0">
                    <?= $this->csrfField() ?>
                    <input type="hidden" name="on" value="<?= !empty($status['always_https']) ? '0' : '1' ?>">
                    <input class="form-check-input" type="checkbox" role="switch" id="cf-ah" onchange="this.form.submit()"<?= !empty($status['always_https']) ? ' checked' : '' ?>>
                    <label class="form-check-label" for="cf-ah">Always Use HTTPS (redirect http:// to https://)</label>
                </form>
            </div>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-hdd-network me-1" aria-hidden="true"></i>Proxy &amp; cache</div>
            <div class="card-body">
                <p class="small text-body-secondary">Proxy (the orange cloud) routes visitors through Cloudflare for caching and protection. Turn it on for your domain's records:</p>
                <?php if (($status['records'] ?? []) === []): ?>
                    <p class="small text-body-secondary mb-0">No matching DNS records found in this zone for <?= e($site['domain']) ?>.</p>
                <?php else: ?>
                    <?php foreach ((array) $status['records'] as $rec): ?>
                        <form method="post" action="<?= e($this->url('sites.cloudflare.proxy', ['id' => $id])) ?>" data-wcp-busy class="d-flex align-items-center gap-2 py-1 border-top">
                            <?= $this->csrfField() ?>
                            <input type="hidden" name="record" value="<?= e((string) $rec['id']) ?>">
                            <input type="hidden" name="on" value="<?= !empty($rec['proxied']) ? '0' : '1' ?>">
                            <span class="small font-monospace flex-grow-1 text-truncate"><?= e((string) $rec['type']) ?> <?= e((string) $rec['name']) ?></span>
                            <div class="form-check form-switch mb-0">
                                <input class="form-check-input" type="checkbox" role="switch" onchange="this.form.submit()"<?= !empty($rec['proxied']) ? ' checked' : '' ?> aria-label="Proxy <?= e((string) $rec['name']) ?>">
                            </div>
                        </form>
                    <?php endforeach; ?>
                <?php endif; ?>
                <hr>
                <form method="post" action="<?= e($this->url('sites.cloudflare.purge', ['id' => $id])) ?>" data-wcp-busy data-wcp-confirm="Purge everything in Cloudflare's cache for <?= e($site['domain']) ?>?">
                    <?= $this->csrfField() ?>
                    <button class="btn btn-outline-secondary btn-sm"><i class="bi bi-trash3 me-1" aria-hidden="true"></i>Purge cache (everything)</button>
                </form>
            </div>
        </div>
    </div>
</div>
