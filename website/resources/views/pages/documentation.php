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
        <div class="prose">
            <h2 class="mt-0">Install</h2>
            <p>On a fresh Ubuntu, AlmaLinux or Rocky Linux server, as root or with <code>sudo</code>:</p>
            <?= $v->partial('partials/install', ['command' => $install]) ?>
            <p class="mt-2">The script installs <code>git</code> and <code>curl</code> if they are missing, fetches the source from GitHub and runs the installer. A new installation is <strong>read-only</strong>: it inventories the server and changes nothing.</p>
            <h3>Common options</h3>
            <pre class="codeblock"><code># serve the panel with HTTPS on port 8443
curl -fsSL https://nimvor.com/install | sudo bash -s -- --public-https

# let NimVor manage websites, mail, DNS and more
curl -fsSL https://nimvor.com/install | sudo bash -s -- --enable-writes

# only fetch the source, so you can read it first
curl -fsSL https://nimvor.com/install | sudo bash -s -- --fetch-only

# pin the install to an exact commit
curl -fsSL https://nimvor.com/install | sudo bash -s -- --commit SHA</code></pre>
            <h3>Update</h3>
            <p>From the source checkout (by default <code>/usr/local/src/nimvor</code>). It keeps your configuration, database, write mode and ports:</p>
            <pre class="codeblock"><code>cd /usr/local/src/nimvor &amp;&amp; sudo ./installer/update.sh</code></pre>
        </div>
        <aside>
            <div class="card">
                <h3>Administrator commands</h3>
                <p class="muted">The panel has a command-line tool for the server administrator:</p>
                <pre class="codeblock"><code>sudo -u nimvor NIMVOR_CONFIG=/etc/nimvor/panel.php \
  php /opt/nimvor/panel/bin/nimvor user:password admin</code></pre>
                <p class="muted">Other commands: <code>user:2fa-reset</code>, <code>doctor</code>, <code>setup:status</code>, <code>agent:check</code>. Newer installs also provide the short form <code>sudo nimvor …</code>.</p>
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
