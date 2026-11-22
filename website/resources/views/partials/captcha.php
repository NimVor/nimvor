<?php
/**
 * The check in front of a public form. Not shown to people who are signed in or when the site has it switched off.
 *
 * @var \NimVorSite\Core\View $v
 * @var \NimVorSite\Services\Captcha $captcha
 * @var array<string, string> $errors
 */
$errors ??= [];
if (!$captcha->enabled() || !empty($user)) {
    return;
}
?>
<?php if ($captcha->provider() === 'builtin'): ?>
    <?php $c = $captcha->challenge(); ?>
    <div class="field captcha"><label for="captcha_answer">Are you a person? <?= e($c['question']) ?></label>
        <input type="hidden" name="captcha_token" value="<?= e($c['token']) ?>">
        <input class="input captcha-input" id="captcha_answer" name="captcha_answer" inputmode="numeric" pattern="[0-9]*" maxlength="3" autocomplete="off" required>
        <?= $v->error($errors, 'captcha') ?></div>
<?php else: ?>
    <div class="field captcha">
        <div class="<?= $captcha->provider() === 'turnstile' ? 'cf-turnstile' : 'h-captcha' ?>" data-sitekey="<?= e($captcha->siteKey()) ?>"></div>
        <script src="<?= e($captcha->script()) ?>" async defer></script>
        <?= $v->error($errors, 'captcha') ?></div>
<?php endif; ?>
