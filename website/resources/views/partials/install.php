<?php /** @var \NimVorSite\Core\View $v */ /** @var string $command */ ?>
<div class="install">
    <span class="prompt" aria-hidden="true">$</span>
    <code><?= e($command) ?></code>
    <button class="copy" type="button" data-copy="<?= e($command) ?>" aria-label="Copy the install command"><?= $v->icon('copy', 15) ?><span class="label">Copy</span></button>
</div>
