<?php
/** @var \NimVor\View\View $this */
/** @var array{private: string, public: string, fingerprint: string, user: string, encrypted: bool} $key */
/** @var string $sshPort */
/** @var string $serverName */
$file = 'nimvor-' . preg_replace('/[^A-Za-z0-9_-]/', '', $key['user']) . '-' . preg_replace('/[^A-Za-z0-9_-]/', '', $serverName) . '.pem';
$portArg = $sshPort !== '' && $sshPort !== '22' ? ' -p ' . $sshPort : '';
?>
<?php $this->start(); ?>New SSH key<?php $this->end('title'); ?>
<?php $this->start(); ?>Security · SSH<?php $this->end('subtitle'); ?>
<div class="alert alert-warning d-flex gap-2" role="alert">
    <i class="bi bi-exclamation-triangle-fill" aria-hidden="true"></i>
    <div><strong>Download the private key now.</strong> It is shown only on this page and is not kept anywhere on the server. If you lose it, remove the key and generate a new one.</div>
</div>
<div class="nim-panel mb-3">
    <p class="mb-2">The key is allowed for <strong><?= e($key['user']) ?></strong> (fingerprint <span class="font-monospace"><?= e($key['fingerprint']) ?></span>).
        <?= $key['encrypted'] ? 'It is protected with the passphrase you chose.' : '<span class="nim-warn">It has no passphrase: anyone who gets the file can sign in. Keep it safe.</span>' ?></p>
    <div class="d-flex flex-wrap gap-2 mb-3">
        <a class="btn btn-sm btn-primary" download="<?= e($file) ?>" href="data:application/x-pem-file;base64,<?= e(base64_encode($key['private'])) ?>"><i class="bi bi-download"></i> Download private key</a>
        <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('security.ssh')) ?>#ssh-keys">Done</a>
    </div>
    <label class="form-label small" for="k-private">Private key (<?= e($file) ?>)</label>
    <textarea class="form-control form-control-sm font-monospace mb-3" id="k-private" rows="8" readonly autocomplete="off" spellcheck="false"><?= e($key['private']) ?></textarea>
    <label class="form-label small" for="k-public">Public key (allowed on this server)</label>
    <textarea class="form-control form-control-sm font-monospace" id="k-public" rows="3" readonly><?= e($key['public']) ?></textarea>
</div>
<div class="nim-panel">
    <h2 class="h6">Sign in with it</h2>
    <p class="small mb-1"><strong>Linux, macOS, Windows (PowerShell):</strong> save the file in your <code>.ssh</code> folder (on Linux and macOS, then run <code>chmod 600 ~/.ssh/<?= e($file) ?></code>) and connect:</p>
    <pre class="small bg-body-tertiary p-2 rounded mb-2"><code>ssh -i ~/.ssh/<?= e($file) ?><?= e($portArg) ?> <?= e($key['user']) ?>@<?= e($serverName) ?></code></pre>
    <p class="small mb-1"><strong>PuTTY:</strong> open PuTTYgen, <em>Conversions → Import key</em>, choose the file, then <em>Save private key</em> (.ppk) and use it under <em>Connection → SSH → Auth → Credentials</em>.</p>
    <p class="small nim-muted mb-0">Test a new connection with the key before you turn off password logins on the SSH page.</p>
</div>
