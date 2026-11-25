<?php
/**
 * Server → Swap: what is in use and the controls to create, resize or remove NimVor's swap file.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed>|null $status swap.status
 * @var \NimVor\Agent\AgentException|null $agentError
 */
$s = (array) ($status ?? []);
$entries = (array) ($s['entries'] ?? []);
$canManage = $this->routeAllowed('server.swap.set');
$total = (int) ($s['swap_total_bytes'] ?? 0);
$used = (int) ($s['swap_used_bytes'] ?? 0);
$mem = (int) ($s['mem_total_bytes'] ?? 0);
$avail = (int) ($s['mem_available_bytes'] ?? 0);
$maxMb = (int) ($s['max_mb'] ?? 0);
$minMb = (int) ($s['min_mb'] ?? 256);
$managed = array_values(array_filter($entries, static fn ($e): bool => !empty(((array) $e)['managed'])));
$presets = array_filter([1, 2, 4, 8, 16], static fn (int $g): bool => ($maxMb <= 0 || $g * 1024 <= $maxMb) && $g * 1024 >= $minMb);
$bar = static fn (float $p): string => $p >= 90 ? 'bg-danger' : ($p >= 75 ? 'bg-warning' : '');
?>
<?php $this->start(); ?>Swap<?php $this->end('title'); ?>
<?php $this->start(); ?>Swap memory: what the server uses when it runs out of RAM<?php $this->end('subtitle'); ?>

<?php if ($agentError !== null || $status === null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php return; endif; ?>

<div class="row g-3 mb-3">
    <div class="col-md-6">
        <div class="nim-panel h-100 p-3">
            <div class="fw-semibold mb-2"><i class="bi bi-memory me-1" aria-hidden="true"></i>Memory</div>
            <dl class="nim-dl mb-0">
                <dt>RAM</dt><dd><?= e(format_bytes($mem)) ?> (<?= e(format_bytes($avail)) ?> available)</dd>
                <dt>Swap</dt><dd><?= $total > 0 ? e(format_bytes($used)) . ' used of ' . e(format_bytes($total)) : 'none' ?></dd>
                <dt>Swappiness</dt><dd><?= (int) ($s['swappiness'] ?? 0) ?> <span class="text-body-secondary small">(how eagerly the kernel uses swap; 60 is the usual default)</span></dd>
                <dt>File system</dt><dd><?= e((string) ($s['fs_type'] ?? '—')) ?><?php if (isset($s['fs_free_bytes']) && (int) $s['fs_free_bytes'] > 0): ?>, <?= e(format_bytes((int) $s['fs_free_bytes'])) ?> free<?php endif; ?></dd>
            </dl>
            <?php if ($total > 0): ?><div class="progress nim-progress-thin mt-3" role="progressbar" aria-label="Swap used" aria-valuenow="<?= $total > 0 ? (int) round($used * 100 / $total) : 0 ?>" aria-valuemin="0" aria-valuemax="100"><div class="progress-bar <?= e($bar($total > 0 ? $used * 100 / $total : 0)) ?>" data-width="<?= $total > 0 ? max(1, min(100, (int) round($used * 100 / $total))) : 0 ?>"></div></div><?php endif; ?>
        </div>
    </div>
    <div class="col-md-6">
        <div class="nim-panel h-100 p-3">
            <div class="fw-semibold mb-2"><i class="bi bi-layers me-1" aria-hidden="true"></i>Swap areas</div>
            <?php if ($entries === []): ?>
                <div class="text-body-secondary small">No swap is in use on this server.</div>
            <?php else: ?>
                <table class="table table-sm align-middle mb-0">
                    <thead><tr><th>Where</th><th>Type</th><th class="text-end">Size</th><th class="text-end">Used</th></tr></thead>
                    <tbody>
                    <?php foreach ($entries as $e): $e = (array) $e; ?>
                        <tr><td class="font-monospace"><?= e((string) $e['path']) ?><?= !empty($e['managed']) ? ' <span class="badge text-bg-light border">NimVor</span>' : '' ?></td><td><?= e((string) $e['type']) ?></td><td class="text-end"><?= e(format_bytes((int) $e['size_bytes'])) ?></td><td class="text-end"><?= e(format_bytes((int) $e['used_bytes'])) ?></td></tr>
                    <?php endforeach; ?>
                    </tbody>
                </table>
                <?php if (count($entries) > count($managed)): ?><div class="small text-body-secondary mt-2">NimVor changes only its own swap file (<code>/swapfile</code>, or Ubuntu's <code>/swap.img</code>). Swap on a partition or elsewhere is left alone.</div><?php endif; ?>
            <?php endif; ?>
        </div>
    </div>
</div>

<?php if (!empty($s['simulated'])): ?><div class="alert alert-info small">This is a simulated server: nothing here changes the system.</div><?php endif; ?>
<?php if (empty($s['supported'])): ?>
    <div class="alert alert-warning"><i class="bi bi-exclamation-triangle me-1" aria-hidden="true"></i><?= e((string) ($s['reason'] ?? 'Swap cannot be changed on this server.')) ?></div>
<?php elseif ($canManage): ?>
    <div class="nim-panel p-3 mb-3">
        <div class="fw-semibold mb-1"><i class="bi bi-sliders me-1" aria-hidden="true"></i><?= $managed === [] ? 'Create a swap file' : 'Change the size of ' . e((string) $s['target']) ?></div>
        <p class="small text-body-secondary mb-3">
            The new file is built next to the current one, then swap is switched over and <code>/etc/fstab</code> is updated so it comes back after a restart. If any step fails, the previous swap is put back. Turning the old swap off needs enough free memory to take back what is in it; if there is not, nothing is changed.
            At most <?= e(format_bytes($maxMb * 1048576)) ?> can be used now (the disk keeps 1 GB free).
        </p>
        <form method="post" action="<?= e($this->url('server.swap.set')) ?>" class="row g-2 align-items-end" data-nim-busy data-nim-confirm="<?= $managed === [] ? 'Create the swap file?' : 'Change the swap size? Swap is turned off briefly while the new file takes over.' ?>">
            <?= $this->csrfField() ?>
            <div class="col-auto">
                <label class="form-label small mb-1" for="swap-gb">Size</label>
                <select class="form-select form-select-sm" id="swap-gb" name="size_gb">
                    <option value="">Choose…</option>
                    <?php foreach ($presets as $g): ?><option value="<?= $g ?>"><?= $g ?> GB</option><?php endforeach; ?>
                </select>
            </div>
            <div class="col-auto">
                <label class="form-label small mb-1" for="swap-mb">or a custom size in MB</label>
                <input class="form-control form-control-sm" id="swap-mb" name="custom_mb" inputmode="numeric" pattern="[0-9]*" placeholder="<?= $minMb ?>–<?= $maxMb ?>">
            </div>
            <div class="col-auto"><button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= $managed === [] ? 'Create swap' : 'Apply' ?></button></div>
        </form>
        <?php if ($managed !== []): ?>
            <form method="post" action="<?= e($this->url('server.swap.remove')) ?>" class="mt-3" data-nim-busy data-nim-confirm="Turn off and delete <?= e((string) $s['target']) ?>? Programs that use swap need free memory to take its contents back.">
                <?= $this->csrfField() ?>
                <button class="btn btn-sm btn-outline-danger"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Remove the swap file</button>
            </form>
        <?php endif; ?>
    </div>
<?php endif; ?>
