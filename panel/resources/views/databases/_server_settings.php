<?php
/**
 * The database server's settings form (the settings page and the window opened from the dashboard and the Databases page).
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed>|null $status db.server_status
 * @var bool $modal in a window: no second header (the window has its own title) and no card frame
 */
$modal = !empty($modal);
use NimVor\Security\Permissions as P;

$canManage = $this->can(P::SERVER_MANAGE);
$canRestart = $this->can(P::SERVICES_MANAGE);
$vars = (array) ($status['variables'] ?? []);
$managed = (array) ($status['managed'] ?? []);
$shown = static function (array $s, string $v): string {
    if ($s['kind'] === 'size' && ctype_digit($v)) {
        return format_bytes((int) $v, 0);
    }
    return $v;
};
$groups = [];
foreach ((array) ($status['settings'] ?? []) as $s) {
    $groups[(string) $s['group']][] = $s;
}
$collations = (array) ($status['collations'] ?? []);
$charset = (string) ($managed['character_set_server'] ?? $vars['character_set_server'] ?? 'utf8mb4');
?>
<form method="post" action="<?= e($this->url('databases.server.settings')) ?>" data-nim-busy class="mb-3">
    <?= $this->csrfField() ?>
    <div class="<?= $modal ? '' : 'card' ?>">
        <?php if ($modal): ?>
            <div class="small nim-muted mb-2">Saved in <code><?= e((string) ($status['drop_in'] ?? '')) ?></code>, read after the distribution's files</div>
        <?php else: ?>
        <div class="card-header d-flex flex-wrap align-items-center gap-2"><span><i class="bi bi-sliders me-1"></i>Settings</span><span class="small nim-muted ms-auto">Saved in <code><?= e((string) ($status['drop_in'] ?? '')) ?></code>, read after the distribution's files</span></div>
        <?php endif; ?>
        <div class="<?= $modal ? '' : 'card-body' ?>">
            <p class="small nim-muted">Empty fields keep the server's own value (shown in grey). Values are checked, then the server program checks the option files; settings marked <span class="badge text-bg-light border fw-normal">now</span> are applied at once, the others at the next restart. If anything fails, the previous settings come back.</p>
            <?php $profiles = (array) ($status['profiles'] ?? []); $advice = \NimVor\Services\DbTuningAdvisor::advise((array) ($status['status'] ?? []), $vars); ?>
            <?php if ($profiles !== [] && $canManage): ?>
            <div class="border rounded p-3 mb-4" data-nim-db-profiles>
                <div class="fw-semibold mb-1">Performance profile</div>
                <p class="small nim-muted mb-2">Starting values for the memory and disk settings, calculated for this server's <?= e(format_bytes((int) ($status['ram_bytes'] ?? 0), 1)) ?> of memory. A profile only fills in the form below: change anything you like, then press <em>Save settings</em>. Everything else (listening address, character set, logging, how safely commits are written) is left alone.</p>
                <div class="row g-2">
                    <?php foreach ($profiles as $p): ?>
                    <div class="col-md-4">
                        <button type="button" class="btn btn-outline-secondary w-100 text-start h-100" data-nim-db-profile="<?= e((string) json_encode($p['values'], JSON_UNESCAPED_SLASHES)) ?>" data-nim-db-profile-name="<?= e((string) $p['name']) ?>">
                            <span class="fw-semibold"><?= e((string) $p['name']) ?></span><?php if (!empty($p['recommended'])): ?> <span class="badge text-bg-success fw-normal">Recommended here</span><?php endif; ?>
                            <span class="d-block small nim-muted"><?= e((string) $p['description']) ?></span>
                            <span class="d-block small mt-1">Buffer pool <?= e((string) $p['values']['innodb_buffer_pool_size']) ?> · <?= e((string) $p['values']['max_connections']) ?> connections · I/O <?= e((string) $p['values']['innodb_io_capacity']) ?>/<?= e((string) $p['values']['innodb_io_capacity_max']) ?></span>
                            <?php if (!empty($p['warning'])): ?><span class="d-block small text-warning-emphasis mt-1"><i class="bi bi-exclamation-triangle me-1"></i><?= e((string) $p['warning']) ?></span><?php endif; ?>
                        </button>
                    </div>
                    <?php endforeach; ?>
                </div>
                <div class="small mt-2" data-nim-db-profile-note role="status"></div>
            </div>
            <?php endif; ?>
            <?php if ($advice !== []): ?>
            <div class="border rounded p-3 mb-4">
                <div class="fw-semibold mb-1">Workload check</div>
                <ul class="list-unstyled small mb-0">
                    <?php foreach ($advice as $a): ?>
                    <li class="mb-1"><i class="bi <?= $a['level'] === 'warn' ? 'bi-exclamation-triangle text-warning' : ($a['level'] === 'ok' ? 'bi-check-circle text-success' : 'bi-info-circle nim-muted') ?> me-1"></i><?= e($a['text']) ?></li>
                    <?php endforeach; ?>
                </ul>
                <div class="small nim-muted mt-2">Profiles are starting points, not the best values for every server. This check reads the server's own counters (peak connections, running statements, InnoDB reads from disk, slow queries) so you can adjust them to what your sites actually do.</div>
            </div>
            <?php endif; ?>
            <div class="row g-4">
            <?php foreach ($groups as $group => $list): ?>
                <div class="col-xl-6">
                    <h2 class="h6"><?= e($group) ?></h2>
                    <?php foreach ($list as $s): $k = (string) $s['key']; $cur = (string) ($managed[$k] ?? ''); $eff = $shown($s, (string) ($vars[$k] ?? '')); $id = 'dbs-' . $k; ?>
                        <div class="row g-2 align-items-start mb-2">
                            <div class="col-sm-6 col-form-label col-form-label-sm">
                                <label for="<?= e($id) ?>"><?= e((string) $s['label']) ?></label><?= $this->hint((string) $s['help'], (string) $s['label']) ?> <?= !empty($s['dynamic']) ? '<span class="badge text-bg-light border fw-normal">now</span>' : '<span class="badge text-bg-light border fw-normal">restart</span>' ?>
                                <div class="nim-var"><?= e($k) ?></div>
                            </div>
                            <div class="col-sm-6">
                                <input type="hidden" name="managed[<?= e($k) ?>]" value="<?= e($cur) ?>">
                                <?php if ($s['kind'] === 'bool' || $s['kind'] === 'choice'):
                                    $opts = $s['kind'] === 'bool' ? ['ON', 'OFF'] : ($k === 'collation_server' ? (array) ($collations[$charset] ?? []) : (array) $s['choices']); ?>
                                    <select class="form-select form-select-sm" id="<?= e($id) ?>" name="settings[<?= e($k) ?>]"<?= $canManage ? '' : ' disabled' ?>>
                                        <option value="">Server's value (<?= e($eff !== '' ? $eff : '—') ?>)</option>
                                        <?php foreach ($opts as $o): ?><option<?= $cur === (string) $o ? ' selected' : '' ?>><?= e((string) $o) ?></option><?php endforeach; ?>
                                    </select>
                                <?php else: ?>
                                    <input class="form-control form-control-sm font-monospace" id="<?= e($id) ?>" name="settings[<?= e($k) ?>]" value="<?= e($cur) ?>" placeholder="<?= e($eff) ?>"<?= $canManage ? '' : ' disabled' ?>>
                                <?php endif; ?>
                            </div>
                        </div>
                    <?php endforeach; ?>
                </div>
            <?php endforeach; ?>
            </div>
        </div>
        <?php if ($canManage): ?>
        <div class="<?= $modal ? 'border-top pt-3 mt-3' : 'card-footer' ?> d-flex flex-wrap align-items-center gap-3">
            <?php if ($canRestart): ?><div class="form-check mb-0"><input class="form-check-input" type="checkbox" name="restart" value="1" id="dbs-restart"><label class="form-check-label small" for="dbs-restart">Restart the database server now to apply everything (checked first; rolled back if it does not come back)</label></div><?php endif; ?>
            <button class="btn btn-primary ms-auto"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save settings</button>
        </div>
        <?php endif; ?>
    </div>
</form>
