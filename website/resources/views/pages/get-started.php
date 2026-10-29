<?php
/** @var \NimVorSite\Core\View $v */
/** @var string $install */
$v->layout('layouts/site');
$v->set('title', 'Install NimVor: a free control panel for Ubuntu, AlmaLinux and Rocky Linux');
$v->set('description', 'Install NimVor in one command. Build the exact command for your server: profile, PHP versions, host name, administrator and options. Free and open source.');
$v->set('script', 'js/install-builder.js');
$recommended = "curl -fsSL https://nimvor.com/install | sudo bash -s -- \\\n  --yes \\\n  --enable-writes \\\n  --profile=full \\\n  --php=8.3,8.4 \\\n  --admin-user=admin \\\n  --admin-email=you@example.com";
$phpChoices = ['8.0' => false, '8.1' => false, '8.2' => false, '8.3' => true, '8.4' => true];
?>
<?= $v->partial('partials/page-head', ['title' => 'Install NimVor', 'intro' => 'One command sets up the panel, and a server for hosting if you want it. Choose your options below and copy the command.', 'crumbs' => [['Install NimVor', '']]]) ?>
<section class="section" id="install">
    <div class="container split">
        <div class="prose" data-doc-body>
            <h2 class="mt-0">Before you start</h2>
            <p>You need a server running <strong>Ubuntu 22.04 or 24.04, AlmaLinux 9, Rocky Linux 9 or RHEL 9</strong> (preview: Ubuntu 26.04 and AlmaLinux, Rocky Linux or RHEL 10), a root login or <code>sudo</code>, systemd and internet access. A fresh server is best; on an existing one, websites and services stay untouched until you import them. The full hosting profile wants 4&nbsp;GB of memory or more.</p>

            <h2>Build your install command</h2>
            <div id="install-static">
                <p>Run this on the server (it sets up a full hosting server; change the user name and email):</p>
                <pre class="codeblock"><code><?= e($recommended) ?></code></pre>
                <p class="muted">Turn on JavaScript to choose the options and get your own command.</p>
            </div>
            <form id="install-builder" class="builder" hidden novalidate aria-label="Install command builder">
                <fieldset>
                    <legend>What do you want to install?</legend>
                    <label class="opt"><input type="radio" name="mode" value="hosting" checked> <span><strong>A hosting server</strong> (recommended)<small>Sets the server up for websites, with write mode on.</small></span></label>
                    <label class="opt"><input type="radio" name="mode" value="look"> <span><strong>Just look first</strong><small>Read-only: NimVor inventories the server and changes nothing.</small></span></label>
                    <label class="opt"><input type="radio" name="mode" value="agent"> <span><strong>The agent only</strong><small>For a server that another NimVor panel manages.</small></span></label>
                </fieldset>

                <div class="field" data-mode="hosting">
                    <label for="ib-profile">Server profile</label>
                    <select class="select" id="ib-profile" name="profile">
                        <option value="full" selected>Full: websites, email, DNS, FTP, antivirus (4 GB+)</option>
                        <option value="web">Web: websites and databases, no email or DNS (2 GB+)</option>
                        <option value="minimal">Minimal: Nginx, PHP, MariaDB, firewall</option>
                        <option value="none">None: install NimVor only</option>
                    </select>
                </div>
                <div class="field" data-mode="hosting" data-profile="full web minimal">
                    <span class="label">PHP versions for websites</span>
                    <div class="checks-row">
                        <?php foreach ($phpChoices as $ver => $on): ?>
                            <label class="check"><input type="checkbox" name="php" value="<?= e($ver) ?>"<?= $on ? ' checked' : '' ?>> PHP <?= e($ver) ?></label>
                        <?php endforeach; ?>
                    </div>
                    <small class="muted">Versions the system does not offer come from a third-party repository (Ond&#345;ej Sur&yacute; on Ubuntu, Remi on the RHEL family) unless you tick &ldquo;no third-party repositories&rdquo; below.</small>
                </div>
                <div class="field" data-mode="hosting" data-profile="full">
                    <label for="ib-hostname">Mail server host name</label>
                    <input class="input" id="ib-hostname" name="hostname" autocomplete="off" placeholder="server.example.com" maxlength="253">
                </div>
                <div class="field" data-mode="hosting look">
                    <label for="ib-user">Administrator user name</label>
                    <input class="input" id="ib-user" name="adminUser" autocomplete="off" placeholder="admin" maxlength="32">
                </div>
                <div class="field" data-mode="hosting look">
                    <label for="ib-email">Administrator email</label>
                    <input class="input" id="ib-email" name="adminEmail" type="email" autocomplete="off" placeholder="you@yourdomain.com" maxlength="190">
                    <small class="muted">Also the Let&rsquo;s Encrypt contact for expiry notices: use a real address.</small>
                </div>
                <div class="field" data-mode="hosting look">
                    <label class="check"><input type="checkbox" name="publicHttps"> Open the panel over HTTPS on this server (self-signed certificate)</label>
                    <div class="inline-fields">
                        <label for="ib-https-port">HTTPS port</label>
                        <input class="input narrow-input" id="ib-https-port" name="httpsPort" inputmode="numeric" placeholder="8443" maxlength="5" disabled>
                        <label for="ib-port">Panel&rsquo;s own port</label>
                        <input class="input narrow-input" id="ib-port" name="port" inputmode="numeric" placeholder="8090" maxlength="5">
                    </div>
                    <small class="muted">Without it, reach the panel through an SSH tunnel.</small>
                </div>
                <div class="field" data-mode="agent">
                    <label for="ib-remote">Address the agent listens on</label>
                    <input class="input" id="ib-remote" name="remote" autocomplete="off" placeholder="0.0.0.0:8443" maxlength="60">
                </div>
                <div class="field" data-mode="agent">
                    <label for="ib-allow">Allow connections only from (the panel server&rsquo;s IP)</label>
                    <input class="input" id="ib-allow" name="allow" autocomplete="off" placeholder="203.0.113.10" maxlength="200">
                </div>

                <details class="advanced">
                    <summary>More options</summary>
                    <label class="check"><input type="checkbox" name="yes" checked> Do not ask questions (<code>--yes</code>)</label>
                    <label class="check"><input type="checkbox" name="resume"> Finish an interrupted install or run again (<code>--resume</code>)</label>
                    <label class="check" data-mode="hosting"><input type="checkbox" name="noThirdParty"> No third-party repositories (<code>--no-third-party-repos</code>)</label>
                    <label class="check"><input type="checkbox" name="noDeps"> Only list missing packages (<code>--no-install-deps</code>)</label>
                    <label class="check"><input type="checkbox" name="forceUnsupported"> Try an unsupported system anyway (<code>--force-unsupported</code>)</label>
                    <label class="check"><input type="checkbox" name="dryRun"> Show what would happen, change nothing (<code>--dry-run</code>)</label>
                    <div class="field">
                        <label for="ib-version">Release (default: the newest stable)</label>
                        <input class="input narrow-input" id="ib-version" name="version" autocomplete="off" placeholder="1.1.0" maxlength="20">
                    </div>
                    <div class="field">
                        <label for="ib-commit">Require this exact commit</label>
                        <input class="input" id="ib-commit" name="commit" autocomplete="off" placeholder="full or short commit hash" maxlength="40">
                    </div>
                </details>

                <div class="builder-out">
                    <h3 class="mt-0">Your command</h3>
                    <ul id="ib-errors" class="alert alert-error list-plain" role="alert" hidden></ul>
                    <pre class="codeblock"><code id="ib-command"><?= e($recommended) ?></code></pre>
                    <ul id="ib-notes" class="list-plain muted" hidden></ul>
                    <button class="btn btn-primary" id="ib-copy" type="button" data-copy="<?= e($recommended) ?>"><?= $v->icon('copy', 18) ?> <span class="label">Copy command</span></button>
                    <p class="muted mb-0 mt-1">Run it on the server, as root or with <code>sudo</code>. The installer prints the administrator password <strong>once</strong> at the end: store it.</p>
                </div>
            </form>

            <h2>The short version</h2>
            <p>The default install is <strong>read-only</strong>: NimVor inventories the server and changes nothing.</p>
            <?= $v->partial('partials/install', ['command' => $install]) ?>

            <h2>Every option</h2>
            
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

            <h2>Where things are installed</h2>
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

            <h2>Open the panel</h2>
            <p>With <code>--public-https</code>: browse to <code>https://SERVER-IP:8443/</code> (the browser warns once about the self-signed certificate). Otherwise use an SSH tunnel from your computer:</p>
            <pre class="codeblock"><code>ssh -L 8090:127.0.0.1:8090 you@server.example.com
# then open http://127.0.0.1:8090/</code></pre>
            <p>Sign in with the administrator and password the installer printed, then turn on two-factor sign-in in your profile.</p>

            <h2>Update</h2>
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
