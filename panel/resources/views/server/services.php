<?php /** @var \WhaCP\View\View $this */ ?>
<?php $this->start(); ?>Services<?php $this->end('title'); ?>
<?php $this->start(); ?>systemd units monitored by WhaCP<?php $this->end('subtitle'); ?>

<div class="card">
    <?php if ($services === []): ?>
        <div class="card-body text-center py-5 text-body-secondary wcp-empty">
            <i class="bi bi-cpu display-6"></i>
            <p class="mt-3 mb-0">No known services were found. systemd may be unavailable on this host.</p>
        </div>
    <?php else: ?>
        <div class="table-responsive">
            <table class="table table-hover align-middle mb-0 wcp-table">
                <thead><tr><th>Service</th><th>State</th><th>Boot</th><th>PID</th><th>Memory</th><th>Since</th></tr></thead>
                <tbody>
                <?php foreach ($services as $s): ?>
                    <tr>
                        <td><span class="fw-semibold"><?= e($s['unit']) ?></span><div class="small text-body-secondary"><?= e($s['description']) ?></div></td>
                        <td><?= $this->statusBadge((string) $s['active_state'], $s['active_state'] . ' (' . $s['sub_state'] . ')') ?></td>
                        <td><?= e($s['unit_file_state'] !== '' ? $s['unit_file_state'] : '—') ?></td>
                        <td><?= (int) $s['main_pid'] > 0 ? (int) $s['main_pid'] : '—' ?></td>
                        <td><?= e(format_bytes($s['memory_bytes'] ?? null)) ?></td>
                        <td class="small"><?= e($s['active_since'] !== null ? time_ago((string) $s['active_since']) : '—') ?></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
    <div class="card-footer small text-body-secondary"><i class="bi bi-lock me-1"></i>Start, stop and restart actions will be added with write support.</div>
</div>
