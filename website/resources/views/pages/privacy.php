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
            <p class="muted">Applies to this website, not to the NimVor software you install on your own server: that software sends nothing to us.</p>
            <h2>What we store</h2>
            <ul>
                <li><strong>Visitors.</strong> We count page visits per day and per page. We do not store your address, a cookie or any other identifier for this, and we use no analytics service.</li>
                <li><strong>Accounts.</strong> If you register: your name, email address, a hash of your password (we never store the password), the time you registered and last signed in.</li>
                <li><strong>Messages and requests.</strong> What you type into the contact and build-service forms, with your name and email, so we can reply.</li>
                <li><strong>Server logs.</strong> The web server keeps ordinary access and error logs (including addresses) for security and fixing faults.</li>
            </ul>
            <h2>Cookies</h2>
            <p>One cookie, <code>nimvor_sid</code>, is set only when you sign in or send a form. It identifies your session, is not readable by scripts and is deleted when you log out or after a few hours. We use no advertising or tracking cookies.</p>
            <h2>How we use it</h2>
            <p>To run your account, to answer you, and to keep the site secure (for example to slow down repeated failed sign-ins, using your address for a short time). We do not sell your data or share it for advertising.</p>
            <h2>Who sees it</h2>
            <p>The people who run the NimVor project, and the hosting provider that runs this site. We share it with others only if the law requires.</p>
            <h2>How long we keep it</h2>
            <p>Account details until you ask us to delete the account. Messages and requests until they are no longer needed to help you. Visit counts, which are not personal, indefinitely.</p>
            <h2>Your choices</h2>
            <p>You can ask to see, correct or delete what we hold about you through the <a href="/support/contact">contact form</a>, or change your name and password under your account.</p>
            <p class="muted">Last updated: 28 October 2026.</p>
        </article>
    </div>
</section>
