<?php
/**
 * Per-site Memcached: connection details and status. Memcached is a shared,
 * local, unauthenticated cache, so this page is informational — there is no
 * per-site isolation to manage and no global flush from here.
 *
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $site
 * @var array<string, mixed>|null $status
 */
$status ??= null;
$cfg = (array) ($status['config'] ?? []);
$port = (int) ($cfg['port'] ?? 11211);
$installed = $status !== null && !empty($status['installed']);
$running = $installed && !empty($status['active']) && empty($status['error']);
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'memcached']) ?>

<?php if ($status === null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError ?? null]) ?>
    <?php return; ?>
<?php endif; ?>

<p class="small text-body-secondary">Memcached is a fast in-memory cache your application can use (for sessions, query caches and the like). It runs locally on this server and is shared by the sites hosted here.</p>

<?php if (!$installed): ?>
    <div class="card"><div class="wcp-empty-state">
        <i class="bi bi-hdd-stack" aria-hidden="true"></i>
        <div class="fw-medium">Memcached is not available on this server</div>
        <div class="small">Ask your administrator to enable Memcached if your application needs it.</div>
    </div></div>
    <?php return; ?>
<?php endif; ?>

<div class="wcp-infobar mb-3">
    <i class="bi bi-hdd-stack" aria-hidden="true"></i>
    <span class="me-auto">Memcached is <strong><?= $running ? 'running' : 'not running' ?></strong>.</span>
    <?= $running ? $this->statusBadge('active', 'running') : $this->statusBadge('failed', 'stopped') ?>
</div>

<div class="card mb-3">
    <div class="card-header"><i class="bi bi-plug me-1" aria-hidden="true"></i>Connect your application</div>
    <div class="card-body">
        <dl class="row mb-0 small">
            <dt class="col-sm-3">Host</dt><dd class="col-sm-9"><code>127.0.0.1</code></dd>
            <dt class="col-sm-3">Port</dt><dd class="col-sm-9"><code><?= e((string) $port) ?></code></dd>
            <dt class="col-sm-3">Authentication</dt><dd class="col-sm-9">None (local connections only)</dd>
        </dl>
        <hr>
        <p class="small text-body-secondary mb-2">PHP needs the <code>memcached</code> extension (enable it under PHP → Extensions). A quick test:</p>
        <pre class="small bg-body-tertiary border rounded p-2 mb-0"><code>$m = new Memcached();
$m->addServer('127.0.0.1', <?= (int) $port ?>);
$m->set('hello', 'world', 60);
echo $m->get('hello');</code></pre>
    </div>
</div>

<div class="card">
    <div class="card-body small text-body-secondary">
        <i class="bi bi-info-circle me-1" aria-hidden="true"></i>
        Memcached has no per-site separation and no password: pick your own key prefix (for example your domain) so your keys don't clash with another site's. The cache is cleared whenever the service restarts.
    </div>
</div>
