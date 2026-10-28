<?php
/** @var \NimVorSite\Core\View $v */
/** @var string $email */
$v->layout('layouts/plain');
$v->set('title', 'Setup finished');
?>
<h1>The site is set up</h1>
<div class="alert alert-success">The tables were created and <?= e($email) ?> is the administrator.</div>
<ul class="checks">
    <li><?= $v->icon('check', 18) ?><span>The setup key file was deleted and this page is now locked.</span></li>
    <li><?= $v->icon('check', 18) ?><span>Your settings were saved in <code>config/local.php</code>, which is outside the public folder. Keep it private.</span></li>
</ul>
<p class="mt-3"><a class="btn btn-primary" href="/login">Log in</a></p>
