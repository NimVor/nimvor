<?php
/** @var \NimVorSite\Core\View $v */
$v->layout('layouts/site');
$v->set('title', 'Features');
$v->set('description', 'Everything NimVor manages: websites, PHP versions, WordPress and apps, email, DNS, SSL, databases, files, backups, security, monitoring, hosting plans, automation and an API.');
$groups = [
    ['websites', 'globe', 'Websites and domains', 'Host PHP, static and reverse-proxy sites without hand-editing Nginx.', [
        'Nginx virtual hosts with aliases, redirects and subdomains, and a validated reload (`nginx -t`) with automatic rollback',
        'A separate PHP-FPM pool per site, several PHP versions side by side and per-site settings',
        'One page per website: files, databases, email, DNS, SSL, PHP, cron, logs and backups',
        'Take over an existing site: files are copied, Nginx switches in one reload, and the import can be undone',
        'Discovery finds websites that already run on the server so you can adopt them',
        'Git deployment with webhooks, and an IP address overview showing which sites use which address',
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
    ]],
    ['dns-ssl', 'lock', 'DNS and SSL', 'Zones and certificates that are checked before they go live.', [
        'Authoritative zones on BIND: A, AAAA, CNAME, MX, TXT, NS, SRV and CAA, with templates and secondaries',
        'Every zone is validated with `named-checkzone` and the serial is verified after each reload',
        "AutoSSL: Let's Encrypt for every website with renewals and a log, plus custom certificates, keys and CSRs",
        'HTTPS redirect, HSTS, TLS protocol and cipher settings, and certificates for the panel, mail server and host name',
        'A Cloudflare page for CDN and DNS through your own Cloudflare account',
    ]],
    ['data', 'db', 'Databases and data stores', 'The databases your sites use, with sensible limits.', [
        'MySQL and MariaDB databases, users and grants, with server settings, connections and logs',
        'A web database browser (rows, structure, SQL console) confined to a single database',
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
    ['monitoring', 'chart', 'Monitoring and health', 'Know what the server is doing, and when something is wrong.', [
        'A dashboard with CPU, memory, disk, load, services and websites',
        'Metrics history with alerts, live statistics and a process list',
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
    ]],
    ['api', 'tool', 'Automation and extension', 'Script it or extend it.', [
        'A REST API with personal access tokens, described by an OpenAPI file',
        'Plugins for the panel',
        'A command-line tool for administrators (`sudo nimvor`) with a `doctor` check for the installation',
    ]],
];?>
<?= $v->partial('partials/page-head', ['title' => 'Features', 'intro' => 'What NimVor manages today, and how it keeps each change safe.', 'crumbs' => [['Features', '']]]) ?>

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
