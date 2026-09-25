<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $status */
use WhaCP\Security\Permissions as P;

$st = (array) ($status['stats'] ?? []);
$rt = (array) ($status['runtime'] ?? []);
$cfg = (array) ($status['settings'] ?? []);
$hits = (int) ($st['get_hits'] ?? 0);
$misses = (int) ($st['get_misses'] ?? 0);
$limit = (int) ($st['limit_maxbytes'] ?? 0);
$used = (int) ($st['bytes'] ?? 0);
$pct = $limit > 0 ? min(100, round(100 * $used / $limit, 1)) : 0;
$uptime = (int) ($st['uptime'] ?? 0);
?>
<?php $this->start(); ?>Memcached<?php $this->end('title'); ?>
<?php $this->start(); ?>Distributed memory object cache<?php $this->end('subtitle'); ?>
<?php if ($agentError !== null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError]) ?>
<?php elseif (empty($status['installed'])): ?>
    <div class="card"><div class="card-body">
        <p>Memcached is not installed on this server.</p>
        <?php if ($this->can(P::PACKAGES_MANAGE)): ?>
            <form method="post" action="<?= e($this->url('memcached.install')) ?>"><?= $this->csrfField() ?><button class="btn btn-primary"><i class="bi bi-download me-1"></i>Install Memcached</button></form>
            <p class="small text-body-secondary mt-2 mb-0">The distribution package listens on 127.0.0.1 only. PHP websites also need the <code>memcached</code> PHP extension (PHP → extensions).</p>
        <?php endif; ?>
    </div></div>
<?php else: ?>
<div class="row g-3">
    <div class="col-lg-6">
        <div class="card mb-3">
            <div class="card-header d-flex align-items-center"><i class="bi bi-memory me-1"></i>Status
                <span class="ms-auto"><?= !empty($status['active']) && empty($status['error']) ? $this->statusBadge('active', 'running') : $this->statusBadge('failed', 'not running') ?></span></div>
            <div class="card-body">
                <?php if (!empty($status['error'])): ?><div class="alert alert-warning small"><?= e($status['error']) ?></div><?php endif; ?>
                <?php if ($st !== []): ?>
                <div class="mb-3">
                    <div class="d-flex small mb-1"><span>Memory</span><span class="ms-auto"><?= e(format_bytes($used)) ?> of <?= e(format_bytes($limit)) ?></span></div>
                    <div class="progress" role="progressbar" aria-label="Memory used" aria-valuenow="<?= (int) $pct ?>" aria-valuemin="0" aria-valuemax="100"><div class="progress-bar<?= $pct > 90 ? ' bg-warning' : '' ?>" data-width="<?= (int) $pct ?>"></div></div>
                </div>
                <dl class="wcp-dl">
                    <dt>Version</dt><dd><?= e($st['version'] ?? '') ?></dd>
                    <dt>Uptime</dt><dd><?= e(intdiv($uptime, 86400) . 'd ' . gmdate('H:i', $uptime % 86400)) ?></dd>
                    <dt>Items</dt><dd><?= e(number_format((int) ($st['curr_items'] ?? 0))) ?></dd>
                    <dt>Hit ratio</dt><dd><?= $hits + $misses > 0 ? e(round(100 * $hits / ($hits + $misses), 1) . ' %') : '—' ?> <span class="small text-body-secondary">(<?= e(number_format($hits)) ?> hits, <?= e(number_format($misses)) ?> misses)</span></dd>
                    <dt>Evictions</dt><dd><?= e(number_format((int) ($st['evictions'] ?? 0))) ?></dd>
                    <dt>Connections</dt><dd><?= e((string) ($st['curr_connections'] ?? 0)) ?> of <?= e((string) ($rt['maxconns'] ?? $cfg['max_conns'] ?? '')) ?></dd>
                    <dt>Listening on</dt><dd><code><?= e(implode(', ', (array) ($cfg['listen'] ?? []))) ?>:<?= (int) ($cfg['port'] ?? 11211) ?></code></dd>
                </dl>
                <?php endif; ?>
                <?php if ($this->can(P::SERVICES_MANAGE)): ?>
                    <form method="post" action="<?= e($this->url('memcached.restart')) ?>" class="d-inline"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-repeat me-1"></i>Restart</button></form>
                <?php endif; ?>
            </div>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-sliders me-1"></i>Settings</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('memcached.configure')) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <div class="row g-2">
                        <div class="col-sm-6"><label class="form-label small" for="mc-mem">Memory (MB)</label><input class="form-control" id="mc-mem" type="number" name="memory_mb" min="16" max="262144" value="<?= (int) ($cfg['memory_mb'] ?? 64) ?>"></div>
                        <div class="col-sm-6"><label class="form-label small" for="mc-conn">Max. connections</label><input class="form-control" id="mc-conn" type="number" name="max_conns" min="16" max="65536" value="<?= (int) ($cfg['max_conns'] ?? 1024) ?>"></div>
                    </div>
                    <label class="form-label small mt-2" for="mc-listen">Listen addresses</label>
                    <input class="form-control font-monospace" id="mc-listen" name="listen" value="<?= e(implode(', ', (array) ($cfg['listen'] ?? ['127.0.0.1']))) ?>">
                    <div class="form-text">Loopback only (127.0.0.1, ::1): Memcached has no password, so it must never be reachable from the network.</div>
                    <div class="form-check mt-3"><input class="form-check-input" type="checkbox" name="confirm" value="1" id="mc-confirm" required><label class="form-check-label small" for="mc-confirm">Allow WhaCP to edit <code><?= e($status['config_file'] ?? '') ?></code> (it belongs to the Memcached package; a backup is kept and it is restored if Memcached does not come back)</label></div>
                    <button class="btn btn-primary mt-3">Save and restart</button>
                </form>
            </div>
        </div>
        <div class="card">
            <div class="card-header"><i class="bi bi-eraser me-1"></i>Flush</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('memcached.flush')) ?>" class="row g-2 align-items-end">
                    <?= $this->csrfField() ?>
                    <div class="col"><label class="form-label small" for="mc-flush">Type <strong>flush</strong> to remove every cached item</label><input class="form-control" id="mc-flush" name="confirm" autocomplete="off"></div>
                    <div class="col-auto"><button class="btn btn-outline-danger">Flush all</button></div>
                </form>
            </div>
        </div>
    </div>
</div>
<?php endif; ?>
