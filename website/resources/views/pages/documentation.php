<?php
/** @var \NimVorSite\Core\View $v */
/** @var string $install */
$v->layout('layouts/site');
$v->set('title', 'Documentation');
$v->set('description', 'Install NimVor, update it, reset an administrator password and find the full documentation.');
$docs = [
    ['Installation', '/docs/installation', 'Requirements, every installer option, accessing the panel, verifying, updating and uninstalling.'],
    ['Administration', '/docs/administration', 'Day-to-day use of every area of the panel.'],
    ['Security', '/docs/security', 'The security model, what is protected and how to report a problem.'],
    ['Agent', '/docs/agent', 'The Go service: actions, the write gate and what it will and will not run.'],
    ['REST API', '/docs/api', 'Personal tokens and the /api/v1 endpoints.'],
    ['Architecture', '/docs/architecture', 'How the panel, agent and installer fit together.'],
    ['Plugins', '/docs/plugins', 'Extending the panel.'],
    ['Troubleshooting', '/docs/troubleshooting', 'Common problems and how to find the cause.'],
    ['Development', '/docs/development', 'Building, testing and contributing to NimVor.'],
];
?>
<?= $v->partial('partials/page-head', ['title' => 'Documentation', 'intro' => 'The full documentation is kept with the code and shown here, so it matches the release you run.', 'crumbs' => [['Resources', '/resources'], ['Documentation', '']]]) ?>
<section class="section" id="install">
    <div class="container split">
        <div class="prose" data-doc-body>
            <h2 class="mt-0">Install</h2>
            <p>On a server running <strong>Ubuntu 22.04 or 24.04, AlmaLinux 9, Rocky Linux 9 or RHEL 9</strong> (preview: Ubuntu 26.04 and AlmaLinux, Rocky Linux or RHEL 10), as root or with <code>sudo</code>. The server needs systemd and internet access. A fresh server is best; an existing one keeps its websites and services untouched until you import them.</p>

            <h3>1. Look first (changes nothing)</h3>
            <p>The default install is <strong>read-only</strong>: NimVor inventories the server and changes nothing.</p>
            <?= $v->partial('partials/install', ['command' => $install]) ?>

            <h3>2. Install a hosting server (recommended)</h3>
            <p>This is the whole command: it sets the server up for hosting, turns on write mode, and asks nothing (<code>--yes</code>).</p>
            <pre class="codeblock"><code>curl -fsSL https://nimvor.com/install | sudo bash -s -- \
  --yes \
  --enable-writes \
  --profile=full \
  --hostname=server.example.com \
  --admin-user=admin \
  --admin-email=you@example.com \
  --php=8.3,8.4 \
  --public-https</code></pre>
            <p>Use your <strong>real</strong> email for <code>--admin-email</code>: Let&rsquo;s Encrypt sends expiry notices there. <code>--hostname</code> is the mail server&rsquo;s full name (the system&rsquo;s own host name by default). Everything after <code>-s --</code> is passed to the installer. The installer prints the administrator password <strong>once</strong> at the end: store it.</p>

            <h3>Options</h3>
            <div class="table-wrap"><table class="data"><thead><tr><th>Option</th><th>What it does</th></tr></thead><tbody>
                <tr><td><code>--yes</code></td><td>Answer yes to every question (needed in scripts; without a terminal and without it, no server profile is applied).</td></tr>
                <tr><td><code>--enable-writes</code></td><td>Write mode: NimVor may manage websites, databases, certificates, mail, DNS and the firewall. Without it NimVor only observes. A server profile turns it on too.</td></tr>
                <tr><td><code>--profile=full|web|minimal|none</code></td><td>What to set up: <code>full</code> (4 GB+) Nginx, PHP, MariaDB, mail, DNS, FTP, Redis, ClamAV, firewall and more; <code>web</code> (2 GB+) without mail, DNS and ClamAV; <code>minimal</code>; <code>none</code> installs NimVor only.</td></tr>
                <tr><td><code>--hostname=NAME</code></td><td>The mail server&rsquo;s full name, such as <code>server.example.com</code>.</td></tr>
                <tr><td><code>--php=8.3,8.4</code></td><td>PHP versions for websites (default 8.3,8.4), for example <code>--php=8.2,8.3,8.4</code>. Versions the system lacks come from a third-party repository (see below).</td></tr>
                <tr><td><code>--admin-user=NAME</code></td><td>The first administrator&rsquo;s user name.</td></tr>
                <tr><td><code>--admin-email=EMAIL</code></td><td>Their email (also the Let&rsquo;s Encrypt contact).</td></tr>
                <tr><td><code>--port=8090</code></td><td>The panel&rsquo;s own port on 127.0.0.1 (1024&ndash;65535).</td></tr>
                <tr><td><code>--public-https[=8443]</code></td><td>Serve the panel at <code>https://SERVER-IP:8443/</code> with a self-signed certificate and open that port in the firewall. Without it, reach the panel through an SSH tunnel.</td></tr>
                <tr><td><code>--dry-run</code></td><td>Show what would happen and change nothing.</td></tr>
                <tr><td><code>--reinstall</code> or <code>--resume</code></td><td>Run again on an installed or half-installed server: replaces program files, keeps configuration, database and settings. Use it to finish an interrupted install.</td></tr>
                <tr><td><code>--no-install-deps</code></td><td>Only list missing packages instead of installing them.</td></tr>
                <tr><td><code>--no-third-party-repos</code></td><td>Never add Ond&#345;ej Sur&yacute;&rsquo;s PHP repository (Ubuntu) or Remi&rsquo;s (RHEL family); PHP versions the system does not offer are skipped.</td></tr>
                <tr><td><code>--force-unsupported</code></td><td>Try an unsupported system anyway, best effort.</td></tr>
                <tr><td><code>--agent-only</code></td><td>Install only the agent, for a server another NimVor panel manages. Add <code>--remote-access=0.0.0.0:8443 --allow-from=PANEL_IP</code>.</td></tr>
            </tbody></table></div>
            <p class="mt-2">The one-line script has its own options too, given before the installer&rsquo;s: <code>--version X.Y.Z</code> (default: the newest stable release), <code>--branch NAME</code>, <code>--commit SHA</code> (stop unless the source is exactly that commit), <code>--source URL</code> (a mirror), <code>--dir PATH</code> (where the source is kept) and <code>--fetch-only</code> (download the source, so you can read it first). Example:</p>
            <pre class="codeblock"><code>curl -fsSL https://nimvor.com/install | sudo bash -s -- --version 1.0.0 --commit SHA --yes</code></pre>

            <h3>Where things are installed</h3>
            <div class="table-wrap"><table class="data"><thead><tr><th>What</th><th>Where</th></tr></thead><tbody>
                <tr><td>Source checkout (what the installer ran from, used to update)</td><td><code>/usr/local/src/nimvor</code></td></tr>
                <tr><td>The panel (PHP)</td><td><code>/opt/nimvor</code></td></tr>
                <tr><td>The agent (Go program)</td><td><code>/usr/local/bin/nimvor-agent</code></td></tr>
                <tr><td>Configuration and tokens</td><td><code>/etc/nimvor</code></td></tr>
                <tr><td>Panel database and data</td><td><code>/var/lib/nimvor</code></td></tr>
                <tr><td>Logs</td><td><code>/var/log/nimvor</code></td></tr>
                <tr><td>Websites NimVor creates (write mode)</td><td><code>/home/nimvor</code></td></tr>
                <tr><td>Backups and the agent&rsquo;s state (write mode)</td><td><code>/var/backups/nimvor</code>, <code>/var/lib/nimvor-agent</code></td></tr>
                <tr><td>Services</td><td><code>nimvor-agent</code>, <code>nimvor-panel</code>, <code>nimvor-scheduler.timer</code> (systemd)</td></tr>
            </tbody></table></div>

            <h3>Open the panel</h3>
            <p>With <code>--public-https</code>: browse to <code>https://SERVER-IP:8443/</code> (the browser warns once about the self-signed certificate). Otherwise use an SSH tunnel from your computer:</p>
            <pre class="codeblock"><code>ssh -L 8090:127.0.0.1:8090 you@server.example.com
# then open http://127.0.0.1:8090/</code></pre>
            <p>Sign in with the administrator and password the installer printed, then turn on two-factor sign-in in your profile.</p>

            <h3>Update</h3>
            <p>From the source checkout. It keeps your configuration, database, write mode and ports:</p>
            <pre class="codeblock"><code>cd /usr/local/src/nimvor &amp;&amp; sudo ./installer/update.sh</code></pre>
            <p>Running the one-line installer again does the same. If an installation was interrupted, run the same command again with <code>--resume</code>. When NimVor is installed or upgraded, the installer sends one anonymous report that feeds the <a href="/resources/stats">statistics</a>.</p>
        </div>
        <aside>
            <div class="card">
                <h3>Administrator commands</h3>
                <p class="muted">The server administrator&rsquo;s command-line tool:</p>
                <pre class="codeblock"><code>sudo nimvor user:password admin
sudo nimvor doctor
sudo nimvor help</code></pre>
                <p class="muted"><code>user:password</code> sets a new password for a user, <code>doctor</code> checks the installation. More in the <a href="/docs/installation">installation guide</a>.</p>
            </div>
        </aside>
    </div>
</section>
<section class="section section-soft">
    <div class="container">
        <div class="section-head"><span class="eyebrow">Read more</span><h2>The full documentation</h2><p><a class="btn btn-outline" href="/docs">Browse all pages <?= $v->icon('arrow', 16) ?></a></p></div>
        <div class="grid grid-3">
            <?php foreach ($docs as [$title, $file, $text]): ?>
                <a class="card card-link" href="<?= e($file) ?>"><h3><?= e($title) ?> <?= $v->icon('arrow', 16) ?></h3><p class="muted"><?= e($text) ?></p></a>
            <?php endforeach; ?>
        </div>
    </div>
</section>
