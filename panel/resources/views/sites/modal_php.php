<?php
/**
 * The PHP-FPM window of the Websites list: the version, the limits and the process manager of one website.
 * Saved without leaving the list (data-nim-site-modal-form); the full page keeps the PHP directives and
 * the security profile.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var list<string> $phpVersions
 */
$s = (array) ($site['php_settings'] ?? []);
$v = static fn (string $k, mixed $def = '') => $s[$k] ?? $def;
$id = (int) $site['id'];
?>
<form method="post" action="<?= e($this->url('sites.php.update', ['id' => $id])) ?>" data-nim-site-modal-form novalidate>
    <?= $this->csrfField() ?>
    <div class="modal-header">
        <h2 class="modal-title h5"><i class="bi bi-cpu me-1"></i>PHP-FPM <span class="text-body-secondary fw-normal">· <?= e($site['domain']) ?></span></h2>
        <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
    </div>
    <div class="modal-body">
        <div class="alert alert-danger small d-none" data-nim-modal-error role="alert"></div>
        <div class="row g-3">
            <div class="col-md-4">
                <label class="form-label small" for="mp-version">PHP version</label>
                <select class="form-select form-select-sm" id="mp-version" name="php_version">
                    <?php if ($phpVersions === []): ?><option value="">No PHP-FPM installed</option><?php endif; ?>
                    <?php foreach (array_reverse($phpVersions) as $pv): ?><option value="<?= e($pv) ?>"<?= (string) ($site['php_version'] ?? '') === $pv ? ' selected' : '' ?>>PHP <?= e($pv) ?></option><?php endforeach; ?>
                </select>
            </div>
            <div class="col-md-8">
                <label class="form-label small" for="mp-pm">Process manager</label>
                <select class="form-select form-select-sm" id="mp-pm" name="pm">
                    <?php foreach (['ondemand' => 'On demand: start processes when requests come (least memory)', 'dynamic' => 'Dynamic: keep some idle processes ready', 'static' => 'Static: always keep all processes (fastest, most memory)'] as $pmv => $pml): ?>
                        <option value="<?= e($pmv) ?>"<?= (string) $v('pm', 'ondemand') === $pmv ? ' selected' : '' ?>><?= e($pml) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>
            <?php foreach ([
                ['max_children', 'Max PHP processes', 5, 'pm.max_children'], ['memory_limit_mb', 'Memory limit per request (MB)', 256, 'memory_limit'],
                ['upload_max_mb', 'Max upload (MB)', 64, 'upload_max_filesize / post_max_size'], ['max_execution_seconds', 'Max execution (s)', 60, 'max_execution_time'],
                ['request_timeout_seconds', 'Request timeout (s, 0 = none)', 0, 'request_terminate_timeout'], ['max_requests', 'Requests per process', 500, 'pm.max_requests'],
                ['idle_timeout_seconds', 'Idle timeout (s, on demand)', 10, 'pm.process_idle_timeout'], ['slowlog_seconds', 'Log requests slower than (s)', 10, 'request_slowlog_timeout'],
                ['start_servers', 'Start processes (dynamic; 0 = automatic)', 0, 'pm.start_servers'], ['min_spare_servers', 'Min idle (dynamic; 0 = automatic)', 0, 'pm.min_spare_servers'],
                ['max_spare_servers', 'Max idle (dynamic; 0 = automatic)', 0, 'pm.max_spare_servers'],
            ] as [$k, $label, $def, $hint]): ?>
                <div class="col-sm-6 col-md-4">
                    <label class="form-label small" for="mp-<?= e($k) ?>"><?= e($label) ?></label>
                    <input class="form-control form-control-sm" type="number" id="mp-<?= e($k) ?>" name="<?= e($k) ?>" value="<?= e((string) $v($k, $def)) ?>">
                    <div class="form-text font-monospace"><?= e($hint) ?></div>
                </div>
            <?php endforeach; ?>
        </div>
        <p class="small text-body-secondary mt-3 mb-0">Changing the version restarts this website's PHP processes. Extra <code>php.ini</code> directives and the security profile are on the <a href="<?= e($this->url('sites.php', ['id' => $id])) ?>">PHP settings page</a>.</p>
    </div>
    <div class="modal-footer">
        <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
        <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save and apply</button>
    </div>
</form>
