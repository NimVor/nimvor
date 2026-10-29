<?php
/** @var \NimVorSite\Core\View $v */
/** @var string $install */
/** @var list<array<string, mixed>> $posts */
/** @var ?int $servers */
$v->layout('layouts/site');
$v->set('description', 'NimVor is a free, open-source hosting control panel for VPS and dedicated servers. Websites, email, DNS, databases, SSL and backups, with every change validated and reversible.');
?>
<section class="hero">
    <div class="container hero-grid">
        <div>
            <span class="eyebrow">Free. Open. Yours to audit.</span>
            <h1>Your servers, in your hands.</h1>
            <p class="lead">Websites, email, DNS and backups on your own server. One free control panel that checks before it changes anything.</p>
            <div class="cta-row">
                <a class="btn btn-primary btn-lg" href="/get-started">Install free <?= $v->icon('arrow', 18) ?></a>
                <a class="link-arrow" href="/features">Explore features</a>
            </div>
            <?= $v->partial('partials/install', ['command' => $install]) ?>
            <ul class="trust mt-3">
                <li><?= $v->icon('check', 16) ?>Read-only until you say so</li>
                <li><?= $v->icon('check', 16) ?>Ubuntu · AlmaLinux · Rocky</li>
            </ul>
        </div>
        <div><?= $v->partial('partials/hero-art') ?></div>
    </div>
</section>

<?php if (($servers ?? null) !== null): ?>
<section class="strip" aria-label="Usage">
    <div class="container strip-in"><strong><?= number_format($servers) ?></strong><span>server<?= $servers === 1 ? '' : 's' ?> have installed NimVor</span><a class="link-arrow" href="/resources/stats">See the numbers <?= $v->icon('arrow', 14) ?></a></div>
</section>
<?php endif; ?>
<section class="section">
    <div class="container">
        <div class="section-head split-head">
            <div><span class="eyebrow">Everything a server needs</span><h2>Build it on your own terms.</h2></div>
            <p>One panel for the whole hosting stack. Run what you already run, without giving up control of the server underneath.</p>
        </div>
        <div class="rows">
            <a class="row-cell" href="/features#websites"><span class="label">01 / Hosting</span><h3>Websites that stay up</h3><p>PHP, static and proxy sites on Nginx, a PHP-FPM pool for each, one-click WordPress and Laravel.</p><span class="arrow"><?= $v->icon('arrow', 28) ?></span></a>
            <a class="row-cell" href="/features#email"><span class="label">02 / Email and DNS</span><h3>Give your email a home</h3><p>Postfix, Dovecot, spam filtering and webmail, with DNS records that are checked before they load.</p><span class="arrow"><?= $v->icon('arrow', 28) ?></span></a>
            <a class="row-cell" href="/features#backups"><span class="label">03 / Backups and security</span><h3>Safe to change, easy to undo</h3><p>Encrypted backups, a firewall and two-factor sign-in. Risky changes roll back unless you confirm them.</p><span class="arrow"><?= $v->icon('arrow', 28) ?></span></a>
        </div>
    </div>
</section>

<section class="section section-dark">
    <div class="container">
        <div class="section-head split-head">
            <div><span class="eyebrow">A little less admin. A lot more building.</span><h2>Everything in its place.</h2></div>
            <p>From a first site to a whole fleet, the panel brings the everyday work together and shows you what it is about to change.</p>
        </div>
        <div class="walk" aria-hidden="true">
            <div class="walk-top"><span><span class="dot"></span>Product walkthrough</span><span>server-01</span></div>
            <div class="walk-body">
                <div class="walk-side"><div class="who"><img src="<?= e($v->asset('img/logo.svg')) ?>" alt="">NimVor</div><b class="on">Dashboard</b><b>Websites</b><b>Email</b><b>DNS</b><b>Databases</b><b>Backups</b><b>Security</b></div>
                <div class="walk-main">
                    <div class="walk-tiles">
                        <div class="walk-tile"><small>CPU</small><strong>18%</strong><progress class="meter" value="18" max="100"></progress></div>
                        <div class="walk-tile"><small>Memory</small><strong>2.4 GB</strong><progress class="meter" value="46" max="100"></progress></div>
                        <div class="walk-tile"><small>Disk</small><strong>37%</strong><progress class="meter" value="37" max="100"></progress></div>
                    </div>
                    <div class="walk-chart"><i class="h3"></i><i class="h4"></i><i class="h3"></i><i class="h5"></i><i class="h4"></i><i class="h7"></i><i class="h5"></i><i class="h4"></i><i class="h6"></i><i class="h4"></i><i class="h5"></i><i class="h3"></i></div>
                    <div class="walk-list">
                        <div class="walk-row"><span>example.com</span><em>nginx -t passed · reloaded</em></div>
                        <div class="walk-row"><span>shop.example.org</span><em>certificate renews in 61 days</em></div>
                    </div>
                </div>
            </div>
        </div>
        <div class="chips"><span class="chip">Websites</span><span class="chip">Email</span><span class="chip">DNS</span><span class="chip">SSL</span><span class="chip">Databases</span><span class="chip">Backups</span><span class="chip">Firewall</span><span class="chip">REST API</span></div>
    </div>
</section>

<section class="section">
    <div class="container split">
        <div>
            <span class="eyebrow">Safe by design</span>
            <h2>A control panel has root-level consequences.</h2>
            <p class="lead">So NimVor treats every privileged operation as something to prove, not assume.</p>
            <a class="btn btn-dark mt-2" href="/resources/about">How we build it</a>
        </div>
        <div class="list-rows">
            <div class="list-row"><span class="n">01</span><div><h3>Explicit</h3><p>The panel can only ask the agent for named actions. There is no "run this command" anywhere.</p></div></div>
            <div class="list-row"><span class="n">02</span><div><h3>Validated</h3><p>Configuration is generated, then checked with the service's own tool, before anything is reloaded.</p></div></div>
            <div class="list-row"><span class="n">03</span><div><h3>Audited</h3><p>Every privileged or destructive action is recorded. Secrets never reach logs or responses.</p></div></div>
            <div class="list-row"><span class="n">04</span><div><h3>Reversible</h3><p>The previous working file is kept, and a failed change is rolled back automatically.</p></div></div>
        </div>
    </div>
</section>

<section class="section section-sage">
    <div class="container split">
        <div>
            <span class="eyebrow">Yours to build on</span>
            <h2>Open source. Room to grow.</h2>
            <p class="lead">Start with the free panel. Add the help and services that make sense for you.</p>
            <a class="btn btn-dark mt-2" href="/get-started">Install NimVor <?= $v->icon('arrow', 18) ?></a>
            <div class="pills mt-3">
                <span class="pill">Ubuntu 22.04</span><span class="pill">Ubuntu 24.04</span><span class="pill">AlmaLinux 9</span><span class="pill">Rocky Linux 9</span><span class="pill">RHEL 9</span>
                <span class="pill preview">Ubuntu 26.04 · preview</span><span class="pill preview">AlmaLinux 10 · preview</span><span class="pill preview">Rocky Linux 10 · preview</span>
            </div>
        </div>
        <div class="list-rows">
            <div class="list-row"><span class="n">01</span><div><h3>The free foundation</h3><p>The whole panel under the MIT licence: websites, mail, DNS, databases, SSL, backups and security.</p></div></div>
            <div class="list-row"><span class="n">02</span><div><h3>Help when you want it</h3><p>Migrations, hardened installs, reviews and custom features, from the people who build NimVor.</p><a class="link-arrow mt-1" href="/resources/build-services">See build services <?= $v->icon('arrow', 14) ?></a></div></div>
            <div class="list-row"><span class="n">03</span><div><h3>A community to build with</h3><p>Read the source, follow development and find help in the documentation.</p><a class="link-arrow mt-1" href="/resources/community">Explore on GitHub <?= $v->icon('arrow', 14) ?></a></div></div>
        </div>
    </div>
</section>

<?php if ($posts !== []): ?>
<section class="section">
    <div class="container">
        <div class="section-head"><span class="eyebrow">From the blog</span><h2>Latest posts.</h2></div>
        <div class="rows">
            <?php foreach ($posts as $p): ?>
                <a class="row-cell post-card" href="/resources/blog/<?= e($p['slug']) ?>"><span class="label"><?= e(substr((string) $p['published_at'], 0, 10)) ?></span><h3><?= e($p['title']) ?></h3><p><?= e($p['excerpt']) ?></p><span class="arrow"><?= $v->icon('arrow', 28) ?></span></a>
            <?php endforeach; ?>
        </div>
    </div>
</section>
<?php endif; ?>

<section class="section section-soft">
    <div class="container split">
        <div><span class="eyebrow">Good questions</span><h2>Before you install.</h2><p class="lead">Short answers to what people ask first.</p></div>
        <div class="faq">
            <details><summary>Will it change my server the moment I install it?</summary><p>No. A new installation is read-only: NimVor inventories the server and changes nothing. You switch on write mode with <code>--enable-writes</code> when you are ready for it to manage websites, mail and DNS.</p></details>
            <details><summary>Can I use it next to another panel?</summary><p>Read-only mode is safe next to an existing panel, and NimVor can take over a website it discovers: the files are copied, Nginx switches in one validated reload, and the import can be undone.</p></details>
            <details><summary>What does it run on?</summary><p>Ubuntu 22.04 and 24.04, AlmaLinux 9, Rocky Linux 9 and RHEL 9 are supported. Ubuntu 26.04 and version 10 of the RHEL family are in preview.</p></details>
            <details><summary>Is it really free?</summary><p>Yes. NimVor is MIT licensed with no paywalled tier. If you want someone to do the work for you, see <a href="/resources/build-services">build services</a>.</p></details>
            <details><summary>How do I report a security problem?</summary><p>Please do not open a public issue. Use GitHub's private vulnerability reporting on the repository.</p></details>
        </div>
    </div>
</section>

<section class="section">
    <div class="container">
        <div class="cta-band">
            <span class="eyebrow">Get started</span>
            <h2>Free to use. Free to read. Free to change.</h2>
            <p>Install it, read the code, or help build it.</p>
            <div class="cta-row flush">
                <a class="btn btn-primary btn-lg" href="/get-started">Install NimVor</a>
                <a class="btn btn-ghost btn-lg" href="/resources/community">Join the community</a>
            </div>
        </div>
    </div>
</section>
