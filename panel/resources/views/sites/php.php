<?php
/**
 * A website's PHP settings: the PHP version, the process limits and security
 * profile, and the website's own php.ini directives.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var list<string> $phpVersions
 * @var string $profileDefault
 */
$old ??= [];
$errors ??= [];
$isNew = false;
$v = static function (string $k, mixed $default = '') use ($old, $site) {
    if (array_key_exists($k, $old)) {
        return $old[$k];
    }
    if (array_key_exists($k, $site['php_settings'] ?? [])) {
        return $site['php_settings'][$k];
    }
    if (array_key_exists($k, $site)) {
        return $site[$k];
    }
    return $default;
};
$serverPhp = $this->serverScope();
$err = static fn (string $k): string => isset($errors[$k]) ? '<div class="invalid-feedback d-block">' . e($errors[$k]) . '</div>' : '';
$cls = static fn (string $k): string => isset($errors[$k]) ? ' is-invalid' : '';
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'php']) ?>

<p class="small text-body-secondary">PHP settings of <strong><?= e($site['domain']) ?></strong>: the PHP version it runs, its limits, and its own <code>php.ini</code> directives. Everything else about the website is under Settings.</p>

<form method="post" action="<?= e($this->url('sites.php.update', ['id' => (int) $site['id']])) ?>" data-nim-busy novalidate>
    <?= $this->csrfField() ?>
    <div class="row g-3">
        <div class="col-lg-6">
            <div class="card mb-3" id="php-version">
                <div class="card-header"><i class="bi bi-filetype-php me-1"></i>PHP version</div>
                <div class="card-body">
                    <label class="form-label" for="f-php">Version</label>
                    <select class="form-select<?= $cls('php_version') ?>" id="f-php" name="php_version">
                        <?php if ($phpVersions === []): ?><option value="">No PHP-FPM installed</option><?php endif; ?>
                        <?php foreach (array_reverse($phpVersions) as $pv): ?>
                            <option value="<?= e($pv) ?>"<?= (string) $v('php_version', '') === $pv ? ' selected' : '' ?>>PHP <?= e($pv) ?></option>
                        <?php endforeach; ?>
                    </select>
                    <?= $err('php_version') ?>
                    <div class="form-text">Changing the version restarts this website's PHP processes. Check your application supports the version first.</div>
                </div>
            </div>
            <div class="card mb-3" id="php">
                <div class="card-header"><i class="bi bi-filetype-php me-1"></i>PHP settings</div>
                <div class="card-body">
                    <div class="row g-2">
                        <?php foreach (array_merge([
                            ['memory_limit_mb', 'Memory limit (MB)', 256], ['upload_max_mb', 'Max upload (MB)', 64],
                            ['max_execution_seconds', 'Max execution (s)', 60],
                        ], $serverPhp ? [['max_children', 'Max PHP processes', 5]] : []) as [$k, $label, $def]): ?>
                            <div class="col-6">
                                <label class="form-label small" for="f-<?= e($k) ?>"><?= e($label) ?></label>
                                <input class="form-control form-control-sm<?= $cls($k) ?>" type="number" id="f-<?= e($k) ?>" name="<?= e($k) ?>" value="<?= e($v($k, $def)) ?>">
                                <?= $err($k) ?>
                            </div>
                        <?php endforeach; ?>
                    </div>
                    <?php if ($serverPhp): ?>
                    <?php
                        // PHP security profile (disable_functions). A new website
                        // defaults to Recommended; an existing one keeps its own
                        // choice, and "Keep current" leaves a legacy site unchanged.
                        $profileNow = (string) $v('profile', (($site ?? null) === null) ? ($profileDefault ?? 'recommended') : '');
                        $profileOpts = ['recommended' => 'Recommended — disable exec, system, shell_exec, passthru, popen, proc_open, proc_* and pcntl_*', 'balanced' => 'Balanced — disable the shell functions, keep process control for queue workers', 'compatibility' => 'Compatibility — disable nothing', 'custom' => 'Custom — a list you choose'];
                        if ($profileNow === '') {
                            $profileOpts = ['' => 'Keep current'] + $profileOpts;
                        }
                    ?>
                    <div class="mt-3">
                        <label class="form-label small" for="f-php-profile">PHP security profile</label>
                        <select class="form-select form-select-sm<?= $cls('php_profile') ?>" id="f-php-profile" name="php_profile">
                            <?php foreach ($profileOpts as $pv => $pl): ?>
                                <option value="<?= $pv ?>"<?= $profileNow === $pv ? ' selected' : '' ?>><?= htmlspecialchars($pl) ?></option>
                            <?php endforeach; ?>
                        </select>
                        <div class="mt-1<?= $profileNow === 'custom' ? '' : ' d-none' ?>" data-nim-php-custom>
                            <input class="form-control form-control-sm<?= $cls('disable_functions') ?>" name="disable_functions" value="<?= htmlspecialchars((string) $v('disable_functions', '')) ?>" placeholder="exec, system, shell_exec">
                            <div class="form-text">Comma-separated function names to disable.</div>
                        </div>
                    </div>
                    <details class="mt-3"<?= array_intersect(array_keys($errors ?? []), ['pm', 'start_servers', 'min_spare_servers', 'max_spare_servers', 'idle_timeout_seconds', 'max_requests', 'request_timeout_seconds', 'slowlog_seconds']) !== [] ? ' open' : '' ?>>
                        <summary class="small fw-semibold">Advanced PHP-FPM</summary>
                        <div class="row g-2 mt-1">
                            <div class="col-12">
                                <label class="form-label small" for="f-pm">Process manager</label>
                                <select class="form-select form-select-sm<?= $cls('pm') ?>" id="f-pm" name="pm">
                                    <?php foreach (['ondemand' => 'On demand: start processes when requests come (least memory)', 'dynamic' => 'Dynamic: keep some idle processes ready', 'static' => 'Static: always run all processes (fastest, most memory)'] as $pmv => $pml): ?>
                                        <option value="<?= e($pmv) ?>"<?= (string) $v('pm', 'ondemand') === $pmv ? ' selected' : '' ?>><?= e($pml) ?></option>
                                    <?php endforeach; ?>
                                </select>
                                <?= $err('pm') ?>
                            </div>
                            <?php foreach ([
                                ['start_servers', 'Start processes (dynamic; 0 = automatic)', 0], ['min_spare_servers', 'Min idle (dynamic; 0 = automatic)', 0],
                                ['max_spare_servers', 'Max idle (dynamic; 0 = automatic)', 0], ['idle_timeout_seconds', 'Idle timeout (s, on demand)', 10],
                                ['max_requests', 'Requests per process', 500], ['request_timeout_seconds', 'Request timeout (s, 0 = none)', 0],
                                ['slowlog_seconds', 'Log requests slower than (s)', 10],
                            ] as [$k, $label, $def]): ?>
                                <div class="col-6">
                                    <label class="form-label small" for="f-<?= e($k) ?>"><?= e($label) ?></label>
                                    <input class="form-control form-control-sm<?= $cls($k) ?>" type="number" id="f-<?= e($k) ?>" name="<?= e($k) ?>" value="<?= e($v($k, $def)) ?>">
                                    <?= $err($k) ?>
                                </div>
                            <?php endforeach; ?>
                        </div>
                        <p class="small text-body-secondary mt-2 mb-0">Server-wide PHP-FPM settings and php.ini are under PHP → the version → Settings.</p>
                    </details>
                    <?php endif; ?>
                </div>
            </div>
            <?php if (!$isNew): $ini = (array) ($old['ini'] ?? ($site['php_settings']['ini'] ?? [])); $iniErr = array_filter($errors ?? [], static fn ($k) => str_starts_with((string) $k, 'ini'), ARRAY_FILTER_USE_KEY); ?>
            <div class="card mb-3" id="php-ini">
                <div class="card-header nim-toolbar">
                    <span class="nim-toolbar-title"><i class="bi bi-sliders2 me-1"></i>Additional PHP directives <span class="badge rounded-pill text-bg-light border fw-normal"><?= count(array_filter($ini, static fn ($x) => $x !== '')) ?> set</span></span>
                    <input class="form-control form-control-sm nim-filter" type="search" placeholder="Filter directives" aria-label="Filter directives" data-nim-filter="#php-ini-table">
                </div>
                <?php if (isset($iniErr['ini'])): ?><div class="text-danger small px-3 pt-2"><?= e((string) $iniErr['ini']) ?></div><?php endif; ?>
                <div class="table-responsive nim-scroll-400">
                    <table class="table table-sm align-middle mb-0 nim-table" id="php-ini-table">
                        <tbody>
                        <?php foreach (\NimVor\Services\PhpDirectives::groups() as $group => $keys): ?>
                            <tr class="table-light"><th colspan="2" class="small"><?= e($group) ?></th></tr>
                            <?php foreach ($keys as $k): [, $kind, $hint] = \NimVor\Services\PhpDirectives::ALL[$k]; $val = (string) ($ini[$k] ?? ''); $id = 'ini-' . str_replace('.', '-', $k); $bad = isset($iniErr['ini.' . $k]); ?>
                                <tr>
                                    <td class="w-50"><label class="font-monospace small" for="<?= e($id) ?>"><?= e($k) ?></label><?php if ($hint !== ''): ?><div class="small text-body-secondary"><?= e($hint) ?></div><?php endif; ?></td>
                                    <td>
                                        <?php if ($kind === 'bool' || str_starts_with($kind, 'enum:')): $opts = $kind === 'bool' ? ['On', 'Off'] : explode('|', substr($kind, 5)); ?>
                                            <select class="form-select form-select-sm<?= $bad ? ' is-invalid' : '' ?>" id="<?= e($id) ?>" name="ini[<?= e($k) ?>]">
                                                <option value="">Server default</option>
                                                <?php foreach ($opts as $o): ?><option value="<?= e($o) ?>"<?= $val === $o ? ' selected' : '' ?>><?= e($o) ?></option><?php endforeach; ?>
                                            </select>
                                        <?php else: ?>
                                            <input class="form-control form-control-sm font-monospace<?= $bad ? ' is-invalid' : '' ?>" id="<?= e($id) ?>" name="ini[<?= e($k) ?>]" value="<?= e($val) ?>" placeholder="Server default" autocomplete="off" spellcheck="false">
                                        <?php endif; ?>
                                        <?php if ($bad): ?><div class="invalid-feedback d-block"><?= e((string) $iniErr['ini.' . $k]) ?></div><?php endif; ?>
                                    </td>
                                </tr>
                            <?php endforeach; ?>
                        <?php endforeach; ?>
                        <tr class="d-none" data-nim-filter-empty><td colspan="2" class="small text-body-secondary">No directive matches.</td></tr>
                        </tbody>
                    </table>
                </div>
            </div>
            <?php endif; ?>
        </div>
    </div>
    <div class="d-flex gap-2 align-items-center">
        <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save and apply</button>
    </div>
</form>
