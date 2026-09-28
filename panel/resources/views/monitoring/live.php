<?php
/** @var \WhaCP\View\View $this */
?>
<?php $this->start(); ?>Monitoring<?php $this->end('title'); ?>
<?php $this->start(); ?>Live statistics of this server<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
<button class="btn btn-sm btn-outline-secondary" type="button" data-wcp-live-toggle aria-pressed="false"><i class="bi bi-pause-fill me-1" aria-hidden="true"></i><span>Pause</span></button>
<?php $this->end('actions'); ?>
<nav class="wcp-pagetabs" aria-label="Monitoring"><a href="<?= e($this->url('monitoring')) ?>">History and alerts</a><a class="active" href="<?= e($this->url('monitoring.live')) ?>" aria-current="page">Live</a></nav>
<div data-wcp-live="<?= e($this->url('monitoring.live.data')) ?>">
    <p class="small text-body-secondary" data-live-status aria-live="polite">Loading…</p>
    <div class="row g-3 mb-3" data-live-tiles>
        <?php foreach (['cpu' => 'CPU', 'mem' => 'Memory', 'swap' => 'Swap', 'load' => 'Load (1 / 5 / 15 min)', 'uptime' => 'Uptime', 'procs' => 'Processes'] as $k => $label): ?>
            <div class="col-6 col-md-4 col-xl-2"><div class="card h-100"><div class="card-body"><div class="small text-body-secondary"><?= e($label) ?></div><div class="fs-5 fw-semibold" data-live="<?= e($k) ?>">—</div><div class="small text-body-secondary" data-live="<?= e($k) ?>-sub"></div></div></div></div>
        <?php endforeach; ?>
    </div>
    <div class="row g-3 mb-3">
        <div class="col-xl-5">
            <div class="card h-100">
                <div class="card-header"><i class="bi bi-cpu me-1"></i>CPU</div>
                <div class="card-body small">
                    <dl class="row mb-2">
                        <dt class="col-5">Model</dt><dd class="col-7" data-live="cpu-model">—</dd>
                        <dt class="col-5">Architecture</dt><dd class="col-7" data-live="cpu-arch">—</dd>
                        <dt class="col-5">Sockets / cores / threads</dt><dd class="col-7" data-live="cpu-topology">—</dd>
                        <dt class="col-5">Frequency</dt><dd class="col-7" data-live="cpu-freq">—</dd>
                    </dl>
                    <div class="fw-medium mb-1">Use per logical CPU</div>
                    <div class="wcp-live-cores" data-live-cores></div>
                </div>
            </div>
        </div>
        <div class="col-xl-7">
            <div class="card h-100">
                <div class="card-header"><i class="bi bi-hdd-network me-1"></i>Network (bandwidth)</div>
                <div class="wcp-dtable table-responsive">
                    <table class="table table-sm align-middle mb-0">
                        <thead><tr><th>Interface</th><th class="text-end">In now</th><th class="text-end">Out now</th><th class="text-end">Received</th><th class="text-end">Sent</th><th class="text-end">Errors / dropped</th></tr></thead>
                        <tbody data-live-net></tbody>
                    </table>
                </div>
                <div class="card-footer small text-body-secondary">Totals since the interface came up. History: <a href="<?= e($this->url('monitoring')) ?>">History and alerts</a>.</div>
            </div>
        </div>
    </div>
    <div class="row g-3 mb-3">
        <div class="col-xl-6">
            <div class="card h-100">
                <div class="card-header"><i class="bi bi-lightning me-1"></i>Busiest processes (CPU)</div>
                <div class="wcp-dtable table-responsive"><table class="table table-sm align-middle mb-0"><thead><tr><th>Program</th><th>User</th><th class="text-end">PID</th><th class="text-end">CPU</th><th class="text-end">Memory</th></tr></thead><tbody data-live-topcpu></tbody></table></div>
            </div>
        </div>
        <div class="col-xl-6">
            <div class="card h-100">
                <div class="card-header"><i class="bi bi-memory me-1"></i>Largest processes (memory)</div>
                <div class="wcp-dtable table-responsive"><table class="table table-sm align-middle mb-0"><thead><tr><th>Program</th><th>User</th><th class="text-end">PID</th><th class="text-end">CPU</th><th class="text-end">Memory</th></tr></thead><tbody data-live-topmem></tbody></table></div>
            </div>
        </div>
    </div>
    <div class="card">
        <div class="card-header"><i class="bi bi-hdd me-1"></i>Disks</div>
        <div class="wcp-dtable table-responsive"><table class="table table-sm align-middle mb-0"><thead><tr><th>Mount</th><th>Device</th><th class="text-end">Used</th><th class="text-end">Free</th><th class="text-end">Size</th><th class="wcp-w-25"><span class="visually-hidden">Use</span></th></tr></thead><tbody data-live-disks></tbody></table></div>
    </div>
</div>
