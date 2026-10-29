<?php
/** @var \NimVorSite\Core\View $v */
$v->layout('layouts/site');
$v->set('title', 'About us');
$v->set('description', 'NimVor is an independent, MIT-licensed hosting control panel written from scratch. This is what it is for and the rules it follows.');
?>
<?= $v->partial('partials/page-head', ['title' => 'About us', 'intro' => 'A control panel with root-level consequences, built so that you can see what it does.', 'crumbs' => [['Resources', '/resources'], ['About us', '']]]) ?>
<section class="section">
    <div class="container split">
        <div class="prose">
            <h2 class="mt-0">What NimVor is</h2>
            <p>NimVor is a free, MIT-licensed control panel for Linux VPS and dedicated servers, in the same family of tools as the commercial hosting panels. It manages websites, email, DNS, databases, SSL, backups and security from one place.</p>
            <p>It is written from scratch and is not affiliated with any other control panel.</p>
            <h2>Why it exists</h2>
            <p>A hosting panel can change almost everything on a server. Most are closed, hard to audit and tied to a licence fee. NimVor is open so that you can read what it does, run it without a licence and change it when you need to.</p>
            <h2>How it is built</h2>
            <p>Two parts with a hard line between them: a PHP web panel that never runs a shell command, and a Go agent that performs a fixed list of named actions as root. A change is detected, backed up, validated, applied and verified, and rolled back if it fails.</p>
        </div>
        <div>
            <div class="card">
                <h3>The rules we follow</h3>
                <ul class="checks">
                    <li><?= $v->icon('check', 18) ?><span>Never break existing websites.</span></li>
                    <li><?= $v->icon('check', 18) ?><span>No arbitrary command execution: new capability means a new named action.</span></li>
                    <li><?= $v->icon('check', 18) ?><span>Never trust a path: every one is validated and confined.</span></li>
                    <li><?= $v->icon('check', 18) ?><span>Never reload an invalid configuration.</span></li>
                    <li><?= $v->icon('check', 18) ?><span>Running an action twice must not corrupt anything.</span></li>
                    <li><?= $v->icon('check', 18) ?><span>Never expose secrets in responses, logs or audit records.</span></li>
                    <li><?= $v->icon('check', 18) ?><span>Audit every privileged or destructive action.</span></li>
                    <li><?= $v->icon('check', 18) ?><span>Original work only.</span></li>
                </ul>
            </div>
            <div class="card mt-3">
                <h3>Get involved</h3>
                <p class="muted">Read the code, report a problem, sponsor the project or ask for help.</p>
                <div class="cta-row flush">
                    <a class="btn btn-outline btn-sm" href="/resources/community">Community &amp; source</a>
                    <a class="btn btn-outline btn-sm" href="/support/contact">Contact us</a>
                </div>
            </div>
        </div>
    </div>
</section>
