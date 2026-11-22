<?php
/** @var \NimVorSite\Core\View $v */
$v->layout('layouts/site');
$v->set('title', 'Features');
$v->set('description', 'Everything NimVor manages: websites and their protection, PHP versions, WordPress and apps, email, DNS and DNSSEC, SSL, MySQL, PostgreSQL and MongoDB, files, backups, moving from another panel, security, monitoring, hosting plans, customer themes, automation and an API.');
$groups = [
    ['websites', 'globe', 'Websites and domains', 'Host PHP, static and reverse-proxy sites without hand-editing Nginx.', [
        'Nginx virtual hosts with aliases, redirects and subdomains, and a validated reload (`nginx -t`) with automatic rollback',
        'A separate PHP-FPM pool per site, several PHP versions side by side and per-site settings',
        'One page per website: files, databases, email, DNS, SSL, PHP, cron, logs and backups',
        'Take over an existing site: files are copied, Nginx switches in one reload, and the import can be undone',
        'Discovery finds websites that already run on the server so you can adopt them',
        'Git deployment with webhooks, and an IP address overview showing which sites use which address',
        'A page cache for PHP websites and Brotli compression, switched on per website, with a purge button',
        'Environment variables per website, and quick PHP-FPM and Nginx settings from the websites list',
    ]],
    ['protection', 'shield', 'Website protection', 'Decide who may reach a site, how fast, and what its accounts may run.', [
        'Traffic limits per website: requests per second or minute from one address to a folder, a URL or the whole site, answered with 429, 503 or a closed connection',
        'IP, browser and URL lists, per website and server-wide: allow and deny by address, network or range, by User-Agent and by path, with import and export',
        'Country restrictions: refuse visitors from chosen countries, or serve only visitors from them',
        'A traffic report per website: requests per hour, refused requests, the busiest addresses, URLs and browsers, a country ranking and ModSecurity events, with a Deny button',
        'Anti-Intrusion: a log of every program a service or website account starts, and a blacklist of programs it may not run',
        'Every change to Nginx is checked with `nginx -t` and rolled back if it is refused; certificate renewal is never blocked',
    ]],
    ['php', 'code', 'PHP Manager', 'Choose exactly which PHP versions and extensions a server has.', [
        'Install PHP 8.0 to the newest release side by side, then review and apply the whole change at once',
        'Extensions per version, including the ionCube Loader (checked by PHP before it is kept)',
        'Update a PHP version from its own page when security fixes are pending, with a check that PHP-FPM still starts',
        'Edit php.ini settings for a version or for one website, with limits checked first',
    ]],
    ['apps', 'bolt', 'WordPress and applications', 'Install and look after popular applications.', [
        'WordPress Toolkit: install, updates, security checks and staging copies',
        'An application installer for popular PHP applications, plus Laravel and native apps such as Gitea, Django and FastAPI',
        'Every install can be reviewed first (a dry run lists the checks, database, download and files), downloads are verified and files belong to the site user',
        'An Application Manager to choose which applications and categories customers can install',
    ]],
    ['email', 'mail', 'Email', 'A complete mail stack, set up for you and kept consistent with DNS.', [
        'Postfix and Dovecot with sign-in on submission and anti-spoofing checks',
        'Rspamd spam filtering (Junk folder or refusal), mailboxes with quotas, aliases and catch-all',
        'DKIM keys, with the MX, SPF, DKIM and DMARC records added to the DNS zone in one click',
        'Greylisting with a report, sending limits per account, and a mail queue you can inspect',
        'Delivery reports and statistics, mail tools for testing, and Roundcube webmail',
        'Webmail and mail host names on the domain itself (webmail.example.com), answering over HTTPS with the domain\'s certificate',
        'An administrator\'s switch for sending on port 465 as well as 587, for networks that block submission',
    ]],
    ['dns-ssl', 'lock', 'DNS and SSL', 'Zones and certificates that are checked before they go live.', [
        'Authoritative zones: A, AAAA, CNAME, MX, TXT, NS, SRV and CAA, with templates and secondaries',
        'Choose the name server software: BIND, NSD, Knot DNS or PowerDNS, all serving the same zone files, with the trade-offs shown and the previous server put back if the switch fails',
        'DNSSEC: sign zones served by BIND, Knot DNS or PowerDNS and hand the DS record to your registrar',
        'Every zone is validated with `named-checkzone` and the serial is verified after each reload',
        "AutoSSL: Let's Encrypt for every website with renewals and a log, plus custom certificates, keys and CSRs",
        'HTTPS redirect, HSTS, TLS protocol and cipher settings, and certificates for the panel, mail server and host name',
        'A Cloudflare page for CDN and DNS through your own Cloudflare account',
    ]],
    ['data', 'db', 'Databases and data stores', 'The databases your sites use, with sensible limits.', [
        'MySQL and MariaDB databases, users and grants, with server settings, connections and logs',
        'PostgreSQL: databases, roles and grants kept apart per customer, a table browser, an SQL editor with history, backups with schedules and retention, and remote PostgreSQL servers',
        'Web database browsers for MySQL, PostgreSQL and MongoDB: an overview with sizes, rows, structure, indexes, constraints and bulk actions, each confined to one customer\'s database',
        'MongoDB databases and users',
        'Redis with per-site users and a key browser, and Memcached with stats and settings',
    ]],
    ['files', 'file', 'Files and access', 'Safe ways to reach a site\'s files.', [
        'A full-screen file manager: folder tree, drag-and-drop uploads, an editor, permissions, zip and tar.gz',
        'Chrooted FTPS accounts with quotas and TLS required, and SFTP for the site user with no shell',
        'A web terminal in the browser for administrators',
        'A log viewer with live tail for the web server, PHP, mail, database, security, SSL and system logs',
    ]],
    ['backups', 'backup', 'Backups', 'Encrypted by default, restorable, and tested.', [
        'AES-256-GCM encrypted site, account and server backups, with a backup key you download',
        'S3-compatible storage, SFTP, FTPS, Google Drive or local disk',
        'Schedules and retention; server configuration is restored by component, with rollback',
        'Backups run as background tasks you can follow, and keep going if you close the window',
    ]],
    ['migration', 'cloud', 'Moving to NimVor', 'Bring websites, databases and mail from another server or panel.', [
        'Copy websites, databases and mailboxes from another server over SSH: files with rsync, databases streamed, mail kept intact, with checkpoints so a stopped copy resumes',
        'Sources: cPanel, DirectAdmin, Plesk, HestiaCP, VestaCP, CyberPanel, CloudPanel, aaPanel, ISPConfig, FASTPANEL, Froxlor, InterWorx, Enhance, CentOS Web Panel, another NimVor server and plain Linux servers (most are marked experimental)',
        'Or import a backup file: a cPanel cpmove archive, a DirectAdmin user backup or a HestiaCP / VestaCP backup, with no login to the old server',
        'Creates the destination databases, accounts, forwarders, cron jobs and DNS records for you, and reports what could not be moved',
        'A validation pass compares what arrived, a printable report is kept, and what was created can be rolled back',
    ]],
    ['security', 'shield', 'Security', 'Hardening that does not lock you out.', [
        'A security overview with a safety rating and one-click fixes',
        'UFW firewall, SSH hardening and Fail2ban brute-force protection; every change rolls back unless you confirm it',
        'ModSecurity web application firewall with an events view, and a malware scan of websites',
        'Two-factor sign-in with an authenticator app or security keys and passkeys, which can be required per role',
        'Sign-in and password policies, password strength rules, and blocking of panel and login attacks',
        'Shell access, compiler access, traceroute and fork-bomb limits for accounts',
        'SMTP restrictions so only the mail server sends mail, and limits on websites\' outgoing connections',
        'A TLS audit, vulnerability advisories and file integrity monitoring',
        'An audit log of who changed what, CSRF protection and a strict Content-Security-Policy in the panel',
    ]],
    ['dashboard', 'chart', 'Dashboard', 'One screen for the whole server, arranged the way you work.', [
        'Widgets you can show, hide, drag into a new order and widen or narrow, each with its own refresh interval, saved per user',
        'Website health from real HTTP checks (online, errors, slowest sites, uptime over 24 hours), with a switch to leave a site out of the checks',
        'Security overview and SSL certificates in one place: firewall, brute-force protection, ModSecurity, malware scans and certificates about to expire',
        'Database, cache and web-stack overviews for MySQL or MariaDB, PostgreSQL, Redis, Memcached, Nginx and PHP-FPM',
        'Backup health, pending updates, DNS and mail status, deployments and WordPress sites at a glance',
        'Capacity forecast: when the disk is expected to fill, only when there is enough history to say so',
        'History charts for CPU, memory, load, network, disk, services and databases drawn from what the monitor really recorded, with gaps shown as gaps',
        'A top-processes view, and every figure that could not be measured shown as N/A instead of a made-up zero',
    ]],
    ['monitoring', 'bell', 'Monitoring and health', 'Know what the server is doing, and when something is wrong.', [
        'An alert center: one open alert per problem, with severity, acknowledge and resolve, and the thresholds set in one place',
        'Alerts for websites that stop answering or get slow, stale backups, security updates, a stuck mail queue and a disk that is filling up',
        'Metrics history, live statistics and a process list',
        'A health page that runs every check in one place, and notifications for what happened and what failed',
        'Resource limits (CPU, memory and disk) and monthly bandwidth limits per account',
    ]],
    ['server', 'server', 'Server management', 'Look after the machine itself.', [
        'A setup wizard that shows what is done and what is left, by server profile',
        'Components: what each part (web, PHP, email, DNS, FTP, caches, security) needs, and what is installed',
        'Start, stop and restart services; install and update server software; host name, time, updates and reboot',
        'Nginx state, config test and tuning; the FTP server and its settings; Linux system accounts',
        'Node.js and Python runtimes for website workers, cron jobs and background workers, and FFmpeg for media',
        'Several servers managed from one panel, with remote agents over pinned TLS',
    ]],
    ['hosting', 'users', 'Hosting, plans and teams', 'Run it for yourself or for customers.', [
        'Plans and quotas, resellers and hosting customers, each seeing only their own sites',
        'Roles (administrator, operator, viewer, reseller, customer) and custom roles',
        'One form creates a customer, their login and their first website',
        'Basic WebHost settings and a Tweak Settings page where every setting shows its default, with search and reset',
        'Four themes for the customer panel (Classic, Workspace, Simple and Compact), each customer\'s choice of light or dark and an accent colour, with the themes on offer set by the administrator',
    ]],
    ['api', 'tool', 'Automation and extension', 'Script it or extend it.', [
        'A REST API with personal access tokens, described by an OpenAPI file',
        'Plugins for the panel',
        'A command-line tool for administrators (`sudo nimvor`): `nimvor update` brings the panel to the newest release with its database migrations, `doctor` checks the installation and `user:password` resets a password',
    ]],
];?>
<?= $v->partial('partials/page-head', ['title' => 'Features', 'intro' => 'What NimVor manages today, and how it keeps each change safe.', 'crumbs' => [['Features', '']]]) ?>

<?php if (!empty($latest)): ?>
<section class="section feature-new">
    <div class="container">
        <a class="rel-banner" href="/resources/releases/<?= e(rawurlencode($latest['version'])) ?>">
            <span class="badge badge-ok">New in <?= e($latest['version']) ?></span>
            <span class="rel-banner-text"><?= $latest['summary'] !== '' ? strip_tags($inline($latest['summary'])) : e(count($latest['sections']) > 0 ? $latest['count'] . ' changes in this release.' : 'The latest release.') ?></span>
            <span class="link-arrow">Release notes <?= $v->icon('arrow', 16) ?></span>
        </a>
    </div>
</section>
<?php endif; ?>
<section class="section">
    <div class="container">
        <div class="grid grid-2">
            <?php foreach ($groups as [$id, $icon, $title, $blurb, $items]): ?>
                <article class="card" id="<?= e($id) ?>">
                    <div class="card-icon"><?= $v->icon($icon) ?></div>
                    <h2 class="h-card"><?= e($title) ?></h2>
                    <p class="muted"><?= e($blurb) ?></p>
                    <ul class="checks">
                        <?php foreach ($items as $item): ?>
                            <li><?= $v->icon('check', 18) ?><span><?= preg_replace('/`([^`]+)`/', '<code>$1</code>', e($item)) ?></span></li>
                        <?php endforeach; ?>
                    </ul>
                </article>
            <?php endforeach; ?>
        </div>
    </div>
</section>

<section class="section section-soft">
    <div class="container split">
        <div>
            <span class="eyebrow">Under the hood</span>
            <h2>Two parts, with a hard line between them</h2>
            <p class="lead">The panel is a PHP web application. It never runs shell commands. The agent is a small Go service on the server that performs a fixed list of named actions.</p>
        </div>
        <div class="card">
            <ul class="checks mt-0">
                <li><?= $v->icon('check', 18) ?><span>The panel talks to the agent over a Unix socket (token and peer check) or, for remote servers, pinned TLS 1.3.</span></li>
                <li><?= $v->icon('check', 18) ?><span>The agent starts only allowlisted programs with fixed argument shapes, never through a shell.</span></li>
                <li><?= $v->icon('check', 18) ?><span>A write gate keeps every changing action off until you enable write mode.</span></li>
                <li><?= $v->icon('check', 18) ?><span>Long jobs run in the background, one at a time per target.</span></li>
            </ul>
        </div>
    </div>
</section>

<section class="section">
    <div class="container">
        <div class="cta-band">
            <h2>See it on your own server</h2>
            <p>Install in read-only mode first and look at what NimVor finds.</p>
            <a class="btn btn-primary btn-lg" href="/get-started">Install NimVor</a>
        </div>
    </div>
</section>
