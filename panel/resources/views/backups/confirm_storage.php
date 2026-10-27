<?php
/**
 * @var \WhaCP\View\View $this
 * @var string $name
 * @var string $where
 * @var array<string, string> $trust
 */
$ssh = isset($trust['host_key']);
?>
<?php $this->start(); ?>Trust this server?<?php $this->end('title'); ?>
<?php $this->start(); ?>Storage <?= e($name) ?><?php $this->end('subtitle'); ?>
<div class="row justify-content-center">
    <div class="col-lg-8">
        <div class="card">
            <div class="card-body">
                <p>The connection to <strong><?= e($where) ?></strong> works. This is the first connection to it, so confirm that the
                    <?= $ssh ? 'SSH host key' : 'TLS certificate' ?> below really belongs to your server — ask its provider or compare it on the server itself.</p>
                <?php if ($ssh): ?>
                    <dl class="row small mb-3">
                        <dt class="col-sm-3">Fingerprint</dt><dd class="col-sm-9"><code id="trust-fp"><?= e($trust['fingerprint'] ?? '') ?></code></dd>
                        <dt class="col-sm-3">Key type</dt><dd class="col-sm-9"><code><?= e(strtok($trust['host_key'], ' ') ?: '') ?></code></dd>
                    </dl>
                    <p class="small text-body-secondary">On the server: <code>ssh-keygen -lf /etc/ssh/ssh_host_<?= e(str_replace(['ssh-', 'ecdsa-sha2-nistp256', 'ecdsa-sha2-nistp384', 'ecdsa-sha2-nistp521'], ['', 'ecdsa', 'ecdsa', 'ecdsa'], (string) strtok($trust['host_key'], ' '))) ?>_key.pub</code></p>
                <?php else: ?>
                    <dl class="row small mb-3">
                        <dt class="col-sm-3">Certificate</dt><dd class="col-sm-9"><code><?= e($trust['tls_subject'] ?? '') ?></code></dd>
                        <dt class="col-sm-3">Public key pin</dt><dd class="col-sm-9"><code id="trust-fp"><?= e($trust['tls_pin'] ?? '') ?></code></dd>
                    </dl>
                    <p class="small text-body-secondary">The certificate is not signed by a trusted authority (for example it is self-signed), so WhaCP pins its public key: backups stop if the server ever presents a different key.</p>
                <?php endif; ?>
                <form method="post" action="<?= e($this->url('backups.storages.trust')) ?>" class="d-flex gap-2" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <button class="btn btn-primary" name="trust" value="1"><i class="bi bi-shield-check me-1"></i>Trust and save</button>
                    <button class="btn btn-outline-secondary" name="cancel" value="1" formnovalidate>Cancel</button>
                </form>
            </div>
        </div>
    </div>
</div>
