<?php
/**
 * One migration, on the step it has reached.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $m
 * @var string $step connect | discover | select | assess | run | report
 * @var array<string, mixed> $state the agent's state (run, report)
 * @var list<array<string, mixed>> $sites
 * @var list<array<string, mixed>> $databases
 * @var array<string, string> $errors
 * @var array<string, mixed> $old
 */
$id = (int) $m['id'];
$err = static fn (string $k): string => isset($errors[$k]) ? ' is-invalid' : '';
$fb = static function (string $k) use ($errors): string {
    return isset($errors[$k]) ? '<div class="invalid-feedback d-block">' . e($errors[$k]) . '</div>' : '';
};
$steps = ['connect' => 'Connect', 'discover' => 'Discover', 'select' => 'Choose and map', 'assess' => 'Check', 'run' => 'Migrate', 'report' => 'Report'];
$reached = ['draft' => 'connect', 'connected' => 'discover', 'discovering' => 'discover', 'discovered' => 'select', 'selected' => 'assess', 'assessed' => 'assess'][$m['status']] ?? ($m['status'] === 'completed' ? 'report' : 'run');
$order = array_keys($steps);
$disc = (array) $m['discovery'];
$plan = (array) $m['plan'];
$asm = (array) $m['assessment'];
$pending = (array) $m['pending_key'];
$info = (array) ($m['info']['server'] ?? []);
$sev = ['critical' => ['Critical', 'danger'], 'warning' => ['Warning', 'warning'], 'info' => ['Info', 'secondary']];
$fmt = static fn (int $kb): string => $kb < 0 ? 'unknown' : ($kb >= 1048576 ? number_format($kb / 1048576, 1) . ' GiB' : ($kb >= 1024 ? number_format($kb / 1024, 0) . ' MiB' : $kb . ' KiB'));
$running = in_array($m['status'], ['running', 'discovering'], true);
?>
<?php $this->start(); ?>Migration #<?= $id ?><?php $this->end('title'); ?>
<?php $this->start(); ?><?= e((string) $m['panel']) ?><?= $m['source_host'] !== '' ? ' · ' . e($m['source_user'] . '@' . $m['source_host']) : '' ?><?php $this->end('subtitle'); ?>

<div class="nim-subtabs mb-3" aria-label="Steps">
    <?php foreach ($steps as $k => $label):
        $done = array_search($k, $order, true) < array_search($reached, $order, true);
        $canOpen = $done && in_array($k, ['connect', 'discover', 'select', 'assess'], true) && !in_array($m['status'], ['running', 'completed', 'cancelled'], true);
        ?>
        <?php if ($canOpen): ?><a class="btn btn-sm <?= $k === $step ? 'btn-primary' : 'btn-outline-secondary' ?>" href="<?= e($this->url('migrations.show', ['id' => $id], ['step' => $k])) ?>"><i class="bi bi-check2 me-1"></i><?= e($label) ?></a>
        <?php else: ?><span class="btn btn-sm <?= $k === $step ? 'btn-primary' : 'btn-outline-secondary disabled' ?>"><?= e($label) ?></span><?php endif; ?>
    <?php endforeach; ?>
</div>
<?php if ($m['error'] !== '' && $step !== 'run'): ?><div class="alert alert-danger small"><?= e((string) $m['error']) ?></div><?php endif; ?>

<?php if ($step === 'connect'): ?>
<div class="row g-3">
    <div class="col-lg-7">
        <div class="card"><div class="card-header"><i class="bi bi-plug me-1"></i>Connect to the source server</div><div class="card-body">
            <form method="post" action="<?= e($this->url('migrations.probe', ['id' => $id])) ?>" data-nim-busy class="row g-2 mb-3">
                <?= $this->csrfField() ?>
                <div class="col-md-6"><label class="form-label" for="mg-host">Host name or IP address</label><input class="form-control<?= $err('host') ?>" id="mg-host" name="host" value="<?= e((string) ($old['host'] ?? $pending['host'] ?? $m['source_host'])) ?>" placeholder="old-server.example.com" maxlength="253"><?= $fb('host') ?></div>
                <div class="col-md-2"><label class="form-label" for="mg-port">SSH port</label><input class="form-control<?= $err('port') ?>" id="mg-port" name="port" type="number" min="1" max="65535" value="<?= e((string) ($old['port'] ?? $pending['port'] ?? '')) ?>" placeholder="22"><?= $fb('port') ?></div>
                <div class="col-md-4"><label class="form-label" for="mg-user">SSH user</label><input class="form-control<?= $err('user') ?>" id="mg-user" name="user" value="<?= e((string) ($old['user'] ?? $pending['user'] ?? 'root')) ?>" maxlength="100" autocomplete="off"><?= $fb('user') ?></div>
                <div class="col-12"><button class="btn btn-sm btn-outline-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Check the server</button> <span class="small text-body-secondary">Asks the server for its SSH key. No login is sent.</span></div>
            </form>
            <?php if (!empty($pending['host_key'])): ?>
            <div class="alert alert-warning small">
                <div class="fw-semibold mb-1"><i class="bi bi-shield-check me-1"></i>Confirm the server's SSH key</div>
                <div><strong>Server</strong> <code><?= e($pending['host'] . ':' . $pending['port']) ?></code></div>
                <div><strong>Key</strong> <code class="text-break"><?= e($pending['key_type'] . ' ' . $pending['fingerprint']) ?></code></div>
                <div class="mt-1">Continue only if it matches the server's key (<code>ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub</code> on the source). The login is sent only to a server with exactly this key.</div>
            </div>
            <form method="post" action="<?= e($this->url('migrations.connect', ['id' => $id])) ?>" autocomplete="off" data-nim-busy>
                <?= $this->csrfField() ?>
                <div class="mb-2"><label class="form-label" for="mg-password">Password</label><input class="form-control<?= $err('password') ?>" id="mg-password" name="password" type="password" autocomplete="new-password" maxlength="500"><?= $fb('password') ?></div>
                <div class="mb-2"><label class="form-label" for="mg-key">Or a private key</label><textarea class="form-control font-monospace small<?= $err('private_key') ?>" id="mg-key" name="private_key" rows="3" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" autocomplete="off" spellcheck="false"></textarea><?= $fb('private_key') ?><div class="form-text">An unencrypted key whose public key is in the source's authorized_keys. Use a key made for this migration and remove it afterwards.</div></div>
                <button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Test connection</button>
            </form>
            <?php endif; ?>
        </div></div>
    </div>
    <div class="col-lg-5">
        <div class="card"><div class="card-header"><i class="bi bi-lock me-1"></i>How the login is handled</div><div class="card-body small">
            <ul class="mb-0">
                <li>The login is encrypted here and sent to the agent only when a step needs it; it is never logged or shown again.</li>
                <li>It is erased when the migration completes or is cancelled, or when you choose “Erase the stored login”.</li>
                <li>Everything on the source is read only; nothing is changed or deleted there.</li>
                <li>Use <strong>root</strong>, or a user that can read every website and database you want to move.</li>
            </ul>
        </div></div>
    </div>
</div>
<?php elseif ($step === 'discover'): ?>
<div class="card mb-3"><div class="card-header"><i class="bi bi-hdd-network me-1"></i>The source server</div><div class="card-body small">
    <div class="row">
        <div class="col-md-4"><strong>System</strong><br><?= e((string) ($info['os'] ?? '?')) ?></div>
        <div class="col-md-4"><strong>Panel</strong><br><?= e((string) (($m['info']['panel']['name'] ?? '') ?: 'not recognised')) ?> <?= e((string) ($m['info']['panel']['version'] ?? '')) ?> <span class="badge text-bg-warning"><?= e((string) ($m['info']['panel']['support'] ?? '')) ?></span></div>
        <div class="col-md-4"><strong>Database</strong><br><?= e(trim((string) ($info['engine'] ?? '') . ' ' . (string) ($info['mysql'] ?? '')) ?: 'none found') ?></div>
        <div class="col-md-4 mt-2"><strong>PHP</strong><br><?= e(implode(', ', (array) ($info['php'] ?? [])) ?: 'none found') ?></div>
        <div class="col-md-4 mt-2"><strong>Login</strong><br><?= !empty($info['root']) ? 'root' : '<span class="text-warning-emphasis">not root</span>' ?></div>
        <div class="col-md-4 mt-2"><strong>Tools</strong><br>rsync <?= !empty($info['rsync']) ? '✓' : '✗' ?> · mysqldump <?= !empty($info['mysqldump']) ? '✓' : '✗' ?></div>
    </div>
</div></div>
<div class="d-flex gap-2 align-items-center">
    <?php if ($m['status'] === 'discovering'): ?><span class="nim-state is-warn"><span class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>Discovering… this page refreshes by itself.</span>
    <?php else: ?><form method="post" action="<?= e($this->url('migrations.discover', ['id' => $id])) ?>" data-nim-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Discover accounts, websites and databases</button></form><?php endif; ?>
    <form method="post" action="<?= e($this->url('migrations.forget', ['id' => $id])) ?>" data-nim-confirm="Erase the stored login? You will have to connect again to continue."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary">Erase the stored login</button></form>
</div>
<?php elseif ($step === 'select'): ?>
<form method="post" action="<?= e($this->url('migrations.select', ['id' => $id])) ?>">
    <?= $this->csrfField() ?>
    <?php foreach ((array) ($disc['warnings'] ?? []) as $w): ?><div class="alert alert-warning small mb-2"><?= e((string) $w) ?></div><?php endforeach; ?>
    <?= $err('domains') !== '' ? $fb('domains') : '' ?>
    <div class="nim-panel mb-3" data-nim-dt>
        <div class="nim-actionbar"><div class="fw-semibold">Websites to copy <span class="badge rounded-pill text-bg-light border fw-normal">into NimVor websites that already exist</span></div>
            <div class="nim-actionbar-end"><div class="nim-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search websites" data-nim-dt-search><i class="bi bi-search" aria-hidden="true"></i></div></div></div>
        <div class="nim-dtable table-responsive"><table class="table align-middle">
            <thead><tr><th class="nim-check"><input class="form-check-input" type="checkbox" data-nim-select-all="domains[]" aria-label="Select all websites"></th><th>Account</th><th>Website</th><th>Folder on the source</th><th class="text-end">Size</th><th>Copy into</th></tr></thead>
            <tbody>
            <?php foreach ((array) ($disc['accounts'] ?? []) as $a): foreach ((array) $a['domains'] as $d): $key = $a['id'] . '|' . $d['name']; $sel = array_column((array) ($m['selection']['domains'] ?? []), 'site_id', 'key'); ?>
                <tr data-nim-row>
                    <td><input class="form-check-input" type="checkbox" name="domains[]" value="<?= e($key) ?>"<?= isset($sel[$key]) ? ' checked' : '' ?> aria-label="Select <?= e((string) $d['name']) ?>"></td>
                    <td class="small"><?= e((string) $a['name']) ?></td>
                    <td class="fw-semibold"><?= e((string) $d['name']) ?> <span class="badge text-bg-light border fw-normal"><?= e((string) $d['kind']) ?></span></td>
                    <td class="small font-monospace text-break"><?= e((string) $d['doc_root']) ?></td>
                    <td class="small text-end text-nowrap"><?= e($fmt((int) ($d['size_kb'] ?? -1))) ?></td>
                    <td><select class="form-select form-select-sm<?= $err('site[' . $key . ']') ?>" name="site[<?= e($key) ?>]" aria-label="NimVor website for <?= e((string) $d['name']) ?>">
                        <option value="">— choose —</option>
                        <?php foreach ($sites as $s): ?><option value="<?= (int) $s['id'] ?>"<?= (int) ($sel[$key] ?? 0) === (int) $s['id'] ? ' selected' : '' ?>><?= e((string) $s['domain']) ?></option><?php endforeach; ?>
                    </select><?= $fb('site[' . $key . ']') ?></td>
                </tr>
            <?php endforeach; endforeach; ?>
            <tr class="d-none" data-nim-dt-empty><td colspan="6" class="text-center py-4 nim-muted">Nothing matches.</td></tr>
            </tbody>
        </table></div>
        <div class="nim-pager" data-nim-dt-pager></div>
    </div>
    <div class="nim-panel mb-3" data-nim-dt>
        <div class="nim-actionbar"><div class="fw-semibold">Databases to copy <span class="badge rounded-pill text-bg-light border fw-normal">into empty NimVor databases</span></div></div>
        <div class="nim-dtable table-responsive"><table class="table align-middle">
            <thead><tr><th class="nim-check"><input class="form-check-input" type="checkbox" data-nim-select-all="dbs[]" aria-label="Select all databases"></th><th>Account</th><th>Database</th><th class="text-end">Size</th><th>Copy into</th></tr></thead>
            <tbody>
            <?php foreach ((array) ($disc['accounts'] ?? []) as $a): foreach ((array) $a['databases'] as $d): $key = $a['id'] . '|' . $d['name']; $dsel = array_column((array) ($m['selection']['databases'] ?? []), 'db_id', 'key'); ?>
                <tr data-nim-row>
                    <td><input class="form-check-input" type="checkbox" name="dbs[]" value="<?= e($key) ?>"<?= isset($dsel[$key]) ? ' checked' : '' ?> aria-label="Select <?= e((string) $d['name']) ?>"></td>
                    <td class="small"><?= e((string) $a['name']) ?></td>
                    <td class="fw-semibold"><?= e((string) $d['name']) ?> <span class="badge text-bg-light border fw-normal"><?= e((string) $d['engine']) ?></span></td>
                    <td class="small text-end text-nowrap"><?= e($fmt((int) $d['size_kb'])) ?></td>
                    <td><select class="form-select form-select-sm<?= $err('dbmap[' . $key . ']') ?>" name="dbmap[<?= e($key) ?>]" aria-label="NimVor database for <?= e((string) $d['name']) ?>">
                        <option value="">— choose —</option>
                        <?php foreach ($databases as $db): ?><option value="<?= (int) $db['id'] ?>"<?= (int) ($dsel[$key] ?? 0) === (int) $db['id'] ? ' selected' : '' ?>><?= e((string) $db['name']) ?></option><?php endforeach; ?>
                    </select><?= $fb('dbmap[' . $key . ']') ?></td>
                </tr>
            <?php endforeach; endforeach; ?>
            <tr class="d-none" data-nim-dt-empty><td colspan="5" class="text-center py-4 nim-muted">Nothing matches.</td></tr>
            </tbody>
        </table></div>
        <div class="nim-pager" data-nim-dt-pager></div>
    </div>
    <?php if (($sites ?? []) === []): ?><div class="alert alert-info small">There is no NimVor website yet. Create the websites that will receive the files under <strong>Websites</strong> (and the databases under <strong>Databases</strong>) first, then come back: a migration fills existing, empty ones.</div><?php endif; ?>
    <div class="card mb-3"><div class="card-header"><i class="bi bi-sliders me-1"></i>How to copy</div><div class="card-body row g-3">
        <div class="col-md-3"><label class="form-label" for="mg-workers">Websites at the same time</label><input class="form-control<?= $err('workers') ?>" id="mg-workers" name="workers" type="number" min="1" max="8" value="<?= e((string) ($old['workers'] ?? $plan['config']['workers'] ?? 2)) ?>"><?= $fb('workers') ?></div>
        <div class="col-md-9"><label class="form-label" for="mg-excludes">Leave out (one pattern per line)</label><textarea class="form-control font-monospace small<?= $err('excludes') ?>" id="mg-excludes" name="excludes" rows="2" placeholder="cache/*&#10;*.log"><?= e(implode("\n", (array) ($plan['config']['excludes'] ?? []))) ?></textarea><?= $fb('excludes') ?></div>
        <div class="col-12">
            <div class="form-check"><input class="form-check-input" type="checkbox" name="compress" value="1" id="mg-z"<?= !empty($plan['config']['compress']) ? ' checked' : '' ?>><label class="form-check-label" for="mg-z">Compress during the transfer (helps on slow links; costs CPU on both servers)</label></div>
            <div class="form-check"><input class="form-check-input" type="checkbox" name="allow_overwrite" value="1" id="mg-ow"<?= !empty($plan['config']['allow_overwrite']) ? ' checked' : '' ?>><label class="form-check-label" for="mg-ow"><strong>Allow overwriting.</strong> By default a website that already has files, or a database that already has tables, is refused. Tick this only if it may be overwritten.</label></div>
        </div>
    </div></div>
    <button class="btn btn-primary btn-sm">Save and check compatibility</button>
</form>
<?php elseif ($step === 'assess'): ?>
<?php if ($asm === []): ?>
<div class="card mb-3"><div class="card-body">
    <p class="mb-2"><strong><?= count((array) ($plan['files'] ?? [])) ?></strong> website(s) and <strong><?= count((array) ($plan['databases'] ?? [])) ?></strong> database(s) will be copied. The check compares them with this server: disk space, temporary space, PHP versions, the database engine and the tools needed on the source.</p>
    <form method="post" action="<?= e($this->url('migrations.assess', ['id' => $id])) ?>" data-nim-busy><?= $this->csrfField() ?><button class="btn btn-primary btn-sm"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Run the compatibility check</button></form>
</div></div>
<?php else: $findings = (array) ($asm['findings'] ?? []); ?>
<div class="card mb-3"><div class="card-header">Readiness report</div>
    <ul class="list-group list-group-flush">
    <?php foreach ($findings as $f): [$t, $c] = $sev[$f['severity']] ?? [(string) $f['severity'], 'secondary']; ?>
        <li class="list-group-item small"><span class="badge text-bg-<?= e($c) ?> me-2"><?= e($t) ?></span><?= e((string) $f['message']) ?><?php if (!empty($f['fix'])): ?><div class="text-body-secondary mt-1"><i class="bi bi-lightbulb me-1"></i><?= e((string) $f['fix']) ?></div><?php endif; ?></li>
    <?php endforeach; ?>
    </ul>
</div>
<div class="d-flex gap-2 flex-wrap">
    <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('migrations.show', ['id' => $id], ['step' => 'select'])) ?>">Change the choices</a>
    <form method="post" action="<?= e($this->url('migrations.assess', ['id' => $id])) ?>" data-nim-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-primary">Check again</button></form>
    <?php if (!empty($asm['blocked'])): ?><button class="btn btn-sm btn-primary" disabled title="Solve the critical problems first">Start the migration</button><span class="small text-danger-emphasis align-self-center">Critical problems stop the migration. Solve them and check again.</span>
    <?php else: ?><form method="post" action="<?= e($this->url('migrations.start', ['id' => $id])) ?>" data-nim-confirm="Start the migration? Files and databases are copied into the NimVor websites and databases you chose<?= !empty($plan['config']['allow_overwrite']) ? ', and existing files and tables MAY BE OVERWRITTEN' : '' ?>. Nothing is changed or deleted on the source."><?= $this->csrfField() ?><button class="btn btn-sm btn-primary"><i class="bi bi-play-fill me-1"></i>Start the migration</button></form><?php endif; ?>
</div>
<?php endif; ?>
<?php elseif ($step === 'run' || $step === 'report'): $tasks = (array) ($state['tasks'] ?? []); ?>
<div class="d-flex align-items-center gap-2 mb-3 flex-wrap">
    <span class="nim-state <?= $m['status'] === 'completed' ? 'is-ok' : (in_array($m['status'], ['failed', 'interrupted'], true) ? 'is-error' : 'is-warn') ?>"><?php if ($m['status'] === 'running'): ?><span class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span><?php endif; ?><?= e(ucfirst((string) $m['status'])) ?></span>
    <?php if ($m['status'] === 'running'): ?>
        <form method="post" action="<?= e($this->url('migrations.pause', ['id' => $id])) ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-pause-fill me-1"></i>Pause</button></form>
    <?php endif; ?>
    <?php if (in_array($m['status'], ['paused', 'failed', 'interrupted'], true)): ?>
        <form method="post" action="<?= e($this->url('migrations.resume', ['id' => $id])) ?>" data-nim-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-primary"><i class="bi bi-play-fill me-1"></i><?= $m['status'] === 'failed' ? 'Try the failed tasks again' : 'Continue' ?></button></form>
    <?php endif; ?>
    <?php if (!in_array($m['status'], ['completed', 'cancelled'], true)): ?>
        <form method="post" action="<?= e($this->url('migrations.cancel', ['id' => $id])) ?>" data-nim-confirm="Cancel this migration? What was copied stays where it is; nothing is removed from the source or the destination. A cancelled migration cannot be continued."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger">Cancel</button></form>
    <?php endif; ?>
    <a class="btn btn-sm btn-outline-secondary ms-auto" href="<?= e($this->url('migrations.report', ['id' => $id])) ?>"><i class="bi bi-download me-1"></i>Report (JSON)</a>
</div>
<?php if ($m['error'] !== ''): ?><div class="alert alert-<?= $m['status'] === 'cancelled' ? 'secondary' : 'danger' ?> small"><?= e((string) $m['error']) ?></div><?php endif; ?>
<div class="nim-panel mb-3" data-fetched-running="<?= $running ? 1 : 0 ?>">
    <div class="nim-dtable table-responsive"><table class="table align-middle">
        <thead><tr><th>Task</th><th>Status</th><th>Tries</th><th>Result</th></tr></thead>
        <tbody>
        <?php foreach ($tasks as $t): $s = (string) $t['status']; ?>
            <tr>
                <td class="fw-semibold"><?= e((string) $t['label']) ?></td>
                <td class="small"><?php if ($s === 'done'): ?><span class="nim-state is-ok">Done</span><?php elseif ($s === 'running'): ?><span class="nim-state is-warn"><span class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>Running…</span><?php elseif ($s === 'failed'): ?><span class="nim-state is-error">Failed</span><?php else: ?><span class="nim-state"><?= e(ucfirst($s)) ?></span><?php endif; ?></td>
                <td class="small"><?= (int) $t['attempts'] ?></td>
                <td class="small text-break"><?= e((string) ($t['detail'] ?? '')) ?><?php if (!empty($t['error'])): ?><div class="text-danger-emphasis"><?= e((string) $t['error']) ?></div><?php endif; ?><?php if ((int) ($t['differences'] ?? 0) > 0): ?><div class="text-warning-emphasis"><i class="bi bi-exclamation-triangle me-1"></i><?= (int) $t['differences'] ?> file(s) changed while they were copied: run the copy again before switching DNS.</div><?php endif; ?></td>
            </tr>
        <?php endforeach; ?>
        <?php if ($tasks === []): ?><tr><td colspan="4" class="text-center py-4 nim-muted">No task has started yet.</td></tr><?php endif; ?>
        </tbody>
    </table></div>
</div>
<?php $rb = (array) ($state['rollback'] ?? []); if ($m['status'] === 'rolled_back' && $rb !== []): ?>
<div class="card mb-3"><div class="card-header"><i class="bi bi-arrow-counterclockwise me-1"></i>Rollback</div><div class="card-body small">
    <p class="mb-1"><?= (int) ($rb['files_removed'] ?? 0) ?> file(s) and <?= (int) ($rb['dirs_removed'] ?? 0) ?> folder(s) the migration created were removed<?= ($rb['databases_emptied'] ?? []) !== [] ? '; databases emptied: ' . e(implode(', ', (array) $rb['databases_emptied'])) : '' ?>. Nothing on the source was touched.</p>
    <?php if (($rb['kept'] ?? []) !== []): ?><div class="fw-semibold mt-2">Kept, because removing it could lose newer data</div><ul class="mb-1"><?php foreach ((array) $rb['kept'] as $k): ?><li class="text-break"><?= e((string) $k) ?></li><?php endforeach; ?></ul><?php endif; ?>
    <?php foreach ((array) ($rb['notes'] ?? []) as $n): ?><div class="text-body-secondary"><?= e((string) $n) ?></div><?php endforeach; ?>
</div></div>
<?php endif; ?>
<?php if (in_array($m['status'], ['completed', 'failed', 'cancelled', 'paused', 'interrupted'], true) && $m['agent_id'] !== ''): ?>
<div class="card mb-3"><div class="card-header"><i class="bi bi-arrow-counterclockwise me-1"></i>Roll back this migration</div><div class="card-body small">
    <p>Removes from this server the files and folders the migration <strong>created</strong>, but only files that are still exactly as the copy left them: a file you changed, or one you added, stays. Files the copy overwrote (only possible when overwriting was allowed) cannot be restored. Nothing on the source is touched.</p>
    <form method="post" action="<?= e($this->url('migrations.rollback', ['id' => $id])) ?>" data-nim-confirm="Roll back this migration? Files it created are removed from this server (changed files are kept). This cannot be undone.">
        <?= $this->csrfField() ?><input type="hidden" name="confirm" value="1">
        <div class="form-check mb-2"><input class="form-check-input" type="checkbox" name="empty_databases" value="1" id="mg-rb-db"><label class="form-check-label" for="mg-rb-db">Also empty the databases it filled. A database is emptied only if it was empty before the migration and nothing was written to it since; otherwise it is kept.</label></div>
        <button class="btn btn-sm btn-outline-danger">Roll back</button>
    </form>
</div></div>
<?php endif; ?>
<?php if (!empty($state['remaining'])): ?><div class="alert alert-warning small"><div class="fw-semibold mb-1">What is left to do</div><ul class="mb-0"><?php foreach ((array) $state['remaining'] as $r): ?><li><?= e((string) $r) ?></li><?php endforeach; ?></ul></div><?php endif; ?>
<?php if ($m['status'] === 'completed'): ?>
<div class="card"><div class="card-header">After the migration</div><div class="card-body small">
    <ul class="mb-0">
        <li>Open each migrated website and check pages, forms and logins. <code>.htaccess</code> rules are not translated to Nginx.</li>
        <li>Nothing was deleted on the source. Keep it until you have checked the new site, then change the DNS records yourself: this version does not change DNS.</li>
        <li>Not moved by this version: email, DNS zones and records, SSL certificates, FTP accounts and cron jobs. The report lists what was found.</li>
        <li>Files were compared by checksum after the copy and databases by table count.</li>
    </ul>
</div></div>
<?php endif; ?>
<?php endif; ?>
<?php $this->start(); ?><script src="<?= e($this->asset('assets/js/migrations.js')) ?>" defer></script><?php $this->end('scripts'); ?>
