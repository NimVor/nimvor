<?php
/** @var \NimVorSite\Core\View $v */
$v->layout('layouts/site');
$v->set('title', 'Privacy policy');
$v->set('description', 'What the NimVor website stores about you, why, and for how long.');
?>
<?= $v->partial('partials/page-head', ['title' => 'Privacy policy', 'intro' => 'Short, because this website collects very little.', 'crumbs' => [['Support', '/support'], ['Privacy policy', '']]]) ?>
<section class="section">
    <div class="container">
        <article class="prose">
            <p class="muted">Applies to this website and to the optional telemetry reports of NimVor installations (described below). Once installed, the NimVor software on your server sends nothing to us unless its administrator turns telemetry on.</p>
            <h2>What we store</h2>
            <ul>
                <li><strong>Visitors.</strong> We count page visits per day and per page. We do not store your address, a cookie or any other identifier for this, and we use no analytics service.</li>
                <li><strong>Accounts.</strong> If you register: your name, email address, a hash of your password (we never store the password), the time you registered and last signed in.</li>
                <li><strong>Tickets and requests.</strong> What you type into the contact form, a ticket or the build-service form, with your name and email, and our answers, so we can reply. A ticket without an account is reached only through a private link that is shown to you after you send it (and emailed when the site can send email).</li>
                <li><strong>Telemetry reports (optional).</strong> Only from an installation whose administrator turned telemetry on, and only until they turn it off: a random installation identifier, the NimVor version, the operating system's name and major version, the processor type, the server's size rounded up, which components are installed with their major and minor version, counts of things (for example how many websites exist, never their names) and a few health answers. No host name, address, domain, user, e-mail or file, and we do not keep the sender's address (it is counted for an hour only, to limit abuse). The administrator can turn it off, unregister, or have everything stored for the installation deleted. It feeds the public <a href="/resources/stats">statistics</a> and our own planning, and is never used for licensing. Servers installed by older versions sent one anonymous report at install and upgrade (a random identifier, the version, the system and the processor type); no newer installer does.</li>
                <li><strong>Server logs.</strong> The web server keeps ordinary access and error logs (including addresses) for security and fixing faults.</li>
            </ul>
            <h2>Videos</h2>
            <p>Videos shown from YouTube are loaded from <code>youtube-nocookie.com</code>, YouTube's privacy-enhanced address, and only on the page of that video. YouTube then sees your address, as any site you open would. Uploaded videos are served by this site and nothing is sent to anyone else.</p>
            <h2>Cookies</h2>
            <p>One cookie, <code>nimvor_sid</code>, is set only when you sign in or send a form. It identifies your session, is not readable by scripts and is deleted when you log out or after a few hours. We use no advertising or tracking cookies.</p>
            <h2>How we use it</h2>
            <p>To run your account, to answer you, and to keep the site secure (for example to slow down repeated failed sign-ins, using your address for a short time). We do not sell your data or share it for advertising.</p>
            <h2>Who sees it</h2>
            <p>The people who run the NimVor project, and the hosting provider that runs this site. We share it with others only if the law requires.</p>
            <h2>How long we keep it</h2>
            <p>Account details until you ask us to delete the account. Tickets and requests until they are no longer needed to help you. Visit counts and installation reports, which are not personal, indefinitely.</p>
            <h2>Your choices</h2>
            <p>You can ask to see, correct or delete what we hold about you through the <a href="/support/contact">contact form</a>, or change your name and password under your account.</p>
            <p class="muted">The documentation, the problems list and the sponsors shown on this site are fetched by the site's server from GitHub and, for patrons, from Patreon; your browser contacts neither for them. For a Patreon patron the site keeps only the name, the public Patreon profile link and the monthly amount that Patreon shows for the campaign, never an email address or payment detail.</p>
            <p class="muted">Last updated: 29 October 2026.</p>
        </article>
    </div>
</section>
