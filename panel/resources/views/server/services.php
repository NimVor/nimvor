<?php /** @var \NimVor\View\View $this */ ?>
<?php $this->start(); ?>Services<?php $this->end('title'); ?>
<?php $this->start(); ?>systemd units monitored by NimVor<?php $this->end('subtitle'); ?>

<?php if (!empty($nimvor['services'])): ?>
<div class="card mb-3" id="nimvor-services">
    <div class="card-header"><i class="bi bi-boxes me-1"></i>NimVor</div>
    <div class="table-responsive">
        <table class="table align-middle mb-0">
            <thead><tr><th>Service</th><th>State</th><th>Since</th><th></th></tr></thead>
            <tbody>
            <?php foreach ((array) $nimvor['services'] as $w): $st = (array) $w['status']; ?>
                <tr>
                    <td><span class="fw-semibold"><?= e((string) $w['label']) ?></span><div class="small text-body-secondary font-monospace"><?= e((string) $w['unit']) ?></div></td>
                    <td><?= $this->statusBadge((string) ($st['active_state'] ?? ''), ($st['active_state'] ?? '') . ' (' . ($st['sub_state'] ?? '') . ')') ?></td>
                    <td class="small"><?= e(!empty($st['active_since']) ? time_ago((string) $st['active_since']) : '—') ?></td>
                    <td class="text-end">
                        <?php if (!empty($w['restartable']) && $this->can(\NimVor\Security\Permissions::SERVICES_MANAGE) && $this->serverScope()): ?>
                            <form method="post" action="<?= e($this->url('services.nimvor.restart', ['key' => (string) $w['key']])) ?>" data-nim-busy data-nim-confirm="Restart <?= e((string) $w['label']) ?>?<?= in_array($w['key'], ['agent', 'panel'], true) ? ' The panel is unavailable for a few seconds.' : '' ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-clockwise me-1"></i>Restart</button></form>
                        <?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
</div>
<?php endif; ?>
<div class="card">
    <?php if ($services === []): ?>
        <div class="card-body text-center py-5 text-body-secondary nim-empty">
            <i class="bi bi-cpu display-6"></i>
            <p class="mt-3 mb-0">No known services were found. systemd may be unavailable on this host.</p>
        </div>
    <?php else: ?>
        <div class="table-responsive">
            <table class="table table-hover align-middle mb-0 nim-table">
                <thead><tr><th>Service</th><th>State</th><th>Boot</th><th>PID</th><th>Memory</th><th>Since</th><th></th></tr></thead>
                <tbody>
                <?php foreach ($services as $s): ?>
                    <tr>
                        <td><span class="fw-semibold"><?= e($s['unit']) ?></span><div class="small text-body-secondary"><?= e($s['description']) ?></div></td>
                        <td><?= $this->statusBadge((string) $s['active_state'], $s['active_state'] . ' (' . $s['sub_state'] . ')') ?></td>
                        <td><?= e($s['unit_file_state'] !== '' ? $s['unit_file_state'] : '—') ?></td>
                        <td><?= (int) $s['main_pid'] > 0 ? (int) $s['main_pid'] : '—' ?></td>
                        <td><?= e(format_bytes($s['memory_bytes'] ?? null)) ?></td>
                        <td class="small"><?= e($s['active_since'] !== null ? time_ago((string) $s['active_since']) : '—') ?></td>
                        <td class="text-end text-nowrap">
                            <?php if ($this->can(\NimVor\Security\Permissions::SERVICES_MANAGE) && !preg_match('/^(nimvor-agent|nimvor-panel|ssh|sshd)\.service$/', (string) $s['unit'])): ?>
                                <?php $running = $s['active_state'] === 'active'; ?>
                                <form method="post" action="<?= e($this->url('services.control')) ?>" class="d-inline-flex gap-1" data-nim-busy>
                                    <?= $this->csrfField() ?>
                                    <input type="hidden" name="unit" value="<?= e($s['unit']) ?>">
                                    <?php if ($running): ?>
                                        <button class="btn btn-sm btn-outline-secondary" name="op" value="reload" title="Reload"><i class="bi bi-arrow-repeat"></i></button>
                                        <button class="btn btn-sm btn-outline-secondary" name="op" value="restart" title="Restart"><i class="bi bi-arrow-clockwise"></i></button>
                                        <button class="btn btn-sm btn-outline-danger" name="op" value="stop" title="Stop"><i class="bi bi-stop-fill"></i></button>
                                    <?php else: ?>
                                        <button class="btn btn-sm btn-outline-success" name="op" value="start" title="Start"><i class="bi bi-play-fill"></i></button>
                                    <?php endif; ?>
                                </form>
                            <?php endif; ?>
                        </td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
    <div class="card-footer small text-body-secondary">Nginx is always tested with <code>nginx -t</code> before it is reloaded or restarted. SSH and the agent cannot be stopped from the panel.</div>
</div>
