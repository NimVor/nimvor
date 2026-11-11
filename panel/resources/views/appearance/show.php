<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $profile */
/** @var list<string> $enabled */
/** @var array<string, array{0: string, 1: string}> $accents */
use NimVor\View\Themes;

$current = $this->theme();
$all = Themes::all();
$own = (string) ($profile['ui_theme'] ?? '');
$mode = (string) ($profile['ui_mode'] ?? '');
$accent = (string) ($profile['ui_accent'] ?? '');
$errors = (array) ($this->shared('errors') ?? []);
?>
<?php $this->start(); ?>Appearance<?php $this->end('title'); ?>
<?php $this->start(); ?>How the panel looks for you: theme, light or dark, and accent colour<?php $this->end('subtitle'); ?>

<form method="post" action="<?= e($this->url('appearance.save')) ?>" data-nim-noajax data-nim-appearance-form>
    <?= $this->csrfField() ?>

    <?php if ($isCustomer): ?>
    <section class="card mb-3" aria-labelledby="ap-theme">
        <div class="card-header" id="ap-theme"><i class="bi bi-layout-text-window me-1" aria-hidden="true"></i>Theme</div>
        <div class="card-body">
            <?php if (!$customersChoose): ?>
                <p class="small text-body-secondary mb-3">Your hosting provider uses the <strong><?= e($all[$defaultTheme]['label']) ?></strong> theme for everyone.</p>
            <?php endif; ?>
            <?php if (isset($errors['theme'])): ?><div class="alert alert-danger py-2 small"><?= e($errors['theme']) ?></div><?php endif; ?>
            <div class="nim-theme-grid" role="radiogroup" aria-labelledby="ap-theme">
                <?php foreach ($all as $key => $t): if (!in_array($key, $enabled, true)) { continue; } $isCurrent = $current['key'] === $key && !$current['preview']; ?>
                    <div class="nim-theme-card<?= $isCurrent ? ' is-current' : '' ?>">
                        <?= $this->partial('appearance/thumb', ['theme' => $key]) ?>
                        <div class="nim-theme-card-body">
                            <div class="form-check">
                                <input class="form-check-input" type="radio" name="theme" id="ap-theme-<?= e($key) ?>" value="<?= e($key) ?>"<?= ($own === $key || ($own === '' && $defaultTheme === $key)) ? ' checked' : '' ?><?= $customersChoose ? '' : ' disabled' ?>>
                                <label class="form-check-label fw-semibold" for="ap-theme-<?= e($key) ?>"><?= e($t['label']) ?><?= $key === $defaultTheme ? ' <span class="badge text-bg-light border ms-1">default</span>' : '' ?></label>
                            </div>
                            <div class="small fw-medium"><?= e($t['tagline']) ?></div>
                            <p class="small text-body-secondary mb-2"><?= e($t['about']) ?></p>
                            <?php if ($customersChoose && !$isCurrent): ?>
                                <button class="btn btn-sm btn-outline-secondary" type="submit" formaction="<?= e($this->url('appearance.preview')) ?>" name="theme" value="<?= e($key) ?>" formnovalidate><i class="bi bi-eye me-1" aria-hidden="true"></i>Preview</button>
                            <?php elseif ($isCurrent): ?><span class="small text-success"><i class="bi bi-check-circle-fill me-1" aria-hidden="true"></i>In use</span><?php endif; ?>
                        </div>
                    </div>
                <?php endforeach; ?>
            </div>
        </div>
    </section>
    <?php else: ?>
        <div class="alert alert-info small"><i class="bi bi-info-circle me-1" aria-hidden="true"></i>Themes change the hosting customers' panel. The server administration pages keep their own layout; light or dark and the accent colour apply to them.</div>
    <?php endif; ?>

    <div class="row g-3">
        <section class="col-lg-6" aria-labelledby="ap-mode">
            <div class="card h-100">
                <div class="card-header" id="ap-mode"><i class="bi bi-circle-half me-1" aria-hidden="true"></i>Light or dark</div>
                <div class="card-body">
                    <?php if (isset($errors['mode'])): ?><div class="alert alert-danger py-2 small"><?= e($errors['mode']) ?></div><?php endif; ?>
                    <?php foreach (Themes::modes() as $k => $label): ?>
                        <div class="form-check mb-1">
                            <input class="form-check-input" type="radio" name="mode" id="ap-mode-<?= e($k) ?>" value="<?= e($k) ?>"<?= $current['mode'] === $k ? ' checked' : '' ?>>
                            <label class="form-check-label" for="ap-mode-<?= e($k) ?>"><?= e($label) ?></label>
                        </div>
                    <?php endforeach; ?>
                    <p class="small text-body-secondary mt-2 mb-0">The moon button in the top bar still switches this browser at any time.</p>
                </div>
            </div>
        </section>
        <section class="col-lg-6" aria-labelledby="ap-accent">
            <div class="card h-100">
                <div class="card-header" id="ap-accent"><i class="bi bi-palette me-1" aria-hidden="true"></i>Accent colour</div>
                <div class="card-body">
                    <?php if (isset($errors['accent'])): ?><div class="alert alert-danger py-2 small"><?= e($errors['accent']) ?></div><?php endif; ?>
                    <div class="d-flex flex-wrap gap-2" role="radiogroup" aria-labelledby="ap-accent">
                        <label class="nim-accent-choice">
                            <input type="radio" name="accent" value=""<?= $accent === '' ? ' checked' : '' ?>>
                            <span class="nim-accent-dot nim-accent-auto" aria-hidden="true"></span><span>Theme's own</span>
                        </label>
                        <?php foreach ($accents as $k => [$label]): ?>
                            <label class="nim-accent-choice">
                                <input type="radio" name="accent" value="<?= e($k) ?>"<?= $accent === $k ? ' checked' : '' ?>>
                                <span class="nim-accent-dot nim-accent-<?= e($k) ?>" aria-hidden="true"></span><span><?= e($label) ?></span>
                            </label>
                        <?php endforeach; ?>
                    </div>
                </div>
            </div>
        </section>
    </div>

    <div class="d-flex flex-wrap gap-2 mt-3">
        <button class="btn btn-primary" type="submit"><i class="bi bi-check-lg me-1" aria-hidden="true"></i>Save</button>
        <button class="btn btn-outline-secondary" type="submit" formaction="<?= e($this->url('appearance.reset')) ?>" formnovalidate data-nim-confirm-submit="Restore the default appearance?"><i class="bi bi-arrow-counterclockwise me-1" aria-hidden="true"></i>Restore default</button>
    </div>
</form>
