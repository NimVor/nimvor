<?php
/** @var \NimVorSite\Core\View $v */
/** @var string $install */
/** @var list<array<string, mixed>> $posts */
$v->layout('layouts/site');
$v->set('description', 'NimVor is a free, open-source hosting control panel for VPS and dedicated servers. Websites, email, DNS, databases, SSL and backups, with every change validated and reversible.');
?>
<section class="hero">
    <div class="container hero-grid">
        <div>
            <span class="badge-pill"><span class="dot"></span>Free and open source · MIT licence</span>
            <h1>The control panel that <span class="accent">checks before it changes</span> your server.</h1>
            <p class="lead">Run websites, email, DNS, databases, SSL and backups on your own VPS or dedicated server. NimVor's panel never runs a shell command, and its agent validates every change and rolls it back if anything is wrong.</p>
            <div class="cta-row">
                <a class="btn btn-primary btn-lg" href="/resources/documentation#install">Install NimVor <?= $v->icon('arrow', 18) ?></a>
                <a class="btn btn-ghost btn-lg" href="https://github.com/NimVor/nimvor" rel="noopener"><?= $v->icon('git', 18) ?>View on GitHub</a>
            </div>
            <?= $v->partial('partials/install', ['command' => $install]) ?>
            <ul class="trust mt-2">
                <li><?= $v->icon('check', 16) ?>Read-only until you say otherwise</li>
                <li><?= $v->icon('check', 16) ?>Ubuntu, AlmaLinux, Rocky Linux</li>
                <li><?= $v->icon('check', 16) ?>PHP panel, Go agent</li>
            </ul>
        </div>
        <div class="mock" aria-hidden="true">
            <div class="mock-bar"><i></i><i></i><i></i><span>nimvor · server-01</span></div>
            <div class="mock-body">
                <div class="mock-side">
                    <b class="on">Dashboard</b><b>Websites</b><b>Email</b><b>DNS</b><b>Databases</b><b>Backups</b><b>Security</b>
                </div>
                <div class="mock-main">
                    <div class="mock-tiles">
                        <div class="mock-tile"><small>CPU</small><strong>18%</strong><div class="mock-bar-meter"><progress class="meter" value="18" max="100"></progress></div></div>
                        <div class="mock-tile"><small>Memory</small><strong>2.4 GB</strong><div class="mock-bar-meter"><progress class="meter" value="46" max="100"></progress></div></div>
                        <div class="mock-tile"><small>Disk</small><strong>37%</strong><div class="mock-bar-meter"><progress class="meter" value="37" max="100"></progress></div></div>
                    </div>
                    <div class="mock-chart"><i class="h3"></i><i class="h4"></i><i class="h3"></i><i class="h5"></i><i class="h4"></i><i class="h7"></i><i class="h5"></i><i class="h4"></i><i class="h6"></i><i class="h4"></i><i class="h5"></i><i class="h3"></i></div>
                    <div class="mock-list">
                        <div class="mock-row"><span>example.com</span><em>nginx -t passed · reloaded</em></div>
                        <div class="mock-row"><span>shop.example.org</span><em>certificate renews in 61 days</em></div>
                    </div>
                </div>
            </div>
        </div>
    </div>
</section>

<section class="section">
    <div class="container">
        <div class="section-head center">
            <span class="eyebrow">Everything a server needs</span>
            <h2>One panel for the whole hosting stack</h2>
            <p class="lead">Manage what you already run, from a single place, without giving up control of the server underneath.</p>
        </div>
        <div class="grid grid-3">
            <a class="card card-link" href="/features#websites"><div class="card-icon"><?= $v->icon('globe') ?></div><h3>Websites</h3><p class="muted">PHP, static and reverse-proxy sites on Nginx, with a separate PHP-FPM pool per site and one-click WordPress and Laravel.</p></a>
            <a class="card card-link" href="/features#email"><div class="card-icon"><?= $v->icon('mail') ?></div><h3>Email</h3><p class="muted">Postfix and Dovecot with spam filtering, webmail, mailboxes, aliases and DKIM, SPF and DMARC records ready to publish.</p></a>
            <a class="card card-link" href="/features#dns-ssl"><div class="card-icon"><?= $v->icon('lock') ?></div><h3>DNS and SSL</h3><p class="muted">Authoritative zones on BIND, validated before they load, and Let's Encrypt certificates that renew themselves.</p></a>
            <a class="card card-link" href="/features#data"><div class="card-icon"><?= $v->icon('db') ?></div><h3>Databases</h3><p class="muted">MySQL and MariaDB databases, users and grants, a browser limited to one database, plus Redis and Memcached.</p></a>
            <a class="card card-link" href="/features#backups"><div class="card-icon"><?= $v->icon('backup') ?></div><h3>Backups</h3><p class="muted">Encrypted site and server backups to S3-compatible storage, SFTP, FTPS or local disk, with schedules and restore.</p></a>
            <a class="card card-link" href="/features#security"><div class="card-icon"><?= $v->icon('shield') ?></div><h3>Security</h3><p class="muted">Two-factor sign-in, firewall, SSH hardening and Fail2ban. Risky changes roll back unless you confirm them.</p></a>
        </div>
        <p class="center mt-3"><a class="btn btn-outline" href="/features">See every feature <?= $v->icon('arrow', 16) ?></a></p>
    </div>
</section>

<section class="section section-dark">
    <div class="container">
        <div class="section-head center">
            <span class="eyebrow">Safe by design</span>
            <h2>A control panel has root-level consequences</h2>
            <p class="lead muted">So NimVor treats every privileged operation as something to prove, not assume.</p>
        </div>
        <div class="grid grid-4 principles">
            <div class="card"><span class="n">01</span><h3>Explicit</h3><p class="muted">The panel can only ask the agent for named actions. There is no "run this command" anywhere.</p></div>
            <div class="card"><span class="n">02</span><h3>Validated</h3><p class="muted">Configuration is generated, then checked with the service's own tool, before anything is reloaded.</p></div>
            <div class="card"><span class="n">03</span><h3>Audited</h3><p class="muted">Every privileged or destructive action is recorded, and secrets never reach logs or responses.</p></div>
            <div class="card"><span class="n">04</span><h3>Reversible</h3><p class="muted">The previous working file is kept, and a failed change is rolled back automatically.</p></div>
        </div>
        <div class="flow" aria-label="How a change is applied">
            <span>Detect</span><?= $v->icon('arrow', 16) ?><span>Back up</span><?= $v->icon('arrow', 16) ?><span>Validate</span><?= $v->icon('arrow', 16) ?><span>Apply</span><?= $v->icon('arrow', 16) ?><span>Verify</span><?= $v->icon('arrow', 16) ?><span>Roll back if needed</span>
        </div>
    </div>
</section>

<section class="section">
    <div class="container split">
        <div>
            <span class="eyebrow">Get started</span>
            <h2>Install it next to what you already have</h2>
            <p class="lead">A new installation is read-only: NimVor inventories the server and changes nothing. You switch on write mode when you are ready.</p>
            <div class="steps mt-3">
                <div class="step"><div><h3>Run the installer</h3><p class="muted">One command fetches the source from GitHub and installs the agent and the panel.</p></div></div>
                <div class="step"><div><h3>Open the panel</h3><p class="muted">Reach it over an SSH tunnel, or serve it with HTTPS on its own port.</p></div></div>
                <div class="step"><div><h3>Turn on write mode</h3><p class="muted">Add <code>--enable-writes</code> when you want NimVor to manage websites, mail and DNS.</p></div></div>
            </div>
        </div>
        <div>
            <pre class="codeblock"><code># install (read-only by default)
curl -fsSL https://nimvor.com/install | sudo bash

# serve the panel with HTTPS on port 8443
curl -fsSL https://nimvor.com/install | sudo bash -s -- --public-https

# let NimVor manage the server
curl -fsSL https://nimvor.com/install | sudo bash -s -- --enable-writes</code></pre>
            <h3 class="mt-3">Supported systems</h3>
            <div class="pills mt-1">
                <span class="pill">Ubuntu 22.04</span><span class="pill">Ubuntu 24.04</span><span class="pill">AlmaLinux 9</span><span class="pill">Rocky Linux 9</span><span class="pill">RHEL 9</span>
                <span class="pill preview">Ubuntu 26.04 · preview</span><span class="pill preview">AlmaLinux 10 · preview</span><span class="pill preview">Rocky Linux 10 · preview</span>
            </div>
        </div>
    </div>
</section>

<?php if ($posts !== []): ?>
<section class="section section-soft">
    <div class="container">
        <div class="section-head"><span class="eyebrow">From the blog</span><h2>Latest posts</h2></div>
        <div class="grid grid-3">
            <?php foreach ($posts as $p): ?>
                <article class="card post-card">
                    <time datetime="<?= e(substr((string) $p['published_at'], 0, 10)) ?>"><?= e(substr((string) $p['published_at'], 0, 10)) ?></time>
                    <h3><a href="/resources/blog/<?= e($p['slug']) ?>"><?= e($p['title']) ?></a></h3>
                    <p class="muted"><?= e($p['excerpt']) ?></p>
                </article>
            <?php endforeach; ?>
        </div>
    </div>
</section>
<?php endif; ?>

<section class="section">
    <div class="container">
        <div class="cta-band">
            <h2>Free to use. Free to read. Free to change.</h2>
            <p>NimVor is MIT licensed and written from scratch. Install it, read the code, or help build it.</p>
            <div class="cta-row flush justify-center">
                <a class="btn btn-primary btn-lg" href="/resources/documentation#install">Install NimVor</a>
                <a class="btn btn-ghost btn-lg" href="/resources/community">Join the community</a>
            </div>
        </div>
    </div>
</section>
