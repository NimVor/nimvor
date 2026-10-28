<?php
/** @var \NimVor\View\View $this */
/** @var string $return */
?>
<?php $this->start(); ?>Confirm your password<?php $this->end('title'); ?>
<div class="row justify-content-center">
    <div class="col-md-6 col-lg-5">
        <div class="card">
            <div class="card-body p-4">
                <p class="text-body-secondary small">Changes to accounts, roles, API tokens and sign-in policies need your password, confirmed at most 15 minutes ago.</p>
                <form method="post" action="<?= e($this->url('auth.confirm.submit')) ?>" data-nim-busy novalidate>
                    <?= $this->csrfField() ?><input type="hidden" name="return" value="<?= e($return) ?>">
                    <?= $this->partial('partials/field', ['name' => 'password', 'label' => 'Password', 'type' => 'password', 'value' => '', 'attrs' => ['autocomplete' => 'current-password', 'required' => 'required', 'autofocus' => 'autofocus']]) ?>
                    <button class="btn btn-primary" type="submit">Confirm</button>
                    <a class="btn btn-link" href="<?= e($return) ?>">Cancel</a>
                </form>
            </div>
        </div>
    </div>
</div>
