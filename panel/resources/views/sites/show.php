<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $site */
/** @var array<string, mixed> $summary */
use WhaCP\Security\Permissions as P;
use WhaCP\View\SiteMenu;

$id = (int) $site['id'];
$u = fn (string $key, string $hash = ''): ?string => ($url = SiteMenu::url($this, $site, $key)) === null ? null : $url . $hash;
$mail = $summary['mail'];
$dns = $summary['dns'];
$mailUrl = fn (string $hash): ?string => $mail !== null && $this->can(P::MAIL_MANAGE) ? $this->url('mail.domain', ['id' => $mail['id']]) . $hash : $u('mail');
$dnsUrl = fn (string $hash): ?string => $dns !== null && $this->can(P::DNS_MANAGE) ? $this->url('dns.zone', ['id' => $dns['id']]) . $hash : $u('dns');
$plural = static fn (int $n, string $one, string $many): string => $n . ' ' . ($n === 1 ? $one : $many);
$disk = $inspect !== null ? format_bytes((int) $inspect['disk_bytes']) . (empty($inspect['disk_complete']) ? '+' : '') : null;
$sslLabel = match ((string) $site['ssl_mode']) {
    'letsencrypt' => "Let's Encrypt",
    'custom' => 'Own certificate',
    default => $site['https'] ? 'Installed' : 'Not installed',
};
$lastBackup = $summary['last_backup'];

// Feature groups: [title, icon, description, status, links]; a link is
// [label, url|null, description]. Links without a URL (no permission or
// feature not installed) are left out, and so are empty groups.
$groups = [
    ['Files', 'folder2-open', 'Upload, edit and manage your website files', $disk !== null ? $disk . ' used' : null, [
        ['File manager', $u('files'), 'Browse, upload and edit files'],
        ['FTP / SFTP accounts', $u('ftp'), 'Connect with FileZilla or another client'],
        ['Logs', $u('logs'), 'Visitor and error logs'],
    ]],
    ['Databases', 'database', 'Create and manage MySQL / MariaDB databases', $plural($summary['databases'], 'database', 'databases'), [
        ['Databases', $u('databases'), 'Create, back up and delete databases'],
        ['Database users', $u('databases', '#db-users'), 'Logins and passwords for your applications'],
        ['Database browser', $u('databases'), 'Browse tables and run SQL queries'],
    ]],
    ['Email', 'envelope', 'Mailboxes, aliases and email settings', $mail !== null ? $plural($mail['mailboxes'], 'mailbox', 'mailboxes') : 'Not set up', [
        ['Mailboxes', $mailUrl('#mailboxes'), 'Addresses with their own inbox'],
        ['Aliases and forwarders', $mailUrl('#aliases'), 'Forward mail to other addresses'],
        ['Email authentication', $mail !== null ? $mailUrl('#mail-dns') : null, 'MX, SPF, DKIM and DMARC records'],
        ['Mail app settings and webmail', $mail !== null ? $mailUrl('#mail-clients') : null, 'Set up Outlook, phones and webmail'],
        ['Spam filter', $this->can(P::SERVER_MANAGE) && $this->hasRoute('mail') ? $this->url('mail') . '#spam-filter' : null, 'Server-wide spam filtering'],
    ]],
    ['Domains', 'globe2', 'Domain names, DNS and HTTPS', $site['aliases'] === [] ? 'No aliases' : $plural(count($site['aliases']), 'alias', 'aliases'), [
        ['Domains and aliases', $u('settings', '#domains'), 'Other names for this website, e.g. www'],
        ['DNS records', $dnsUrl(''), $dns !== null ? $plural($dns['records'], 'record', 'records') : 'Point your domain to this server'],
        ['SSL / TLS', $u('ssl'), $sslLabel],
        ['Redirects', $u('settings', '#requests'), 'Send old addresses to new pages'],
    ]],
    ['Applications', 'grid-3x3-gap', 'Install WordPress, Laravel and deploy from Git', $summary['apps'] !== [] ? implode(', ', array_map('ucfirst', $summary['apps'])) : null, [
        ['WordPress', $u('apps', '#app-wordpress'), 'Install in a few clicks'],
        ['Laravel', $u('apps', '#app-laravel'), 'Framework with database set up'],
        ['Application installer', $u('apps'), 'All applications'],
        ['Git deployment', $u('git'), $summary['git'] !== null ? (string) $summary['git']['branch'] . ' branch' : 'Deploy from GitHub, GitLab, …'],
    ]],
    ['Performance', 'speedometer', 'PHP version, limits and caching', $site['kind'] === 'php' ? 'PHP ' . $site['php_version'] : null, [
        ['PHP version and limits', $site['kind'] === 'php' ? $u('settings', '#php') : null, 'Version, memory, upload size, PHP-FPM'],
        ['Redis', $u('redis'), 'Fast cache for your application'],
        ['Memcached', $this->can(P::REDIS_MANAGE) && $this->hasRoute('memcached') ? $this->url('memcached') : null, 'Server-wide object cache'],
    ]],
    ['Backups', 'archive', 'Back up and restore your website', $lastBackup !== null ? 'Last: ' . time_ago((string) $lastBackup['created_at']) : 'No backup yet', [
        ['Back up now', $this->can(P::BACKUPS_MANAGE) ? $u('backups', '#backup-now') : null, 'Files and databases'],
        ['Restore', $u('backups', '#backup-list'), $plural($summary['backups'], 'backup', 'backups')],
        ['Backup schedule', $this->can(P::BACKUPS_MANAGE) ? $u('backups', '#schedule') : null, $summary['backup_schedules'] > 0 ? $plural($summary['backup_schedules'], 'schedule', 'schedules') : 'Not scheduled'],
        ['Backup destinations', $this->can(P::BACKUPS_MANAGE) && $this->can(P::SERVER_MANAGE) ? $this->url('backups') . '#storages' : null, 'Remote storage (SFTP, FTP, Google Drive)'],
    ]],
    ['Automation', 'clock-history', 'Scheduled tasks and background processes', null, [
        ['Cron jobs', $u('cron'), $plural($summary['cron'], 'job', 'jobs')],
        ['Background workers', $u('workers'), $plural($summary['workers'], 'worker', 'workers')],
    ]],
    ['Security', 'shield-check', 'HTTPS, access and sign-in security', $site['https'] ? 'HTTPS on' : 'HTTPS off', [
        ['HTTPS and HSTS', $u('ssl', $site['https'] ? '#https-options' : ''), $site['force_https'] ? 'HTTP redirects to HTTPS' : 'Encrypt visitors\' connections'],
        ['File access (FTP / SFTP)', $u('ftp'), 'Who can reach your files'],
        ['Your sign-in security', $this->url('profile'), 'Password, two-factor, security keys'],
        ['Server security', $this->can(P::SECURITY_VIEW) && $this->hasRoute('security') ? $this->url('security') : null, 'Firewall, SSH, Fail2ban'],
        ['Audit log', $this->can(P::AUDIT_VIEW) && $this->hasRoute('audit') ? $this->url('audit') : null, 'Who changed what'],
    ]],
];

// Quick actions: the most common tasks.
$quick = array_values(array_filter([
    ['Upload files', 'cloud-arrow-up', $u('files')],
    ['Create database', 'database-add', $this->can(P::DATABASES_MANAGE) ? $u('databases', '#new-database') : null],
    ['Create mailbox', 'envelope-plus', $mail !== null ? $mailUrl('#new-mailbox') : $u('mail')],
    [$site['https'] ? 'Manage SSL' : 'Install SSL', 'shield-lock', $this->can(P::SSL_MANAGE) ? $u('ssl', $site['https'] ? '' : '#issue') : null],
    ['Add DNS record', 'signpost', $dns !== null ? $dnsUrl('#add-record') : $u('dns')],
    ['Create backup', 'archive', $this->can(P::BACKUPS_MANAGE) ? $u('backups', '#backup-now') : null],
], static fn (array $q): bool => $q[2] !== null));

// Searchable actions: every link above plus common phrasings.
$search = [];
foreach ($groups as [$gTitle, $gIcon, , , $links]) {
    foreach ($links as [$label, $url, $desc]) {
        if ($url !== null) {
            $search[] = [$label, $url, $gTitle, $gIcon, $desc];
        }
    }
}
foreach ([
    ['Create database', $this->can(P::DATABASES_MANAGE) ? $u('databases', '#new-database') : null, 'Databases', 'database', 'mysql mariadb new add'],
    ['Create database user', $this->can(P::DATABASES_MANAGE) ? $u('databases', '#new-db-user') : null, 'Databases', 'database', 'mysql login password'],
    ['Add email / create mailbox', $mail !== null ? $mailUrl('#new-mailbox') : $u('mail'), 'Email', 'envelope', 'mail address inbox new'],
    ['Add forwarder / alias', $mail !== null ? $mailUrl('#new-alias') : $u('mail'), 'Email', 'envelope', 'forward redirect mail'],
    ['Install SSL certificate', $this->can(P::SSL_MANAGE) ? $u('ssl', '#issue') : null, 'Domains', 'shield-lock', "https let's encrypt letsencrypt certificate tls"],
    ['Upload SSL certificate', $this->can(P::SSL_MANAGE) ? $u('ssl', '#upload-certificate') : null, 'Domains', 'shield-lock', 'custom pem own'],
    ['Add DNS record', $dns !== null ? $dnsUrl('#add-record') : $u('dns'), 'Domains', 'signpost', 'a aaaa cname mx txt zone'],
    ['Create FTP account', $u('ftp', '#new-ftp'), 'Files', 'hdd-network', 'ftp filezilla upload login'],
    ['Change PHP version', $site['kind'] === 'php' ? $u('settings', '#php') : null, 'Performance', 'filetype-php', 'php version upgrade memory limit upload'],
    ['Create cron job', $this->can(P::CRON_MANAGE) ? $u('cron', '#new-cron') : null, 'Automation', 'clock-history', 'schedule task crontab'],
    ['Restore backup', $u('backups', '#backup-list'), 'Backups', 'archive', 'restore recover undo'],
    ['View error log', $u('logs'), 'Files', 'journal-text', 'error access log debug'],
    ['Website settings', $u('settings'), 'Advanced', 'sliders', 'document root aliases redirects upload size'],
    ['Disable or delete website', $u('advanced', '#danger-zone'), 'Advanced', 'exclamation-octagon', 'disable delete remove danger'],
] as [$label, $url, $gTitle, $gIcon, $desc]) {
    if ($url !== null) {
        $search[] = [$label, $url, $gTitle, $gIcon, $desc];
    }
}
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'overview']) ?>

<?php if ($site['status'] === 'error' && $site['last_error'] !== ''): ?>
    <div class="alert alert-danger small"><strong>The last change failed:</strong> <?= e($site['last_error']) ?><?php if ($u('advanced') !== null): ?> · <a href="<?= e($u('advanced', '#danger-zone')) ?>">Re-apply configuration</a><?php endif; ?></div>
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

<section class="wcp-site-summary mb-4" aria-label="Website summary" data-wcp-site-summary>
    <div class="wcp-sum">
        <div class="wcp-sum-label">Status</div>
        <div class="wcp-sum-value"><?php if ($site['status'] === 'error'): ?><span class="wcp-state is-error">Needs attention</span><?php elseif ($site['enabled']): ?><span class="wcp-state is-ok">Active</span><?php else: ?><span class="wcp-state is-off">Disabled</span><?php endif; ?></div>
    </div>
    <div class="wcp-sum">
        <div class="wcp-sum-label"><?= $site['kind'] === 'php' ? 'PHP' : 'Type' ?></div>
        <div class="wcp-sum-value"><?= $site['kind'] === 'php' ? e($site['php_version']) : e(\WhaCP\Services\WebsiteService::KINDS[$site['kind']] ?? $site['kind']) ?></div>
    </div>
    <div class="wcp-sum">
        <div class="wcp-sum-label">HTTPS</div>
        <div class="wcp-sum-value"><?= $site['https'] ? '<span class="wcp-state is-ok">Enabled</span>' : '<span class="wcp-state is-warn">Not enabled</span>' ?></div>
    </div>
    <div class="wcp-sum">
        <div class="wcp-sum-label">SSL</div>
        <div class="wcp-sum-value"><?= e($sslLabel) ?></div>
    </div>
    <div class="wcp-sum">
        <div class="wcp-sum-label">Disk</div>
        <div class="wcp-sum-value"><?= $disk !== null ? e($disk) : '<span class="text-body-secondary">—</span>' ?></div>
    </div>
    <div class="wcp-sum">
        <div class="wcp-sum-label">Last backup</div>
        <div class="wcp-sum-value"><?= $lastBackup !== null ? e(time_ago((string) $lastBackup['created_at'])) : '<span class="text-body-secondary">None yet</span>' ?></div>
    </div>
</section>

<div class="wcp-feature-search mb-4" data-wcp-feature-search>
    <label class="visually-hidden" for="wcp-fs">Search features</label>
    <div class="input-group input-group-lg">
        <span class="input-group-text"><i class="bi bi-search" aria-hidden="true"></i></span>
        <input class="form-control" id="wcp-fs" type="search" placeholder="Search features… e.g. create database, add email, DNS records" autocomplete="off"
               role="combobox" aria-expanded="false" aria-controls="wcp-fs-results" aria-autocomplete="list" data-wcp-feature-input>
    </div>
    <div class="list-group wcp-feature-results d-none" id="wcp-fs-results" role="listbox" data-wcp-feature-results>
        <?php foreach ($search as $i => [$label, $url, $gTitle, $gIcon, $desc]): ?>
            <a class="list-group-item list-group-item-action d-none" id="wcp-fs-<?= (int) $i ?>" role="option" href="<?= e($url) ?>" data-wcp-feature="<?= e(strtolower($label . ' ' . $gTitle . ' ' . $desc)) ?>" data-wcp-feature-label="<?= e(strtolower($label)) ?>">
                <i class="bi bi-<?= e($gIcon) ?> me-2 text-body-secondary" aria-hidden="true"></i><span class="fw-medium"><?= e($label) ?></span>
                <span class="small text-body-secondary ms-2"><?= e($gTitle) ?></span>
            </a>
        <?php endforeach; ?>
        <div class="list-group-item small text-body-secondary d-none" data-wcp-feature-none>Nothing found. Try “database”, “email” or “backup”.</div>
    </div>
</div>

<?php if ($quick !== []): ?>
<section class="mb-4" aria-labelledby="qa-title">
    <h2 class="wcp-section-heading" id="qa-title">Quick actions</h2>
    <div class="wcp-quick" data-wcp-quick-actions>
        <?php foreach ($quick as [$label, $icon, $url]): ?>
            <a class="wcp-quick-action" href="<?= e($url) ?>"><i class="bi bi-<?= e($icon) ?>" aria-hidden="true"></i><span><?= e($label) ?></span></a>
        <?php endforeach; ?>
    </div>
</section>
<?php endif; ?>

<section aria-labelledby="features-title">
    <h2 class="wcp-section-heading" id="features-title">Manage your website</h2>
    <div class="row row-cols-1 row-cols-md-2 row-cols-xxl-3 g-3" data-wcp-feature-groups>
        <?php foreach ($groups as [$title, $icon, $desc, $status, $links]): ?>
            <?php $links = array_values(array_filter($links, static fn (array $l): bool => $l[1] !== null)); ?>
            <?php if ($links === []) { continue; } ?>
            <div class="col">
                <div class="card h-100 wcp-feature" data-wcp-feature-group="<?= e(strtolower($title)) ?>">
                    <div class="card-body pb-2">
                        <div class="d-flex align-items-start gap-3">
                            <span class="wcp-feature-icon" aria-hidden="true"><i class="bi bi-<?= e($icon) ?>"></i></span>
                            <div class="wcp-minw-0 flex-grow-1">
                                <h3 class="h6 mb-0"><a class="text-reset text-decoration-none" href="<?= e($links[0][1]) ?>"><?= e($title) ?></a></h3>
                                <div class="small text-body-secondary"><?= e($desc) ?></div>
                            </div>
                            <?php if ($status !== null): ?><span class="badge rounded-pill text-bg-light border fw-normal text-nowrap"><?= e($status) ?></span><?php endif; ?>
                        </div>
                    </div>
                    <div class="list-group list-group-flush wcp-feature-links">
                        <?php foreach ($links as [$label, $url, $ldesc]): ?>
                            <a class="list-group-item list-group-item-action" href="<?= e($url) ?>">
                                <span><?= e($label) ?><span class="d-block small text-body-secondary"><?= e($ldesc) ?></span></span>
                                <i class="bi bi-chevron-right" aria-hidden="true"></i>
                            </a>
                        <?php endforeach; ?>
                    </div>
                </div>
            </div>
        <?php endforeach; ?>
    </div>
</section>

<?php if ($u('advanced') !== null): ?>
    <p class="small text-body-secondary mt-4 mb-0"><i class="bi bi-tools me-1"></i>Server details (system user, folders, generated configuration) and actions like disabling or deleting the website are under <a href="<?= e($u('advanced')) ?>">Advanced</a>.</p>
<?php endif; ?>
