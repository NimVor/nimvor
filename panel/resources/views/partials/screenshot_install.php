<?php
/**
 * The bar that lets an administrator install the browser the website pictures need.
 *
 * @var \NimVor\View\View $this
 */
if ($this->routeAllowed('server.screenshots.install')): ?>
    <div class="nim-infobar mb-3">
        <i class="bi bi-image" aria-hidden="true"></i>
        <span class="me-auto">Show a picture of each website's home page: this needs a browser on the server (Google Chrome, about 300 MB, from Google's own repository).</span>
        <form method="post" action="<?= e($this->url('server.screenshots.install')) ?>" data-nim-confirm="Install Google Chrome from Google's repository so the websites can show their home pages?"><?= $this->csrfField() ?><button class="btn btn-sm btn-primary">Install the browser</button></form>
    </div>
<?php endif; ?>
