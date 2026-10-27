<?php
/**
 * @var \WhaCP\View\View $this
 * @var list<array<string, mixed>> $keys
 * @var list<string> $keyTypes
 * @var array<string, mixed>|null $key
 * @var list<array<string, mixed>> $websites
 */
use WhaCP\Security\Permissions as P;

$canManage = $this->can(P::SSL_MANAGE);
$typeLabels = ['ecdsa256' => 'ECDSA P-256 (recommended)', 'ecdsa384' => 'ECDSA P-384', 'rsa2048' => 'RSA 2048', 'rsa3072' => 'RSA 3072', 'rsa4096' => 'RSA 4096'];
// Websites a key's names fit, first.
$fits = static function (array $w) use ($key): bool {
    foreach ((array) ($key['names'] ?? []) as $n) {
        if (strcasecmp((string) $n, (string) $w['domain']) === 0) {
            return true;
        }
    }
    return false;
};
?>
<?= $this->partial('ssl/_tabs', get_defined_vars()) ?>

<?php if ($key !== null): ?>
<div class="card mb-3" id="key">
    <div class="card-header d-flex flex-wrap align-items-center gap-2"><span><i class="bi bi-key me-1"></i>Key <span class="font-monospace"><?= e((string) $key['id']) ?></span></span><a class="ms-auto small" href="<?= e($this->url('ssl.keys')) ?>">Close</a></div>
    <div class="card-body">
        <dl class="row small mb-3">
            <dt class="col-sm-3 fw-normal wcp-muted">Names</dt><dd class="col-sm-9 font-monospace"><?= e(implode(', ', (array) $key['names'])) ?></dd>
            <dt class="col-sm-3 fw-normal wcp-muted">Subject</dt><dd class="col-sm-9 font-monospace text-break"><?= e((string) $key['subject']) ?></dd>
            <dt class="col-sm-3 fw-normal wcp-muted">Key</dt><dd class="col-sm-9"><?= e($typeLabels[$key['key_type']] ?? (string) $key['key_type']) ?> · public key SHA-256 <span class="font-monospace text-break"><?= e((string) $key['public_key_sha256']) ?></span></dd>
            <dt class="col-sm-3 fw-normal wcp-muted">Private key</dt><dd class="col-sm-9">Stored on this server only (root, mode 0600). It is not shown or downloaded.</dd>
        </dl>
        <?php if (!empty($key['csr'])): ?>
            <div class="d-flex align-items-center gap-2 mb-1"><span class="fw-semibold small">Certificate signing request</span>
                <button type="button" class="btn btn-sm btn-outline-secondary ms-auto" data-wcp-copy="#csr-pem"><i class="bi bi-clipboard me-1"></i>Copy</button>
                <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('ssl.keys.csr', [], ['id' => (string) $key['id']])) ?>"><i class="bi bi-download me-1"></i>Download</a></div>
            <textarea class="form-control form-control-sm font-monospace mb-3" id="csr-pem" rows="8" readonly aria-label="CSR"><?= e((string) $key['csr']) ?></textarea>
        <?php endif; ?>
        <?php if ($canManage): ?>
        <form method="post" action="<?= e($this->url('ssl.keys.install')) ?>" data-wcp-busy>
            <?= $this->csrfField() ?><input type="hidden" name="key_id" value="<?= e((string) $key['id']) ?>">
            <h2 class="h6">Install on a website</h2>
            <div class="row g-2">
                <div class="col-md-6"><label class="form-label small" for="ik-site">Website</label>
                    <select class="form-select form-select-sm" id="ik-site" name="website_id" required>
                        <option value="">Choose…</option>
                        <?php foreach ([true, false] as $pass): foreach ($websites as $w): if ($fits($w) !== $pass) { continue; } ?><option value="<?= (int) $w['id'] ?>"><?= e((string) $w['domain']) ?></option><?php endforeach; endforeach; ?>
                    </select></div>
                <?php if (!empty($key['self_signed'])): ?>
                <div class="col-md-6 d-flex align-items-end"><div class="form-check"><input class="form-check-input" type="checkbox" name="self_signed" value="1" id="ik-self"><label class="form-check-label small" for="ik-self">Use the self-signed certificate made with this key (browsers warn)</label></div></div>
                <?php endif; ?>
                <div class="col-12"><label class="form-label small" for="ik-cert">Certificate from the certificate authority (PEM)</label><textarea class="form-control form-control-sm font-monospace" id="ik-cert" name="certificate" rows="5" placeholder="-----BEGIN CERTIFICATE-----"></textarea></div>
                <div class="col-12"><label class="form-label small" for="ik-chain">Intermediate certificates (CA bundle, optional)</label><textarea class="form-control form-control-sm font-monospace" id="ik-chain" name="chain" rows="4"></textarea></div>
            </div>
            <div class="form-text mb-2">Checked before installing: the certificate belongs to this key, covers the website's domain, is valid now and each certificate in the chain signs the one before it. Nginx is checked before it reloads.</div>
            <button class="btn btn-primary btn-sm"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Install and enable HTTPS</button>
        </form>
        <?php endif; ?>
    </div>
</div>
<?php endif; ?>

<div class="row g-3">
    <div class="<?= $canManage ? 'col-xl-7' : 'col-12' ?>">
        <div class="wcp-panel h-100 mb-0">
            <div class="wcp-actionbar"><div class="fw-semibold"><i class="bi bi-key me-1"></i>Keys made on this server <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($keys) ?></span></div></div>
            <div class="wcp-dtable table-responsive">
                <table class="table align-middle small">
                    <thead><tr><th>Names</th><th>Key</th><th>Created</th><th>Has</th><th class="text-end">Operate</th></tr></thead>
                    <tbody>
                    <?php foreach ($keys as $k): ?>
                        <tr>
                            <td class="font-monospace text-break"><a href="<?= e($this->url('ssl.keys', [], ['id' => (string) $k['id']])) ?>#key"><?= e(implode(', ', (array) $k['names'])) ?></a></td>
                            <td><?= e((string) $k['key_type']) ?></td>
                            <td class="text-nowrap"><?= e(substr((string) $k['created'], 0, 10)) ?></td>
                            <td><?= !empty($k['has_csr']) ? 'CSR' : '' ?><?= !empty($k['self_signed']) ? ' · self-signed' : '' ?></td>
                            <td class="text-end text-nowrap"><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('ssl.keys', [], ['id' => (string) $k['id']])) ?>#key">Open</a>
                                <?php if ($canManage): ?><form class="d-inline" method="post" action="<?= e($this->url('ssl.keys.delete')) ?>" data-wcp-confirm="Delete this private key and its CSR? A certificate issued for it can then no longer be installed from here. Certificates already installed keep working."><?= $this->csrfField() ?><input type="hidden" name="id" value="<?= e((string) $k['id']) ?>"><button class="btn btn-sm btn-outline-danger">Delete</button></form><?php endif; ?></td>
                        </tr>
                    <?php endforeach; ?>
                    <?php if ($keys === []): ?><tr><td colspan="5" class="text-center py-4 wcp-muted">No keys yet. Create one to request a certificate from a certificate authority.</td></tr><?php endif; ?>
                    </tbody>
                </table>
            </div>
        </div>
    </div>
    <?php if ($canManage): ?>
    <div class="col-xl-5">
        <form class="card h-100" method="post" action="<?= e($this->url('ssl.keys.create')) ?>" data-wcp-busy>
            <?= $this->csrfField() ?>
            <div class="card-header"><i class="bi bi-plus-lg me-1"></i>New key and CSR</div>
            <div class="card-body">
                <div class="mb-2"><label class="form-label small" for="csr-names">Domain names</label><input class="form-control form-control-sm font-monospace" id="csr-names" name="names" required placeholder="example.com, www.example.com"><div class="form-text">The first is the common name; separate with commas or spaces. *.example.com requests a wildcard.</div></div>
                <div class="row g-2 mb-2">
                    <div class="col-sm-6"><label class="form-label small" for="csr-o">Organization</label><input class="form-control form-control-sm" id="csr-o" name="organization" maxlength="64"></div>
                    <div class="col-sm-6"><label class="form-label small" for="csr-ou">Department</label><input class="form-control form-control-sm" id="csr-ou" name="organizational_unit" maxlength="64"></div>
                    <div class="col-sm-5"><label class="form-label small" for="csr-l">City</label><input class="form-control form-control-sm" id="csr-l" name="locality" maxlength="64"></div>
                    <div class="col-sm-4"><label class="form-label small" for="csr-st">State / province</label><input class="form-control form-control-sm" id="csr-st" name="province" maxlength="64"></div>
                    <div class="col-sm-3"><label class="form-label small" for="csr-c">Country</label><input class="form-control form-control-sm text-uppercase" id="csr-c" name="country" maxlength="2" placeholder="DE"></div>
                    <div class="col-sm-7"><label class="form-label small" for="csr-e">Email (optional)</label><input class="form-control form-control-sm" id="csr-e" type="email" name="email"></div>
                    <div class="col-sm-5"><label class="form-label small" for="csr-k">Key</label><select class="form-select form-select-sm" id="csr-k" name="key_type"><?php foreach ($keyTypes as $t): ?><option value="<?= e($t) ?>"<?= $t === 'ecdsa256' ? ' selected' : '' ?>><?= e($typeLabels[$t] ?? $t) ?></option><?php endforeach; ?></select></div>
                </div>
                <div class="form-check"><input class="form-check-input" type="checkbox" name="self_signed" value="1" id="csr-ss"><label class="form-check-label small" for="csr-ss">Also make a self-signed certificate, valid for</label></div>
                <div class="input-group input-group-sm w-auto mb-2"><input class="form-control" type="number" min="1" max="825" name="self_signed_days" value="365" aria-label="Days"><span class="input-group-text">days</span></div>
                <div class="form-text">Self-signed certificates are for testing and internal names; browsers do not trust them.</div>
            </div>
            <div class="card-footer text-end"><button class="btn btn-primary btn-sm"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Create key and CSR</button></div>
        </form>
    </div>
    <?php endif; ?>
</div>
