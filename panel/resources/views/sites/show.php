<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $site */
/** @var array<string, mixed> $summary */
/** @var string|null $serverIp */
/** @var int|null $dbBytes */
use WhaCP\Security\Permissions as P;
use WhaCP\View\SiteMenu;

$id = (int) $site['id'];
$u = fn (string $key, string $hash = ''): ?string => ($url = SiteMenu::url($this, $site, $key)) === null ? null : $url . $hash;
$mail = $summary['mail'];
$dns = $summary['dns'];
$mailUrl = fn (string $hash): ?string => $mail !== null && $this->can(P::MAIL_MANAGE) ? $this->url('mail.domain', ['id' => $mail['id']]) . $hash : $u('mail');
$dnsUrl = fn (string $hash): ?string => $dns !== null && $this->can(P::DNS_MANAGE) ? $this->url('dns.zone', ['id' => $dns['id']]) . $hash : $u('dns');
$disk = $inspect !== null ? format_bytes((int) $inspect['disk_bytes']) . (empty($inspect['disk_complete']) ? '+' : '') : null;
$sslLabel = match ((string) $site['ssl_mode']) {
    'letsencrypt' => "Let's Encrypt",
    'custom' => 'Own certificate',
    default => $site['https'] ? 'Installed' : 'Not installed',
};
$handler = match ((string) $site['kind']) {
    'php' => 'PHP-FPM',
    'proxy' => 'Reverse proxy',
    default => 'Static files',
};
$canManage = $this->can(P::WEBSITES_MANAGE);
$tint = abs(crc32((string) $site['domain'])) % 6;
$lastBackup = $summary['last_backup'];

// Site managing tiles: [label, icon, url|null, note|null]; tiles without
// a URL (no permission, or not for this kind of website) are left out.
$tiles = array_values(array_filter([
    ['FTP accounts', 'hdd-network', $u('ftp'), null],
    ['Files', 'folder2-open', $u('files'), $disk],
    ['Databases', 'database', $u('databases'), (string) $summary['databases']],
    ['Domains (DNS)', 'signpost-2', $dnsUrl(''), $dns !== null ? $dns['records'] . ' records' : null],
    ['SSL certificates', 'shield-lock', $u('ssl'), $site['https'] ? 'On' : 'Off'],
    ['Scheduler', 'calendar3', $u('cron'), $summary['cron'] > 0 ? (string) $summary['cron'] : null],
    ['Mail', 'envelope', $mailUrl(''), $mail !== null ? (string) $mail['mailboxes'] : null],
    ['Backup copies', 'archive', $u('backups'), $summary['backups'] > 0 ? (string) $summary['backups'] : null],
    ['Site access', 'person-lock', $u('ftp', '#sftp'), null],
    ['Terminal', 'terminal', $u('terminal'), null],
    ['Web firewall', 'shield-shaded', $u('firewall'), null],
    ['Settings', 'gear', $u('settings'), null],
    ['PHP settings', 'filetype-php', $site['kind'] === 'php' ? $u('settings', '#php') : null, $site['kind'] === 'php' ? (string) $site['php_version'] : null],
    ['Applications', 'grid-3x3-gap', $u('apps'), $summary['apps'] !== [] ? implode(', ', array_map('ucfirst', $summary['apps'])) : null],
    ['WordPress', 'wordpress', $site['kind'] === 'php' ? $u('wordpress') : null, null],
    ['Git deployment', 'git', $u('git'), $summary['git'] !== null ? (string) $summary['git']['branch'] : null],
    ['Workers', 'gear-wide-connected', $u('workers'), $summary['workers'] > 0 ? (string) $summary['workers'] : null],
    ['Redis', 'lightning', $u('redis'), null],
    ['Logs', 'journal-text', $u('logs'), null],
    ['Statistics', 'bar-chart-line', $u('stats'), null],
    ['Subdomains', 'diagram-3', $u('subdomains'), $summary['subdomains'] > 0 ? (string) $summary['subdomains'] : null],
    ['Addon domains', 'plus-square', $u('addons'), $summary['addons'] > 0 ? (string) $summary['addons'] : null],
    ['Manual settings', 'code-slash', $u('advanced'), null],
], static fn (array $t): bool => $t[2] !== null));

// Searchable actions: the tiles plus common phrasings.
$search = [];
foreach ($tiles as [$label, $icon, $url]) {
    $search[] = [$label, $url, $icon, ''];
}
foreach ([
    ['Create database', $this->can(P::DATABASES_MANAGE) ? $u('databases', '#new-database') : null, 'database', 'mysql mariadb new add'],
    ['Create database user', $this->can(P::DATABASES_MANAGE) ? $u('databases', '#new-db-user') : null, 'database', 'mysql login password remote'],
    ['Create mailbox / add email', $mail !== null ? $mailUrl('#new-mailbox') : $u('mail'), 'envelope', 'mail address inbox new'],
    ['Add forwarder / alias', $mail !== null ? $mailUrl('#new-alias') : $u('mail'), 'envelope', 'forward redirect mail'],
    ['Install SSL certificate', $this->can(P::SSL_MANAGE) ? $u('ssl', '#issue') : null, 'shield-lock', "https let's encrypt letsencrypt certificate tls"],
    ['Upload SSL certificate', $this->can(P::SSL_MANAGE) ? $u('ssl', '#upload-certificate') : null, 'shield-lock', 'custom pem own'],
    ['Add DNS record', $dns !== null ? $dnsUrl('#add-record') : $u('dns'), 'signpost-2', 'a aaaa cname mx txt zone'],
    ['Create FTP account', $u('ftp', '#new-ftp'), 'hdd-network', 'ftp filezilla upload login'],
    ['Create subdomain', $u('subdomains', '#new-subdomain'), 'diagram-3', 'subdomain sub blog shop'],
    ['Add addon domain', $u('addons', '#new-addon'), 'plus-square', 'addon domain another website parked'],
    ['Change PHP version', $site['kind'] === 'php' ? $u('settings', '#php') : null, 'filetype-php', 'php version upgrade memory limit upload'],
    ['Domains and aliases', $u('settings', '#domains'), 'globe2', 'alias www domain'],
    ['Redirects', $u('settings', '#requests'), 'signpost', 'redirect 301'],
    ['Create cron job', $this->can(P::CRON_MANAGE) ? $u('cron', '#new-cron') : null, 'calendar3', 'schedule task crontab scheduler'],
    ['Back up now', $this->can(P::BACKUPS_MANAGE) ? $u('backups', '#backup-now') : null, 'archive', 'backup copy'],
    ['Restore backup', $u('backups', '#backup-list'), 'archive', 'restore recover undo'],
    ['Install WordPress', $u('apps', '#app-wordpress'), 'grid-3x3-gap', 'wordpress wp blog'],
    ['Install Laravel', $u('apps', '#app-laravel'), 'grid-3x3-gap', 'laravel framework'],
    ['View error log', $u('logs'), 'journal-text', 'error access log debug'],
    ['Visitor statistics (AWStats)', $u('stats'), 'bar-chart-line', 'awstats analytics visitors traffic bandwidth stats'],
    ['Disable or delete website', $u('advanced', '#danger-zone'), 'exclamation-octagon', 'disable delete remove danger'],
] as [$label, $url, $icon, $words]) {
    if ($url !== null) {
        $search[] = [$label, $url, $icon, $words];
    }
}
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'overview']) ?>

<?php if ($site['status'] === 'error' && $site['last_error'] !== ''): ?>
    <div class="alert alert-danger small"><strong>The last change failed:</strong> <?= e($site['last_error']) ?><?php if ($u('advanced') !== null): ?> · <a href="<?= e($u('advanced', '#danger-zone')) ?>">Re-apply configuration</a><?php endif; ?></div>
<?php endif; ?>
<?php if (!empty($site['bandwidth_blocked'])): ?>
    <div class="alert alert-danger small" id="bandwidth-blocked"><i class="bi bi-speedometer me-1"></i><strong>Monthly bandwidth used up.</strong> This website answers “509 Bandwidth limit exceeded” until the month ends or the hosting plan allows more<?= $this->serverScope() ? ' (or you <a href="' . e($this->url('accounts.bandwidth', [], ['show' => 'all'])) . '">reset the counter</a>)' : '' ?>.</div>
<?php endif; ?>
<?php if ($inspectError !== null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $inspectError]) ?>
<?php elseif ($inspect !== null && !$inspect['config_exists']): ?>
    <div class="alert alert-warning small"><i class="bi bi-exclamation-triangle me-1"></i>This website's configuration is missing on the server.<?php if ($u('advanced') !== null): ?> <a href="<?= e($u('advanced', '#danger-zone')) ?>">Re-apply the configuration</a> to recreate it.<?php endif; ?></div>
<?php endif; ?>

<?php if (!empty($import)): ?>
    <?php if ($import['status'] === 'running'): ?>
        <div class="alert alert-info small"><span class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>Importing from <code><?= e($import['source']) ?></code>. The old website keeps serving visitors until the switch. Reload this page to see the result.</div>
    <?php elseif ($import['status'] === 'succeeded'): ?>
        <div class="alert alert-secondary small d-flex flex-wrap align-items-center gap-2">
            <span class="me-auto"><i class="bi bi-box-arrow-in-down me-1"></i>Imported from <code><?= e($import['source']) ?></code> on <?= e(substr((string) $import['created_at'], 0, 10)) ?>. The original files and configuration are kept.</span>
            <?php if ($this->can(P::IMPORT_RUN)): ?>
                <form method="post" action="<?= e($this->url('sites.import.undo', ['id' => $id])) ?>" data-wcp-confirm="Undo the import? The original configuration will serve <?= e($site['domain']) ?> again, and the WhaCP copy (files in <?= e($site['root']) ?>, its account and settings) will be deleted. Changes made since the import are lost.">
                    <?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger">Undo import</button>
                </form>
            <?php endif; ?>
        </div>
    <?php endif; ?>
<?php endif; ?>

<section class="card wcp-site-hero mb-4" aria-label="Website details" data-wcp-site-summary>
    <div class="card-body">
        <div class="row g-4 align-items-center">
            <div class="col-md-5 col-lg-4 d-none d-md-block">
                <a class="wcp-site-thumb wcp-site-thumb-lg wcp-tint-<?= (int) $tint ?><?= $site['enabled'] ? '' : ' is-off' ?>" href="<?= e(($site['https'] ? 'https://' : 'http://') . $site['domain']) ?>/" target="_blank" rel="noopener noreferrer" aria-label="Visit <?= e($site['domain']) ?>">
                    <span class="wcp-site-initial" aria-hidden="true"><?= e(strtoupper(substr((string) $site['domain'], 0, 1))) ?></span>
                    <span class="wcp-site-badges">
                        <span class="wcp-site-badge <?= $site['https'] ? 'is-ok' : 'is-muted' ?>"><i class="bi bi-<?= $site['https'] ? 'lock-fill' : 'unlock' ?>" aria-hidden="true"></i> <?= $site['https'] ? 'HTTPS' : 'HTTP' ?></span>
                    </span>
                    <?php if (!$site['enabled']): ?><span class="wcp-site-ribbon">Disabled</span><?php endif; ?>
                    <span class="wcp-site-visit"><i class="bi bi-box-arrow-up-right me-1" aria-hidden="true"></i><?= e($site['domain']) ?></span>
                </a>
            </div>
            <div class="col-md-7 col-lg-8">
                <dl class="wcp-site-facts mb-0">
                    <div><dt><i class="bi bi-person" aria-hidden="true"></i>Site user</dt><dd><code><?= e($site['system_user']) ?></code></dd></div>
                    <div><dt><i class="bi bi-folder2" aria-hidden="true"></i>Root directory</dt><dd class="text-break"><code><?= e($site['document_root']) ?></code></dd></div>
                    <div><dt><i class="bi bi-geo-alt" aria-hidden="true"></i>IPv4</dt><dd><?= $serverIp !== null ? e($serverIp) : '<span class="text-body-secondary">—</span>' ?></dd></div>
                    <div><dt><i class="bi bi-code-slash" aria-hidden="true"></i>Handler</dt><dd><?= e($handler) ?><?php if ($site['kind'] === 'php'): ?> · PHP <?= e($site['php_version']) ?><?php endif; ?></dd></div>
                    <div><dt><i class="bi bi-hdd" aria-hidden="true"></i>Site size</dt><dd><?= $disk !== null ? e($disk) : '<span class="text-body-secondary">—</span>' ?></dd></div>
                    <div><dt><i class="bi bi-database" aria-hidden="true"></i>Database size</dt><dd><?= $dbBytes !== null ? e(format_bytes($dbBytes)) : '<span class="text-body-secondary">—</span>' ?> <span class="small text-body-secondary fw-normal">(<?= (int) $summary['databases'] ?> database<?= $summary['databases'] === 1 ? '' : 's' ?>)</span></dd></div>
                    <div><dt><i class="bi bi-shield-lock" aria-hidden="true"></i>SSL</dt><dd><?= e($sslLabel) ?></dd></div>
                    <div><dt><i class="bi bi-archive" aria-hidden="true"></i>Last backup</dt><dd><?= $lastBackup !== null ? e(time_ago((string) $lastBackup['created_at'])) : '<span class="text-body-secondary">None yet</span>' ?></dd></div>
                </dl>
            </div>
        </div>
    </div>
</section>

<section aria-labelledby="manage-title">
    <div class="d-flex flex-wrap align-items-center gap-2 mb-3">
        <h2 class="h5 mb-0 me-auto" id="manage-title">Site managing</h2>
        <?php if ($canManage): ?>
            <a class="btn btn-sm btn-outline-danger" href="<?= e($this->url('sites.advanced', ['id' => $id])) ?>#delete-website"><i class="bi bi-trash me-1"></i>Delete the website</a>
            <form method="post" action="<?= e($this->url('sites.toggle', ['id' => $id])) ?>" data-wcp-busy<?= $site['enabled'] ? ' data-wcp-confirm="Disable ' . e($site['domain']) . '? Visitors will no longer reach it. Files, databases and settings are kept."' : '' ?>>
                <?= $this->csrfField() ?><input type="hidden" name="return_to" value="overview">
                <button class="btn btn-sm btn-outline-secondary" type="submit"><i class="bi bi-power me-1"></i><?= $site['enabled'] ? 'Disable the website' : 'Enable the website' ?></button>
            </form>
        <?php endif; ?>
    </div>

    <div class="wcp-feature-search mb-3" data-wcp-feature-search>
        <label class="visually-hidden" for="wcp-fs">Search features</label>
        <div class="input-group">
            <span class="input-group-text"><i class="bi bi-search" aria-hidden="true"></i></span>
            <input class="form-control" id="wcp-fs" type="search" placeholder="Search… e.g. create database, add email, DNS records" autocomplete="off"
                   role="combobox" aria-expanded="false" aria-controls="wcp-fs-results" aria-autocomplete="list" data-wcp-feature-input>
        </div>
        <div class="list-group wcp-feature-results d-none" id="wcp-fs-results" role="listbox" data-wcp-feature-results>
            <?php foreach ($search as $i => [$label, $url, $icon, $words]): ?>
                <a class="list-group-item list-group-item-action d-none" id="wcp-fs-<?= (int) $i ?>" role="option" href="<?= e($url) ?>" data-wcp-feature="<?= e(strtolower($label . ' ' . $words)) ?>" data-wcp-feature-label="<?= e(strtolower($label)) ?>">
                    <i class="bi bi-<?= e($icon) ?> me-2 text-body-secondary" aria-hidden="true"></i><span class="fw-medium"><?= e($label) ?></span>
                </a>
            <?php endforeach; ?>
            <div class="list-group-item small text-body-secondary d-none" data-wcp-feature-none>Nothing found. Try “database”, “email” or “backup”.</div>
        </div>
    </div>

    <div class="wcp-tiles" data-wcp-tiles>
        <?php foreach ($tiles as [$label, $icon, $url, $note]): ?>
            <a class="wcp-tile" href="<?= e($url) ?>" data-wcp-tile="<?= e(strtolower($label)) ?>">
                <span class="wcp-tile-icon" aria-hidden="true"><i class="bi bi-<?= e($icon) ?>"></i></span>
                <span class="wcp-tile-label"><?= e($label) ?></span>
                <?php if ($note !== null && $note !== ''): ?><span class="wcp-tile-note"><?= e($note) ?></span><?php endif; ?>
            </a>
        <?php endforeach; ?>
    </div>
</section>
