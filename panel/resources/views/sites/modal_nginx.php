<?php
/**
 * The Nginx window of the Websites list: timeouts, buffers, compression and a few allowlisted directives of
 * one website. Empty fields keep the server-wide value (Server > Nginx). Saved without leaving the list.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 */
use NimVor\Services\SiteNginxService;

$s = (array) ($site['nginx_settings'] ?? []);
$id = (int) $site['id'];
$gzip = !array_key_exists('gzip', $s) ? '' : ($s['gzip'] ? 'on' : 'off');
$hints = [
    'read_timeout_seconds' => $site['kind'] === 'proxy' ? 'proxy_read_timeout (default 300)' : 'fastcgi_read_timeout (default: PHP max execution + 10)',
    'send_timeout_seconds' => 'send_timeout', 'body_timeout_seconds' => 'client_body_timeout', 'keepalive_seconds' => 'keepalive_timeout',
    'fastcgi_buffer_kb' => 'fastcgi_buffer_size', 'gzip_level' => 'gzip_comp_level', 'rate_limit_kbps' => 'limit_rate',
];
?>
<form method="post" action="<?= e($this->url('sites.nginx.update', ['id' => $id])) ?>" data-nim-site-modal-form novalidate>
    <?= $this->csrfField() ?>
    <div class="modal-header">
        <h2 class="modal-title h5"><i class="bi bi-hdd-network me-1"></i>Nginx <span class="text-body-secondary fw-normal">· <?= e($site['domain']) ?></span></h2>
        <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
    </div>
    <div class="modal-body">
        <div class="alert alert-danger small d-none" data-nim-modal-error role="alert"></div>
        <p class="small text-body-secondary">Leave a field empty to use the server-wide value. The configuration is tested before it is kept; if Nginx refuses it, the website keeps its previous settings.</p>
        <div class="row g-3">
            <?php foreach (SiteNginxService::NUMBERS as $k => [$label]): ?>
                <?php if ($k === 'fastcgi_buffer_kb' && $site['kind'] !== 'php') { continue; } ?>
                <?php if ($k === 'gzip_level'): ?>
                    <div class="col-sm-6 col-md-4">
                        <label class="form-label small" for="mn-gzip">Compression (gzip)</label>
                        <select class="form-select form-select-sm" id="mn-gzip" name="gzip">
                            <option value="">Server-wide</option>
                            <option value="on"<?= $gzip === 'on' ? ' selected' : '' ?>>On for this website</option>
                            <option value="off"<?= $gzip === 'off' ? ' selected' : '' ?>>Off for this website</option>
                        </select>
                    </div>
                <?php endif; ?>
                <div class="col-sm-6 col-md-4">
                    <label class="form-label small" for="mn-<?= e($k) ?>"><?= e($label) ?></label>
                    <input class="form-control form-control-sm" type="number" id="mn-<?= e($k) ?>" name="<?= e($k) ?>" value="<?= e((string) ($s[$k] ?? '')) ?>" placeholder="server-wide">
                    <div class="form-text font-monospace"><?= e($hints[$k] ?? '') ?></div>
                </div>
            <?php endforeach; ?>
            <div class="col-12">
                <label class="form-label small" for="mn-extra">More directives <span class="text-body-secondary">(one per line)</span></label>
                <textarea class="form-control form-control-sm font-monospace" id="mn-extra" name="extra" rows="4" spellcheck="false" placeholder="client_body_buffer_size 256k"><?= e(implode("\n", array_map('strval', (array) ($s['extra'] ?? [])))) ?></textarea>
                <div class="form-text">Allowed: <?= e(implode(', ', SiteNginxService::EXTRA)) ?>. Values may use letters, digits and <code>. _ : / * + -</code> only.</div>
            </div>
        </div>
    </div>
    <div class="modal-footer">
        <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
        <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save and apply</button>
    </div>
</form>
