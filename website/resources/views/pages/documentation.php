<?php
/** @var \NimVorSite\Core\View $v */
$v->layout('layouts/site');
$v->set('title', 'Documentation');
$v->set('description', 'The NimVor documentation: administration, security, the agent, the REST API, architecture and troubleshooting.');
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
            <h2 class="mt-0">Install NimVor</h2>
            <p>One command installs the panel and, if you want, sets the server up for hosting. The install page builds the exact command for you: choose the profile, PHP versions, host name and administrator, then copy it.</p>
            <p><a class="btn btn-primary" href="/get-started">Build your install command <?= $v->icon('arrow', 16) ?></a></p>
            <p class="muted">Already installed? Update from the source checkout with <code>cd /usr/local/src/nimvor &amp;&amp; sudo ./installer/update.sh</code>.</p>
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
