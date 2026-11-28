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
<form method="post" action="<?= e($this->url('databases.server.settings')) ?>" data-nim-busy data-nim-db-settings data-nim-db-ram="<?= (int) ($status['ram_bytes'] ?? 0) ?>" class="mb-3">
    <?= $this->csrfField() ?>
    <div class="<?= $modal ? '' : 'card' ?>">
        <?php if ($modal): ?>
            <div class="small nim-muted mb-2">Saved in <code><?= e((string) ($status['drop_in'] ?? '')) ?></code>, read after the distribution's files</div>
        <?php else: ?>
        <div class="card-header d-flex flex-wrap align-items-center gap-2"><span><i class="bi bi-sliders me-1"></i>Settings</span><span class="small nim-muted ms-auto">Saved in <code><?= e((string) ($status['drop_in'] ?? '')) ?></code>, read after the distribution's files</span></div>
        <?php endif; ?>
        <div class="<?= $modal ? '' : 'card-body' ?>">
            <p class="small nim-muted">Empty fields keep the server's own value (shown in grey). Values are checked, then the server program checks the option files; settings marked <span class="badge text-bg-light border fw-normal">now</span> are applied at once, the others at the next restart. If anything fails, the previous settings come back.</p>
            <?php
                $profiles = (array) ($status['profiles'] ?? []);
                $advice = \NimVor\Services\DbTuningAdvisor::advise((array) ($status['status'] ?? []), $vars);
                $ram = (int) ($status['ram_bytes'] ?? 0);
            ?>
            <div class="small mb-3 d-flex flex-wrap gap-x-3 column-gap-4 row-gap-1" data-nim-db-facts>
                <span><span class="nim-muted">MariaDB / MySQL</span> <strong><?= e((string) ($vars['version'] ?? '—')) ?></strong></span>
                <span><span class="nim-muted">Detected memory</span> <strong><?= $ram > 0 ? e(format_bytes($ram, 1)) : '—' ?></strong></span>
                <span><span class="nim-muted">Running for</span> <strong><?= e($advice['uptime_text']) ?></strong></span>
            </div>
            <?php if ($profiles !== [] && $canManage): ?>
            <div class="border rounded p-3 mb-4" data-nim-db-profiles data-nim-db-ram="<?= $ram ?>">
                <div class="fw-semibold mb-1">Performance profile</div>
                <p class="small nim-muted mb-2">Starting values for the memory and disk settings, calculated for this server's <?= e(format_bytes($ram, 1)) ?> of memory: the buffer pool never takes more than 55% of it and always leaves room for PHP-FPM, Nginx, Redis, mail and the system. A profile only fills in the form below: change anything you like, then press <em>Save settings</em>. Everything else (listening address, character set, logging, how safely commits are written) is left alone.</p>
                <div class="row g-2">
                    <?php foreach ($profiles as $p): ?>
                    <div class="col-md-4">
                        <button type="button" class="btn btn-outline-secondary w-100 text-start h-100" data-nim-db-profile="<?= e((string) json_encode($p['values'], JSON_UNESCAPED_SLASHES)) ?>" data-nim-db-profile-name="<?= e((string) $p['name']) ?>" aria-pressed="false">
                            <span class="fw-semibold"><?= e((string) $p['name']) ?></span><?php if (!empty($p['recommended'])): ?> <span class="badge text-bg-success fw-normal">Recommended here</span><?php endif; ?>
                            <span class="d-block small nim-muted"><?= e((string) $p['description']) ?></span>
                            <span class="d-block small mt-1">Buffer pool <?= e((string) $p['values']['innodb_buffer_pool_size']) ?> · <?= e((string) $p['values']['max_connections']) ?> connections · I/O <?= e((string) $p['values']['innodb_io_capacity']) ?>/<?= e((string) $p['values']['innodb_io_capacity_max']) ?></span>
                            <?php if (!empty($p['warning'])): ?><span class="d-block small text-warning-emphasis mt-1"><i class="bi bi-exclamation-triangle me-1"></i><?= e((string) $p['warning']) ?></span><?php endif; ?>
                            <?php foreach ((array) ($p['notes'] ?? []) as $note): ?><span class="d-block small nim-muted mt-1"><i class="bi bi-info-circle me-1"></i><?= e((string) $note) ?></span><?php endforeach; ?>
                        </button>
                    </div>
                    <?php endforeach; ?>
                </div>
                <div class="small mt-2" data-nim-db-profile-status role="status" aria-live="polite"></div>
            </div>
            <?php endif; ?>
            <div class="border rounded p-3 mb-4" data-nim-db-workload>
                <div class="fw-semibold mb-1">Workload check</div>
                <p class="small nim-muted mb-2">MariaDB has been running for <strong><?= e($advice['uptime_text']) ?></strong>. Its status counters count from the last start and <strong>reset whenever MariaDB restarts</strong>, so they describe that period only.</p>
                <?php if (!$advice['enough']): ?>
                <div class="small mb-2"><i class="bi bi-hourglass-split me-1 nim-muted"></i>Too early to judge: review these numbers after the server has run for at least 24 hours of normal traffic, and change settings only then.</div>
                <?php endif; ?>
                <?php foreach ([['Since the server started (cumulative counters)', $advice['cumulative'], 'Nothing worth reporting yet.'], ['Right now (a snapshot)', $advice['snapshot'], '']] as [$title, $items, $empty]): ?>
                <div class="small fw-semibold mt-2"><?= e($title) ?></div>
                <ul class="list-unstyled small mb-0">
                    <?php foreach ($items as $a): ?>
                    <li class="mb-1"><i class="bi <?= $a['level'] === 'warn' ? 'bi-exclamation-triangle text-warning' : ($a['level'] === 'ok' ? 'bi-check-circle text-success' : 'bi-info-circle nim-muted') ?> me-1"></i><?= e($a['text']) ?></li>
                    <?php endforeach; ?>
                    <?php if ($items === [] && $empty !== ''): ?><li class="nim-muted"><?= e($empty) ?></li><?php endif; ?>
                </ul>
                <?php endforeach; ?>
                <div class="small nim-muted mt-2">Profiles are starting points, not the best values for every server.</div>
            </div>
            <div class="small mb-3 d-none" data-nim-db-restart-note role="status"><i class="bi bi-arrow-repeat me-1"></i><span></span></div>
            <div class="row g-4">
            <?php foreach ($groups as $group => $list): ?>
                <div class="col-xl-6">
                    <h2 class="h6"><?= e($group) ?></h2>
                    <?php foreach ($list as $s): $k = (string) $s['key']; $cur = (string) ($managed[$k] ?? ''); $eff = $shown($s, (string) ($vars[$k] ?? '')); $id = 'dbs-' . $k; $ctl = ' data-nim-db-key="' . e($k) . '" data-nim-db-kind="' . e((string) $s['kind']) . '"' . (!empty($s['dynamic']) ? '' : ' data-nim-db-restart="1"') . ' data-nim-db-label="' . e((string) $s['label']) . '"'; ?>
                        <div class="row g-2 align-items-start mb-2">
                            <div class="col-sm-6 col-form-label col-form-label-sm">
                                <label for="<?= e($id) ?>"><?= e((string) $s['label']) ?></label><?= $this->hint((string) $s['help'], (string) $s['label']) ?> <?= !empty($s['dynamic']) ? '<span class="badge text-bg-light border fw-normal">now</span>' : '<span class="badge text-bg-warning-subtle border fw-normal" title="Takes effect after the database server restarts">needs restart</span>' ?> <span class="badge text-bg-info-subtle border fw-normal d-none" data-nim-db-custom>customized</span>
                                <div class="nim-var"><?= e($k) ?></div>
                            </div>
                            <div class="col-sm-6">
                                <input type="hidden" name="reset[<?= e($k) ?>]" value="0" data-nim-db-reset-flag>
                                <?php if ($s['kind'] === 'bool' || $s['kind'] === 'choice'):
                                    $opts = $s['kind'] === 'bool' ? ['ON', 'OFF'] : ($k === 'collation_server' ? (array) ($collations[$charset] ?? []) : (array) $s['choices']); ?>
                                    <select class="form-select form-select-sm" id="<?= e($id) ?>" name="settings[<?= e($k) ?>]"<?= $ctl ?><?= $canManage ? '' : ' disabled' ?>>
                                        <option value="">Server's value (<?= e($eff !== '' ? $eff : '—') ?>)</option>
                                        <?php foreach ($opts as $o): ?><option<?= $cur === (string) $o ? ' selected' : '' ?>><?= e((string) $o) ?></option><?php endforeach; ?>
                                    </select>
                                <?php else: ?>
                                    <input class="form-control form-control-sm font-monospace" id="<?= e($id) ?>" name="settings[<?= e($k) ?>]" value="<?= e($cur) ?>" placeholder="<?= e($eff) ?>"<?= $ctl ?><?= $canManage ? '' : ' disabled' ?>>
                                <?php endif; ?>
                                <div class="small nim-muted mt-1">
                                    <?php if ($s['kind'] !== 'bool' && $s['kind'] !== 'choice'): ?>In use: <?= e($eff !== '' ? $eff : '—') ?><?php endif; ?>
                                    <span data-nim-db-rec></span>
                                    <?php if ($canManage && $cur !== ''): ?> <button type="button" class="btn btn-link btn-sm p-0 align-baseline" data-nim-db-reset>Reset to the server's own value</button><?php endif; ?>
                                </div>
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
