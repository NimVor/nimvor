<?php
/**
 * Administration → Application Manager.
 *
 * @var \WhaCP\View\View $this
 * @var list<array<string, mixed>> $categories  each with 'apps'
 * @var bool $installerEnabled
 */
use WhaCP\Security\Permissions as P;

$canManage = $this->can(P::SETTINGS_MANAGE);
$categories ??= [];
$installerEnabled ??= true;
// A small switch form: posts `on` flipped from the current state.
$toggle = function (string $action, array $fields, bool $on, string $label, bool $disabled) {
    ob_start(); ?>
    <form method="post" action="<?= e($action) ?>" data-wcp-busy class="form-check form-switch mb-0">
        <?= $this->csrfField() ?>
        <?php foreach ($fields as $k => $v): ?><input type="hidden" name="<?= e($k) ?>" value="<?= e((string) $v) ?>"><?php endforeach; ?>
        <input type="hidden" name="on" value="<?= $on ? '0' : '1' ?>">
        <input class="form-check-input" type="checkbox" role="switch" onchange="this.form.submit()"<?= $on ? ' checked' : '' ?><?= $disabled ? ' disabled' : '' ?> aria-label="<?= e($label) ?>">
    </form>
    <?php return (string) ob_get_clean();
};
?>
<?php $this->start(); ?>Application Manager<?php $this->end('title'); ?>
<?php $this->start(); ?>The installer's categories and applications, and what customers can install<?php $this->end('subtitle'); ?>

<div class="wcp-infobar mb-3">
    <i class="bi bi-grid-3x3-gap" aria-hidden="true"></i>
    <span class="me-auto">The application installer is <strong><?= $installerEnabled ? 'on' : 'off' ?></strong> for customers.</span>
    <?php if ($canManage): ?>
        <?= $toggle($this->url('apps.manager.installer'), [], $installerEnabled, 'Application installer', false) ?>
    <?php endif; ?>
</div>

<?php foreach ($categories as $cat): $catOn = (int) $cat['enabled'] === 1; ?>
<div class="card mb-3">
    <div class="card-header d-flex flex-wrap gap-2 align-items-center">
        <i class="bi <?= e((string) $cat['icon']) ?>" aria-hidden="true"></i>
        <span class="fw-medium"><?= e((string) $cat['name']) ?></span>
        <span class="badge rounded-pill text-bg-light border"><?= count($cat['apps']) ?></span>
        <?php if (!$catOn): ?><span class="wcp-state is-warn">category disabled</span><?php endif; ?>
        <div class="ms-auto d-flex align-items-center gap-2">
            <span class="small text-body-secondary">Category</span>
            <?php if ($canManage): ?><?= $toggle($this->url('apps.manager.category'), ['slug' => $cat['slug']], $catOn, 'Category enabled', false) ?><?php endif; ?>
        </div>
    </div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle mb-0">
            <thead><tr><th>Application</th><th class="text-center">Enabled</th><th class="text-center">Visible to customers</th></tr></thead>
            <tbody>
            <?php foreach ($cat['apps'] as $app): $en = (int) $app['enabled'] === 1; $vis = (int) $app['customer_visible'] === 1; ?>
                <tr<?= $catOn ? '' : ' class="opacity-50"' ?>>
                    <td>
                        <i class="bi <?= e((string) $app['icon']) ?> me-1" aria-hidden="true"></i><span class="fw-medium"><?= e((string) $app['name']) ?></span>
                        <div class="small text-body-secondary"><?= e((string) $app['description']) ?></div>
                    </td>
                    <td class="text-center"><?php if ($canManage): ?><div class="d-inline-block"><?= $toggle($this->url('apps.manager.app'), ['slug' => $app['slug'], 'field' => 'enabled'], $en, 'Enabled', false) ?></div><?php else: ?><?= $en ? 'Yes' : 'No' ?><?php endif; ?></td>
                    <td class="text-center"><?php if ($canManage): ?><div class="d-inline-block"><?= $toggle($this->url('apps.manager.app'), ['slug' => $app['slug'], 'field' => 'customer_visible'], $vis, 'Visible to customers', !$en) ?></div><?php else: ?><?= $vis ? 'Yes' : 'No' ?><?php endif; ?></td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
</div>
<?php endforeach; ?>

<p class="small text-body-secondary">Turning off an application hides it from the installer for everyone. Keeping it enabled but not visible to customers lets administrators still install it. Disabling a whole category hides all its applications.</p>
