<?php
/**
 * The variables editor shared by the Environment page and the Websites list window.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var string $text
 * @var string $fieldId
 */
use NimVor\Services\SiteEnvService;

$errors ??= [];
?>
<label class="form-label fw-medium" for="<?= e($fieldId) ?>">Variables <span class="text-body-secondary fw-normal">(one <code>NAME=value</code> per line)</span></label>
<textarea class="form-control form-control-sm font-monospace<?= isset($errors['env']) ? ' is-invalid' : '' ?>" id="<?= e($fieldId) ?>" name="env" rows="10" spellcheck="false" autocomplete="off" autocapitalize="off" placeholder="APP_ENV=production&#10;STRIPE_SECRET_KEY=sk_live_..."><?= e($text) ?></textarea>
<?php if (isset($errors['env'])): ?><div class="invalid-feedback d-block"><?= e((string) $errors['env']) ?></div><?php endif; ?>
<div class="form-text">
    <?php if (($site['kind'] ?? '') === 'php'): ?>PHP reads them with <code>getenv('NAME')</code> or <code>$_SERVER['NAME']</code>.<?php else: ?>Background workers of this website receive them.<?php endif; ?>
    Names: capital letters, digits and <code>_</code> (up to 64 characters); up to <?= (int) SiteEnvService::MAX_VARS ?> variables of <?= number_format(SiteEnvService::MAX_VALUE) ?> characters. Lines starting with <code>#</code> are ignored. A value may not contain a single quote or a line break. Values are stored encrypted and never written to logs; the server keeps them in a file only the system can read. Changing them restarts this website's PHP processes<?= ($site['kind'] ?? '') === 'php' ? '' : ' and background workers' ?>.
</div>
