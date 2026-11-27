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
