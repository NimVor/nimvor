<?php
/** @var \NimVorSite\Core\View $v */
$v->layout('layouts/site');
$v->set('title', 'Features');
$v->set('description', 'Everything NimVor manages: websites, PHP, email, DNS, SSL, databases, files, backups, security, hosting plans, automation and an API.');
$groups = [
    ['websites', 'globe', 'Websites and applications', 'Host PHP, static and reverse-proxy sites without hand-editing Nginx.', [
        'Nginx virtual hosts with aliases, redirects and a validated reload (`nginx -t`) and automatic rollback',
        'A separate PHP-FPM pool per site, several PHP versions side by side and per-site settings',
        'One-click WordPress and Laravel, plus an application installer for popular PHP apps',
        'Take over an existing site: files are copied, Nginx switches in one reload, and the import can be undone',
        'Git deployment with webhooks, cron jobs and systemd workers for queues',
    ]],
    ['email', 'mail', 'Email', 'A complete mail stack, set up for you and kept consistent with DNS.', [
        'Postfix and Dovecot with sign-in on submission and anti-spoofing checks',
        'Rspamd spam filtering (Junk folder or refusal), per-mailbox quotas, aliases and catch-all',
        'DKIM keys, with the MX, SPF, DKIM and DMARC records added to the DNS zone in one click',
        'Roundcube webmail',
    ]],
    ['dns-ssl', 'lock', 'DNS and SSL', 'Zones and certificates that are checked before they go live.', [
        'Authoritative zones on BIND: A, AAAA, CNAME, MX, TXT, NS, SRV and CAA, with templates and secondaries',
        'Every zone is validated with `named-checkzone` and the serial is verified after each reload',
        "Let's Encrypt issue and renew, custom certificates, HTTPS redirect and HSTS",
    ]],
    ['data', 'db', 'Databases and data stores', 'The databases your sites use, with sensible limits.', [
        'MySQL and MariaDB databases, users and grants',
        'A web database browser (rows, structure, SQL console) confined to a single database',
        'Redis with per-site users and a key browser, and Memcached with stats and settings',
    ]],
    ['files', 'file', 'Files and access', 'Safe ways to reach a site\'s files.', [
        'A full-screen file manager: folder tree, drag-and-drop uploads, an editor, permissions, zip and tar.gz',
        'Chrooted FTPS accounts with quotas and TLS required, and SFTP for the site user with no shell',
        'A log viewer with live tail',
    ]],
    ['backups', 'backup', 'Backups', 'Encrypted by default, restorable, and tested.', [
        'AES-256-GCM encrypted site and server backups',
        'S3-compatible storage, SFTP, FTPS, Google Drive or local disk',
        'Schedules and retention; server configuration is restored by component, with rollback',
    ]],
    ['security', 'shield', 'Security', 'Hardening that does not lock you out.', [
        'Two-factor sign-in with an authenticator app or security keys and passkeys, which can be required per role',
        'UFW firewall, SSH hardening and Fail2ban; every change rolls back unless you confirm it',
        'Audit log, CSRF protection and a strict Content-Security-Policy in the panel',
        'A security audit of the server, and file integrity monitoring',
    ]],
    ['hosting', 'users', 'Hosting, plans and teams', 'Run it for yourself or for customers.', [
        'Plans and quotas, resellers and hosting customers, each seeing only their own sites',
        'Roles (administrator, operator, viewer, reseller, customer) and custom roles',
        'Several servers managed from one panel',
    ]],
    ['api', 'code', 'Automation and extension', 'Script it or extend it.', [
        'A REST API with personal access tokens, described by an OpenAPI file',
        'Plugins for the panel',
        'A command-line tool for administrators, with checks (`doctor`) for the installation',
    ]],
];
?>
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
