<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $site */
$isNew = $site === null;
$v = static function (string $k, mixed $default = '') use ($old, $site) {
    if (array_key_exists($k, $old)) {
        return $old[$k];
    }
    if ($site !== null) {
        if (array_key_exists($k, $site['php_settings'] ?? [])) {
            return $site['php_settings'][$k];
        }
        if (array_key_exists($k, $site)) {
            return $site[$k];
        }
    }
    return $default;
};
$kind = (string) $v('kind', 'php');
// Process limits, hardening and PHP-FPM tuning are the administrator's.
$serverPhp = $this->serverScope();
$aliases = $old['aliases'] ?? implode("\n", $site['aliases'] ?? []);
$redirects = $old['redirects'] ?? \WhaCP\Services\WebsiteService::redirectsText($site['redirects'] ?? []);
$docSub = $site === null ? '' : ltrim(substr((string) $site['document_root'], strlen((string) $site['root'])), '/');
$err = static fn (string $k): string => isset($errors[$k]) ? '<div class="invalid-feedback d-block">' . e($errors[$k]) . '</div>' : '';
$cls = static fn (string $k): string => isset($errors[$k]) ? ' is-invalid' : '';
?>
<?php if ($isNew): ?>
    <?php $this->start(); ?>New website<?php $this->end('title'); ?>

<?php else: ?>
    <?= $this->partial('sites/header', ['site' => $site, 'tab' => 'settings']) ?>
<?php endif; ?>

<form method="post" action="<?= e($isNew ? $this->url('sites.store') : $this->url('sites.update', ['id' => (int) $site['id']])) ?>" data-wcp-busy novalidate>
    <?= $this->csrfField() ?>
    <div class="row g-3">
        <div class="col-lg-6">
            <div class="card mb-3" id="domains">
                <div class="card-header"><i class="bi bi-globe2 me-1"></i>Domains</div>
                <div class="card-body">
                    <div class="mb-3">
                        <label class="form-label" for="f-domain">Domain</label>
                        <input class="form-control<?= $cls('domain') ?>" id="f-domain" name="domain" value="<?= e($v('domain')) ?>" placeholder="example.com" <?= $isNew ? 'required' : 'disabled' ?> maxlength="253">
                        <?= $err('domain') ?>
                        <?php if (!$isNew): ?><div class="form-text">The primary domain cannot be changed; create a new website instead.</div><?php endif; ?>
                    </div>
                    <div class="mb-0">
                        <label class="form-label" for="f-aliases">Aliases</label>
                        <textarea class="form-control<?= $cls('aliases') ?>" id="f-aliases" name="aliases" rows="3" placeholder="www.example.com"><?= e($aliases) ?></textarea>
                        <?= $err('aliases') ?>
                        <div class="form-text">One per line. Point their DNS records at this server.</div>
                    </div>
                </div>
            </div>
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-diagram-3 me-1"></i>Application</div>
                <div class="card-body">
                    <div class="mb-3">
                        <span class="form-label d-block">Type</span>
                        <?php
                        // Image cards instead of a dropdown. Node.js and Python
                        // are hosted as a reverse proxy to the app's local port,
                        // so they select the "proxy" type.
                        $kindCards = [
                            ['php', 'PHP application', 'filetype-php'],
                            ['static', 'Static site', 'file-earmark-code'],
                            ['proxy', 'Node.js', 'hexagon'],
                            ['proxy', 'Python', 'filetype-py'],
                        ];
                        $kindChosen = false;
                        ?>
                        <div class="row row-cols-2 row-cols-sm-4 g-2" role="radiogroup" aria-label="Application type">
                            <?php foreach ($kindCards as $i => [$k, $label, $icon]): $checked = !$kindChosen && $kind === $k; if ($checked) { $kindChosen = true; } ?>
                                <div class="col">
                                    <input type="radio" class="btn-check" name="kind" id="f-kind-<?= $i ?>" value="<?= e($k) ?>" data-wcp-toggle-kind autocomplete="off"<?= $checked ? ' checked' : '' ?>>
                                    <label class="btn btn-outline-secondary w-100 h-100 py-3 d-flex flex-column align-items-center justify-content-center gap-1" for="f-kind-<?= $i ?>">
                                        <i class="bi bi-<?= e($icon) ?> fs-4" aria-hidden="true"></i>
                                        <span class="small"><?= e($label) ?></span>
                                    </label>
                                </div>
                            <?php endforeach; ?>
                        </div>
                        <?= $err('kind') ?>
                    </div>
                    <div class="mb-3" data-wcp-kind="php">
                        <label class="form-label" for="f-php">PHP version</label>
                        <select class="form-select<?= $cls('php_version') ?>" id="f-php" name="php_version">
                            <?php if ($phpVersions === []): ?><option value="">No PHP-FPM installed</option><?php endif; ?>
                            <?php foreach (array_reverse($phpVersions) as $pv): ?>
                                <option value="<?= e($pv) ?>"<?= (string) $v('php_version', $defaultPhp ?? '') === $pv ? ' selected' : '' ?>>PHP <?= e($pv) ?></option>
                            <?php endforeach; ?>
                        </select>
                        <?= $err('php_version') ?>
                    </div>
                    <div class="mb-3" data-wcp-kind="proxy">
                        <label class="form-label" for="f-proxy">Upstream address</label>
                        <input class="form-control<?= $cls('proxy_pass') ?>" id="f-proxy" name="proxy_pass" value="<?= e($v('proxy_pass')) ?>" placeholder="http://127.0.0.1:3000">
                        <?= $err('proxy_pass') ?>
                        <div class="form-text">Your app must listen on this local port (run it as a Worker).</div>
                    </div>
                    <div class="mb-0">
                        <label class="form-label" for="f-docsub">Document root</label>
                        <div class="input-group">
                            <span class="input-group-text small"><?= e($site['root'] ?? '<sites root>/<domain>') ?>/</span>
                            <input class="form-control<?= $cls('document_root_sub') ?>" id="f-docsub" name="document_root_sub" value="<?= e($old['document_root_sub'] ?? ($docSub !== '' ? $docSub : 'public_html')) ?>">
                        </div>
                        <?= $err('document_root_sub') ?>
                        <div class="form-text">For Laravel use <code>public_html/public</code>.</div>
                    </div>
                </div>
            </div>
        </div>
        <div class="col-lg-6">
            <?php if ($serverPhp): /* Admins/operators set the account and owner; a hosting customer owns what they create and the account is derived from the domain. */ ?>
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-person me-1"></i>System user</div>
                <div class="card-body">
                    <input class="form-control<?= $cls('system_user') ?>" name="system_user" value="<?= e($v('system_user')) ?>" placeholder="derived from the domain" <?= $isNew ? '' : 'disabled' ?> maxlength="31" aria-label="System user">
                    <?= $err('system_user') ?>
                    <div class="form-text">PHP, cron jobs, workers and deployments run as this Linux account. It is created if it does not exist.</div>
                    <?php if (!empty($owners)): ?>
                        <label class="form-label mt-3" for="f-owner">Owner</label>
                        <select class="form-select" id="f-owner" name="owner_id">
                            <option value="">— none (administrators only) —</option>
                            <?php foreach ($owners as $o): ?>
                                <?php if (($o['role_scope'] ?? 'all') === 'own'): ?>
                                    <option value="<?= (int) $o['id'] ?>"<?= (string) ($old['owner_id'] ?? '') === (string) $o['id'] ? ' selected' : '' ?>><?= e($o['username']) ?> (<?= e($o['role_label']) ?>)</option>
                                <?php endif; ?>
                            <?php endforeach; ?>
                        </select>
                    <?php endif; ?>
                </div>
            </div>
            <?php endif; ?>
            <?php if ($serverPhp || !$isNew): ?>
            <div class="card mb-3" data-wcp-kind="php" id="php">
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
                        <div class="mt-1<?= $profileNow === 'custom' ? '' : ' d-none' ?>" data-wcp-php-custom>
                            <input class="form-control form-control-sm<?= $cls('disable_functions') ?>" name="disable_functions" value="<?= htmlspecialchars((string) $v('disable_functions', '')) ?>" placeholder="exec, system, shell_exec">
                            <div class="form-text">Comma-separated function names to disable.</div>
                        </div>
                    </div>
                    <script>
                    (function () {
                        var sel = document.getElementById('f-php-profile');
                        if (!sel) return;
                        sel.addEventListener('change', function () {
                            var box = sel.closest('.mt-3').querySelector('[data-wcp-php-custom]');
                            if (box) box.classList.toggle('d-none', sel.value !== 'custom');
                        });
                    })();
                    </script>
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
            <?php endif; ?>
            <?php if (!$isNew): $ini = (array) ($old['ini'] ?? ($site['php_settings']['ini'] ?? [])); $iniErr = array_filter($errors ?? [], static fn ($k) => str_starts_with((string) $k, 'ini'), ARRAY_FILTER_USE_KEY); ?>
            <div class="card mb-3" data-wcp-kind="php" id="php-ini">
                <div class="card-header wcp-toolbar">
                    <span class="wcp-toolbar-title"><i class="bi bi-sliders2 me-1"></i>Additional PHP directives <span class="badge rounded-pill text-bg-light border fw-normal"><?= count(array_filter($ini, static fn ($x) => $x !== '')) ?> set</span></span>
                    <input class="form-control form-control-sm wcp-filter" type="search" placeholder="Filter directives" aria-label="Filter directives" data-wcp-filter="#php-ini-table">
                </div>
                <?php if (isset($iniErr['ini'])): ?><div class="text-danger small px-3 pt-2"><?= e((string) $iniErr['ini']) ?></div><?php endif; ?>
                <div class="table-responsive wcp-scroll-400">
                    <table class="table table-sm align-middle mb-0 wcp-table" id="php-ini-table">
                        <tbody>
                        <?php foreach (\WhaCP\Services\PhpDirectives::groups() as $group => $keys): ?>
                            <tr class="table-light"><th colspan="2" class="small"><?= e($group) ?></th></tr>
                            <?php foreach ($keys as $k): [, $kind, $hint] = \WhaCP\Services\PhpDirectives::ALL[$k]; $val = (string) ($ini[$k] ?? ''); $id = 'ini-' . str_replace('.', '-', $k); $bad = isset($iniErr['ini.' . $k]); ?>
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
                        <tr class="d-none" data-wcp-filter-empty><td colspan="2" class="small text-body-secondary">No directive matches.</td></tr>
                        </tbody>
                    </table>
                </div>
            </div>
            <?php endif; ?>
            <div class="card mb-3" id="requests">
                <div class="card-header"><i class="bi bi-signpost me-1"></i>Requests and redirects</div>
                <div class="card-body">
                    <label class="form-label small" for="f-body">Max request body (MB)</label>
                    <input class="form-control form-control-sm mb-3<?= $cls('client_max_body_mb') ?>" type="number" id="f-body" name="client_max_body_mb" value="<?= e($v('client_max_body_mb', 64)) ?>">
                    <?= $err('client_max_body_mb') ?>
                    <label class="form-label small" for="f-redirects">Redirects</label>
                    <textarea class="form-control form-control-sm font-monospace<?= $cls('redirects') ?>" id="f-redirects" name="redirects" rows="3" placeholder="/old-page https://example.com/new-page 301"><?= e($redirects) ?></textarea>
                    <?= $err('redirects') ?>
                </div>
            </div>
        </div>
    </div>
    <div class="d-flex gap-2 align-items-center">
        <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= $isNew ? 'Create website' : 'Save and apply' ?></button>
    </div>
</form>
