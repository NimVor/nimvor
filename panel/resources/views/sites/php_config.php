<?php
/**
 * A website's PHP configuration files: the customer's php.ini and .user.ini as NimVor found them, what each
 * directive does, and the effective values with the source of each.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var array<string, mixed>|null $phpConfig
 */
$cfg = $phpConfig ?? null;
$statusClass = ['active' => 'text-bg-success', 'pending' => 'text-bg-warning', 'error' => 'text-bg-danger'];
$stateClass = ['applied' => 'text-bg-success', 'locked' => 'text-bg-secondary', 'system' => 'text-bg-secondary', 'unsupported' => 'text-bg-warning', 'invalid' => 'text-bg-danger', 'overridden' => 'text-bg-light border', 'unchecked' => 'text-bg-light border'];
$stateLabel = ['applied' => 'In effect', 'locked' => 'Locked', 'system' => 'Server only', 'unsupported' => 'Not supported', 'invalid' => 'Invalid', 'overridden' => 'Replaced', 'unchecked' => 'Not checked'];
$sourceLabel = ['nimvor' => 'NimVor website setting', 'nimvor-locked' => 'NimVor (locked)', 'php.ini' => 'php.ini', '.user.ini' => '.user.ini'];
$yes = static fn (bool $b): string => $b ? '<span class="badge text-bg-success">Yes</span>' : '<span class="badge text-bg-light border">No</span>';
$files = [];
if ($cfg !== null) {
    $files = array_values(array_filter(array_merge((array) ($cfg['php_ini'] ?? []), [(array) ($cfg['user_ini'] ?? [])]), static fn ($f): bool => !empty($f['exists'])));
}
$phpIniFound = false;
foreach ((array) ($cfg['php_ini'] ?? []) as $f) {
    $phpIniFound = $phpIniFound || !empty($f['exists']);
}
$last = (string) ($cfg['last_change'] ?? '');
$hasLast = $last !== '' && !str_starts_with($last, '0001');
?>
<div class="card mb-3" id="php-config">
    <div class="card-header nim-toolbar">
        <span class="nim-toolbar-title"><i class="bi bi-file-earmark-code me-1"></i>PHP configuration files
            <?php if ($cfg !== null): ?><span class="badge rounded-pill <?= e($statusClass[(string) ($cfg['status'] ?? '')] ?? 'text-bg-secondary') ?> fw-normal"><?= e(ucfirst((string) ($cfg['status'] ?? 'unknown'))) ?></span><?php endif; ?>
        </span>
        <span class="d-flex gap-2">
            <form method="post" action="<?= e($this->url('sites.php.config.check', ['id' => (int) $site['id']])) ?>" data-nim-busy>
                <?= $this->csrfField() ?>
                <button class="btn btn-sm btn-outline-secondary" type="submit"><i class="bi bi-arrow-repeat me-1"></i>Check now</button>
            </form>
            <?php if (!empty($site['php_settings']['ini'])): ?>
            <form method="post" action="<?= e($this->url('sites.php.config.reset', ['id' => (int) $site['id']])) ?>" data-nim-busy data-nim-confirm="Remove the website-level PHP directives set on this page? Your own php.ini and .user.ini files are not changed.">
                <?= $this->csrfField() ?>
                <button class="btn btn-sm btn-outline-danger" type="submit"><i class="bi bi-eraser me-1"></i>Reset website overrides</button>
            </form>
            <?php endif; ?>
        </span>
    </div>
    <div class="card-body">
        <?php if ($cfg === null): ?>
            <p class="small text-body-secondary mb-0">The server did not answer, so the PHP configuration cannot be shown right now. Your files still work: PHP reads <code>.user.ini</code> itself.</p>
        <?php else: ?>
            <p class="small text-body-secondary">Create a <code>.user.ini</code> or <code>php.ini</code> in the website's document root (or a <code>php.ini</code> in the website folder above it). NimVor notices a new, changed, renamed or deleted file on its own, within seconds; nothing needs to be saved. These files are never shown to visitors and are never run as code.</p>
            <div class="table-responsive mb-3">
                <table class="table table-sm align-middle mb-0 nim-table">
                    <tbody>
                    <tr><th class="w-25 small">PHP version</th><td>PHP <?= e((string) ($cfg['php_version'] ?? '')) ?></td></tr>
                    <tr><th class="small">php.ini detected</th><td><?= $yes($phpIniFound) ?></td></tr>
                    <tr><th class="small">.user.ini detected</th><td><?= $yes(!empty($cfg['user_ini']['exists'])) ?></td></tr>
                    <tr><th class="small">Configuration status</th><td><span class="badge <?= e($statusClass[(string) ($cfg['status'] ?? '')] ?? 'text-bg-secondary') ?>"><?= e(ucfirst((string) ($cfg['status'] ?? 'unknown'))) ?></span>
                        <?php if ((string) ($cfg['message'] ?? '') !== ''): ?><span class="small ms-2"><?= e((string) $cfg['message']) ?></span><?php endif; ?></td></tr>
                    <tr><th class="small">Last detected change</th><td><?= $hasLast ? '<time datetime="' . e($last) . '">' . e(str_replace(['T', 'Z'], [' ', ' UTC'], $last)) . '</time>' : '<span class="text-body-secondary">None yet</span>' ?></td></tr>
                    <tr><th class="small">.user.ini refresh</th><td class="small">PHP re-reads a changed <code>.user.ini</code> within <?= (int) ($cfg['user_ini_cache_ttl'] ?? 300) ?> seconds (its <code>user_ini.cache_ttl</code>). A changed <code>php.ini</code> is applied by NimVor, with a graceful PHP-FPM reload.</td></tr>
                    </tbody>
                </table>
            </div>

            <?php foreach ($files as $f): ?>
                <h3 class="h6 mt-3"><code><?= e((string) $f['name']) ?></code> <span class="small text-body-secondary">(<?= e((string) $f['where']) ?>, <?= e(number_format((int) ($f['size'] ?? 0))) ?> bytes)</span></h3>
                <?php if ((string) ($f['problem'] ?? '') !== ''): ?>
                    <div class="alert alert-warning small py-2">This file is not used: <?= e((string) $f['problem']) ?></div>
                <?php elseif (empty($f['directives'])): ?>
                    <p class="small text-body-secondary">The file has no settings.</p>
                <?php else: ?>
                    <div class="table-responsive nim-scroll-400">
                        <table class="table table-sm align-middle mb-0 nim-table">
                            <thead><tr><th class="small">Line</th><th class="small">Directive</th><th class="small">Value</th><th class="small">What happens</th></tr></thead>
                            <tbody>
                            <?php foreach ($f['directives'] as $d): $st = (string) ($d['state'] ?? ''); ?>
                                <tr>
                                    <td class="small text-body-secondary"><?= (int) ($d['line'] ?? 0) > 0 ? (int) $d['line'] : '' ?></td>
                                    <td class="font-monospace small"><?= e((string) ($d['key'] ?? '')) ?></td>
                                    <td class="font-monospace small text-break"><?= e((string) ($d['value'] ?? '')) ?></td>
                                    <td class="small"><span class="badge <?= e($stateClass[$st] ?? 'text-bg-light border') ?>"><?= e($stateLabel[$st] ?? $st) ?></span>
                                        <?php if ((string) ($d['reason'] ?? '') !== ''): ?><span class="text-body-secondary ms-1"><?= e((string) $d['reason']) ?></span><?php endif; ?></td>
                                </tr>
                            <?php endforeach; ?>
                            </tbody>
                        </table>
                    </div>
                <?php endif; ?>
            <?php endforeach; ?>

            <?php if (!empty($cfg['effective'])): ?>
                <h3 class="h6 mt-3">Effective settings and where each comes from</h3>
                <div class="table-responsive nim-scroll-400">
                    <table class="table table-sm align-middle mb-0 nim-table">
                        <thead><tr><th class="small">Setting</th><th class="small">Value</th><th class="small">Source</th></tr></thead>
                        <tbody>
                        <?php foreach ($cfg['effective'] as $row): ?>
                            <tr>
                                <td class="font-monospace small"><?= e((string) $row['key']) ?></td>
                                <td class="font-monospace small text-break"><?= e((string) $row['value']) ?></td>
                                <td class="small"><?= e($sourceLabel[(string) ($row['source'] ?? '')] ?? (string) ($row['source'] ?? '')) ?><?php if ((string) ($row['detail'] ?? '') !== ''): ?> <span class="text-body-secondary">(<?= e((string) $row['detail']) ?>)</span><?php endif; ?></td>
                            </tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
                <p class="small text-body-secondary mt-1 mb-0">Only settings NimVor or your files set are listed; everything else is the server's PHP default.</p>
            <?php endif; ?>

            <details class="mt-3">
                <summary class="small fw-semibold">Which setting wins, and what cannot be changed</summary>
                <ol class="small mt-2 mb-1">
                    <li>The server's PHP configuration.</li>
                    <li>NimVor's website settings (the plan's limits and the directives set on this page).</li>
                    <li><code>php.ini</code> from the website folder, then from the document root (the nearer one wins).</li>
                    <li><code>.user.ini</code>, which PHP applies itself, so it wins over all of the above, but only for settings PHP lets a <code>.user.ini</code> change.</li>
                </ol>
                <p class="small mb-1">If a file sets the same name twice, the last line wins. Settings that confine the website (<code>open_basedir</code>, temporary folders, <code>disable_functions</code>, the error log, mail sending), extensions, and server-level PHP settings are locked: they are listed as <em>Locked</em> or <em>Server only</em> and have no effect. <?= !empty($cfg['lock_ini']) ? 'The administrator has also locked this website\'s memory, upload and time limits.' : '' ?></p>
            </details>
        <?php endif; ?>
    </div>
</div>
