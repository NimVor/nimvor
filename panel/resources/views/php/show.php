<?php
/** @var \WhaCP\View\View $this */
/** @var string $version */
/** @var string $tab */
/** @var list<array<string, mixed>> $extensions */
/** @var array<string, mixed>|null $ini */
/** @var array<string, mixed>|null $fpm */
$base = '/php/' . rawurlencode($version);
$iniValues = (array) ($ini['values'] ?? []);
$effective = (array) ($ini['effective'] ?? []);
$fpmSettings = (array) ($fpm['settings'] ?? []);
?>
<?php $this->start(); ?>PHP <?= e($version) ?><?php $this->end('title'); ?>
<?php $this->start(); ?>Extensions, php.ini and PHP-FPM settings<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
<?php $this->end('actions'); ?>

<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>

<ul class="nav nav-tabs wcp-tabs mb-3">
    <?php foreach (['extensions' => ['Extensions', 'puzzle'], 'ini' => ['php.ini', 'sliders'], 'fpm' => ['PHP-FPM', 'cpu']] as $key => [$label, $icon]): ?>
        <li class="nav-item"><a class="nav-link<?= $tab === $key ? ' active' : '' ?>" href="<?= e($base . '?tab=' . $key) ?>"><i class="bi bi-<?= e($icon) ?> me-1"></i><?= e($label) ?></a></li>
    <?php endforeach; ?>
</ul>

<?php if ($tab === 'extensions'): ?>
    <div class="card">
        <div class="card-body small text-body-secondary border-bottom">Install and remove use the server's package manager (only the extension's own package is removed, never others that need it). Switching on or off applies to PHP-FPM and the command line; PHP-FPM is checked and reloaded, and a change it rejects is undone.</div>
        <div class="table-responsive">
            <table class="table table-hover align-middle mb-0 wcp-table" data-wcp-php-extensions>
                <thead><tr><th>Extension</th><th class="d-none d-md-table-cell">Package</th><th>State</th><th class="text-end">Actions</th></tr></thead>
                <tbody>
                <?php foreach ($extensions as $x): ?>
                    <tr data-wcp-ext="<?= e($x['name']) ?>">
                        <td><strong><?= e($x['name']) ?></strong><div class="small text-body-secondary"><?= e($x['description']) ?></div></td>
                        <td class="d-none d-md-table-cell small"><?= $x['package'] !== '' ? '<code>' . e($x['package']) . '</code>' : '<span class="text-body-secondary">part of PHP ' . e($version) . '</span>' ?><?php if (!empty($x['version'])): ?><div class="text-body-secondary"><?= e($x['version']) ?></div><?php endif; ?></td>
                        <td>
                            <?php if (!$x['installed']): ?><span class="badge text-bg-secondary">Not installed</span>
                            <?php elseif ($x['enabled']): ?><span class="badge text-bg-success"><i class="bi bi-check-circle"></i> On</span>
                            <?php else: ?><span class="badge text-bg-warning">Off</span><?php endif; ?>
                            <?php if (!empty($x['protected'])): ?><i class="bi bi-shield-lock ms-1 text-body-secondary" title="<?= e($x['protected']) ?>"></i><?php endif; ?>
                        </td>
                        <td class="text-end text-nowrap">
                            <?php if (!$x['installed'] && $x['package'] !== ''): ?>
                                <form class="d-inline" method="post" action="<?= e($base . '/extensions/install') ?>" data-wcp-busy><?= $this->csrfField() ?><input type="hidden" name="name" value="<?= e($x['name']) ?>"><button class="btn btn-sm btn-outline-primary">Install</button></form>
                            <?php elseif ($x['installed']): ?>
                                <?php if (empty($x['protected']) || !$x['enabled']): ?>
                                    <form class="d-inline" method="post" action="<?= e($base . '/extensions/toggle') ?>" data-wcp-busy><?= $this->csrfField() ?><input type="hidden" name="name" value="<?= e($x['name']) ?>"><input type="hidden" name="enabled" value="<?= $x['enabled'] ? '0' : '1' ?>"><button class="btn btn-sm btn-outline-<?= $x['enabled'] ? 'warning' : 'success' ?>"><?= $x['enabled'] ? 'Switch off' : 'Switch on' ?></button></form>
                                <?php endif; ?>
                                <?php if ($x['package'] !== '' && empty($x['protected'])): ?>
                                    <form class="d-inline" method="post" action="<?= e($base . '/extensions/remove') ?>" data-wcp-busy data-wcp-confirm="Remove <?= e($x['name']) ?> from PHP <?= e($version) ?>? Websites using it will fail."><?= $this->csrfField() ?><input type="hidden" name="name" value="<?= e($x['name']) ?>"><button class="btn btn-sm btn-outline-danger">Remove</button></form>
                                <?php endif; ?>
                            <?php endif; ?>
                        </td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
        <div class="card-footer small text-body-secondary">All modules PHP <?= e($version) ?> loads (including built-in ones) are listed on the PHP page under “Modules”.</div>
    </div>

<?php elseif ($tab === 'ini'): ?>
    <?php $directives = (array) ($ini['directives'] ?? []); ?>
    <div class="alert alert-info small">
        These settings go to NimVor's own file <code><?= e((string) ($ini['file'] ?? '')) ?></code>, loaded after the distribution's php.ini, which is never changed. Empty fields keep PHP's value (shown on the right, as PHP-FPM uses it now). Settings marked <span class="badge text-bg-light border">per website</span> are set by each NimVor website itself (Website → PHP &amp; settings); here they apply to other pools and the command line.
    </div>
    <div class="row g-3">
        <div class="col-xl-7">
            <form class="card" method="post" action="<?= e($base . '/ini') ?>" data-wcp-busy>
                <?= $this->csrfField() ?>
                <input type="hidden" name="mode" value="form">
                <div class="card-header"><i class="bi bi-sliders me-1"></i>Settings</div>
                <div class="table-responsive">
                    <table class="table table-sm align-middle mb-0">
                        <thead><tr><th>Setting</th><th class="wcp-minw-11">Value</th><th class="d-none d-md-table-cell">PHP-FPM now</th></tr></thead>
                        <tbody>
                        <?php foreach ($directives as $dir): ?>
                            <?php $key = (string) $dir['key']; $cur = trim((string) ($iniValues[$key] ?? ''), '"'); ?>
                            <tr>
                                <td><code class="small"><?= e($key) ?></code><?php if (!empty($dir['per_website'])): ?> <span class="badge text-bg-light border">per website</span><?php endif; ?><div class="small text-body-secondary"><?= e($dir['description']) ?></div></td>
                                <td>
                                    <?php if ($dir['kind'] === 'bool' || $dir['kind'] === 'choice'): ?>
                                        <select class="form-select form-select-sm" name="ini[<?= e($key) ?>]" aria-label="<?= e($key) ?>">
                                            <option value="">— PHP's value —</option>
                                            <?php foreach ($dir['kind'] === 'bool' ? ['On', 'Off'] : (array) ($dir['choices'] ?? []) as $c): ?>
                                                <option value="<?= e($c) ?>"<?= strcasecmp($cur, (string) $c) === 0 ? ' selected' : '' ?>><?= e($c) ?></option>
                                            <?php endforeach; ?>
                                        </select>
                                    <?php else: ?>
                                        <input class="form-control form-control-sm" name="ini[<?= e($key) ?>]" value="<?= e($cur) ?>" aria-label="<?= e($key) ?>"
                                               placeholder="<?= e($dir['kind'] === 'size' ? 'e.g. 256M' : ($dir['kind'] === 'timezone' ? 'e.g. Europe/Berlin' : ($dir['kind'] === 'int' ? (int) ($dir['min'] ?? 0) . '–' . (int) ($dir['max'] ?? 0) : ''))) ?>">
                                    <?php endif; ?>
                                </td>
                                <td class="d-none d-md-table-cell small text-body-secondary"><?= e((string) ($effective[$key] ?? '')) ?></td>
                            </tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
                <div class="card-footer text-end"><button class="btn btn-primary btn-sm">Save and reload PHP-FPM</button></div>
            </form>
        </div>
        <div class="col-xl-5">
            <form class="card" method="post" action="<?= e($base . '/ini') ?>" data-wcp-busy>
                <?= $this->csrfField() ?>
                <input type="hidden" name="mode" value="text">
                <div class="card-header"><i class="bi bi-code me-1"></i>Text editor</div>
                <div class="card-body">
                    <p class="small text-body-secondary">One <code>setting = value</code> per line; <code>;</code> starts a comment. Only the settings listed on the left are accepted. Empty removes NimVor's file.</p>
                    <textarea class="form-control font-monospace small" name="content" rows="16" spellcheck="false" aria-label="php.ini settings"><?php foreach ($directives as $dir): ?><?php if (isset($iniValues[$dir['key']])): ?><?= e($dir['key'] . ' = ' . $iniValues[$dir['key']]) . "\n" ?><?php endif; ?><?php endforeach; ?></textarea>
                </div>
                <div class="card-footer text-end"><button class="btn btn-primary btn-sm">Save and reload PHP-FPM</button></div>
            </form>
        </div>
    </div>

<?php else: ?>
    <div class="row g-3">
        <div class="col-xl-7">
            <form class="card" method="post" action="<?= e($base . '/fpm') ?>" data-wcp-busy>
                <?= $this->csrfField() ?>
                <div class="card-header"><i class="bi bi-cpu me-1"></i>Server-wide PHP-FPM settings of PHP <?= e($version) ?></div>
                <div class="card-body">
                    <p class="small text-body-secondary">Written to NimVor's <code><?= e((string) ($fpm['file'] ?? '')) ?></code>, which PHP-FPM loads with its pools. Leave a field empty (or 0) to keep PHP-FPM's default. Each website's process manager, processes and timeouts are in Website → PHP &amp; settings → Advanced PHP-FPM.</p>
                    <div class="row g-3">
                        <?php foreach ([
                            ['emergency_restart_threshold', 'Emergency restart after this many failed processes', 'e.g. 10'],
                            ['emergency_restart_interval_seconds', '… within (seconds)', 'e.g. 60'],
                            ['process_control_timeout_seconds', 'Seconds processes get to stop or reload', 'e.g. 10'],
                            ['process_max', 'Most processes of all pools together (0: unlimited)', 'e.g. 128'],
                        ] as [$k, $label, $ph]): ?>
                            <div class="col-md-6">
                                <label class="form-label small" for="fpm-<?= e($k) ?>"><?= e($label) ?></label>
                                <input class="form-control form-control-sm" type="number" min="0" id="fpm-<?= e($k) ?>" name="<?= e($k) ?>" value="<?= (int) ($fpmSettings[$k] ?? 0) ?: '' ?>" placeholder="<?= e($ph) ?>">
                            </div>
                        <?php endforeach; ?>
                        <div class="col-md-6">
                            <label class="form-label small" for="fpm-log_level">Log level</label>
                            <select class="form-select form-select-sm" id="fpm-log_level" name="log_level">
                                <option value="">— PHP-FPM's default (notice) —</option>
                                <?php foreach ((array) ($fpm['log_levels'] ?? []) as $l): ?><option value="<?= e($l) ?>"<?= ($fpmSettings['log_level'] ?? '') === $l ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?>
                            </select>
                        </div>
                    </div>
                </div>
                <div class="card-footer text-end"><button class="btn btn-primary btn-sm">Save and reload PHP-FPM</button></div>
            </form>
        </div>
    </div>
<?php endif; ?>
