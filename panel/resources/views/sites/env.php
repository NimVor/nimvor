<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var string $text
 */
$id = (int) $site['id'];
$old ??= [];
$errors ??= [];
if ($errors !== [] && isset($old['env'])) {
    $text = (string) $old['env'];
}
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'env']) ?>

<p class="small text-body-secondary">Keys and values this website can read from its environment, such as API keys and database settings, without putting them in its files.</p>

<form method="post" action="<?= e($this->url('sites.env.update', ['id' => $id])) ?>" data-nim-busy novalidate>
    <?= $this->csrfField() ?>
    <div class="card">
        <div class="card-header"><h2 class="nim-toolbar-title"><i class="bi bi-braces me-1"></i>Environment variables</h2></div>
        <div class="card-body">
            <?= $this->partial('sites/env_form', ['site' => $site, 'text' => $text, 'fieldId' => 'env-text', 'errors' => $errors]) ?>
        </div>
        <div class="card-footer text-end"><button class="btn btn-primary" type="submit">Save and apply</button></div>
    </div>
</form>
