<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, list<string>> $coverage permission => pages it opens
 * @var list<string> $own the editor's own permissions
 * @var list<string> $ownScopeAllowed
 * @var list<string> $missingDefaults
 */
$isNew = $role === null;
$system = !$isNew && (int) $role['is_system'] === 1;
$isAdminRole = $system && $role['name'] === 'admin';
$canManage = $this->can(\WhaCP\Security\Permissions::ROLES_MANAGE);
$readonly = $isAdminRole || !$canManage;
$ownScope = !$isNew && ($role['scope'] ?? 'all') === 'own';
$checked = array_flip((array) ($old['permissions'] ?? $granted));
$ownSet = array_flip($own);
$ownScopeSet = array_flip($ownScopeAllowed);
$groups = [];
foreach ($permissions as $p) {
    $groups[$p['grp']][] = $p;
}
?>
<?php $this->start(); ?><?= $isNew ? 'New role' : e($role['label']) ?><?php $this->end('title'); ?>
<?php $this->start(); ?><?= $isNew ? 'Choose what members of the role may do' : e((string) $role['description']) ?><?php $this->end('subtitle'); ?>
<?php if ($system && !$isAdminRole && $canManage): ?>
<?php $this->start(); ?>
    <form method="post" action="<?= e($this->url('roles.reset', ['id' => (int) $role['id']])) ?>" data-wcp-confirm="Give &quot;<?= e($role['label']) ?>&quot; WhaCP's default permissions again?">
        <?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary" type="submit"><i class="bi bi-arrow-counterclockwise me-1"></i>Reset to WhaCP defaults</button>
    </form>
<?php $this->end('actions'); ?>
<?php endif; ?>

<?php if ($isAdminRole): ?>
    <div class="alert alert-info small"><i class="bi bi-lock me-1"></i>Administrator always has every permission, so the server can never be left without a full administrator. For narrower access, change Operator or create a custom role.</div>
<?php elseif ($system): ?>
    <div class="alert alert-secondary small"><i class="bi bi-info-circle me-1"></i>A built-in role. You can change its permissions; your choice is kept when WhaCP is updated. <?= !empty($role['customized']) ? '<strong>Changed from the defaults.</strong>' : 'It has WhaCP\'s defaults.' ?></div>
    <?php if ($missingDefaults !== []): ?>
        <div class="alert alert-warning small"><i class="bi bi-exclamation-triangle me-1"></i>WhaCP's defaults for this role also include: <?= e(implode(', ', $missingDefaults)) ?>. Tick them below or reset the role if members need them.</div>
    <?php endif; ?>
<?php endif; ?>
<?php if ($ownScope): ?>
    <div class="alert alert-secondary small"><i class="bi bi-person-lock me-1"></i>Members only see their own websites (and, for resellers, their customers'). Server-wide permissions cannot be given to this role.</div>
<?php endif; ?>
<form method="post" action="<?= e($isNew ? $this->url('roles.store') : $this->url('roles.update', ['id' => (int) $role['id']])) ?>" data-wcp-busy novalidate>
    <?= $this->csrfField() ?>
    <fieldset<?= $readonly ? ' disabled' : '' ?>>
    <div class="row g-3">
        <div class="col-lg-4">
            <div class="card"><div class="card-body">
                <?php if ($system): ?>
                    <div class="mb-3"><label class="form-label">Name</label><input class="form-control" value="<?= e($role['label']) ?> (<?= e($role['name']) ?>)" disabled></div>
                <?php elseif ($isNew): ?>
                    <?= $this->partial('partials/field', ['name' => 'name', 'label' => 'Name (identifier)', 'type' => 'text', 'value' => (string) ($old['name'] ?? ''), 'help' => 'e.g. support_staff', 'attrs' => ['required' => 'required', 'maxlength' => '32']]) ?>
                <?php else: ?>
                    <div class="mb-3"><label class="form-label">Name</label><input class="form-control" value="<?= e($role['name']) ?>" disabled></div>
                <?php endif; ?>
                <?php if (!$system): ?>
                <?= $this->partial('partials/field', ['name' => 'label', 'label' => 'Label', 'type' => 'text', 'value' => (string) ($old['label'] ?? $role['label'] ?? ''), 'attrs' => ['required' => 'required', 'maxlength' => '60']]) ?>
                <?= $this->partial('partials/field', ['name' => 'description', 'label' => 'Description', 'type' => 'text', 'value' => (string) ($old['description'] ?? $role['description'] ?? ''), 'attrs' => ['maxlength' => '255']]) ?>
                <?php endif; ?>
                <?php if (!$readonly): ?>
                    <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= $isNew ? 'Create role' : 'Save role' ?></button>
                <?php endif; ?>
            </div></div>
        </div>
        <div class="col-lg-8">
            <div class="card">
                <div class="card-header d-flex flex-wrap gap-2 justify-content-between align-items-center"><span>Permissions</span>
                    <input type="search" class="form-control form-control-sm wcp-perm-search" placeholder="Find a permission or page…" aria-label="Find a permission or page" data-wcp-filter-list="#perm-list"></div>
                <div class="card-body" id="perm-list">
                    <?php if (isset($errors['permissions'])): ?><div class="alert alert-danger small"><?= e($errors['permissions']) ?></div><?php endif; ?>
                    <?php foreach ($groups as $group => $items): $gid = 'perm-grp-' . preg_replace('/[^a-z0-9]+/', '-', strtolower((string) $group)); ?>
                        <section class="mb-3" id="<?= e($gid) ?>" data-wcp-filter-group>
                            <div class="d-flex align-items-center gap-2 border-bottom pb-1 mb-2">
                                <span class="fw-semibold small text-body-secondary text-uppercase"><?= e($group) ?></span>
                                <?php if (!$readonly): ?>
                                <span class="ms-auto small">
                                    <button type="button" class="btn btn-link btn-sm p-0" data-wcp-check-all="#<?= e($gid) ?>">All</button> ·
                                    <button type="button" class="btn btn-link btn-sm p-0" data-wcp-check-none="#<?= e($gid) ?>">None</button>
                                </span>
                                <?php endif; ?>
                            </div>
                            <?php foreach ($items as $p):
                                $name = (string) $p['name'];
                                $on = isset($checked[$name]);
                                $why = '';
                                if ($ownScope && !isset($ownScopeSet[$name]) && !str_starts_with($name, 'plugin.')) {
                                    $why = 'Server-wide: not for resellers or hosting customers';
                                } elseif (!isset($ownSet[$name])) {
                                    $why = 'You do not have this permission yourself';
                                }
                                $lock = !$on && $why !== '';
                                $pages = $coverage[$name] ?? [];
                            ?>
                                <div class="form-check mb-2" data-wcp-filter-item>
                                    <input class="form-check-input" type="checkbox" name="permissions[]" value="<?= e($name) ?>" id="perm-<?= e($name) ?>"<?= $on ? ' checked' : '' ?><?= $lock ? ' disabled' : '' ?>>
                                    <label class="form-check-label small" for="perm-<?= e($name) ?>"><?= e($p['label']) ?> <code class="text-body-secondary"><?= e($name) ?></code>
                                        <?php if ($lock): ?><span class="badge text-bg-light border ms-1 wcp-badge-wrap" title="<?= e($why) ?>"><i class="bi bi-lock"></i> <?= e($why) ?></span><?php endif; ?>
                                    </label>
                                    <?php if ($pages !== []): ?><div class="small text-body-secondary wcp-perm-pages">Opens: <?= e(implode(' · ', $pages)) ?></div><?php endif; ?>
                                </div>
                            <?php endforeach; ?>
                        </section>
                    <?php endforeach; ?>
                    <p class="small text-body-secondary mb-0" data-wcp-filter-empty hidden>No permission matches.</p>
                </div>
            </div>
        </div>
    </div>
    </fieldset>
</form>

<?php if (!$isNew && $this->can(\WhaCP\Security\Permissions::ROLES_MANAGE)): ?>
<div class="card mt-3" id="two-factor-policy">
    <div class="card-header"><i class="bi bi-phone me-1"></i>Sign-in policy</div>
    <div class="card-body">
        <form method="post" action="<?= e($this->url('roles.2fa', ['id' => (int) $role['id']])) ?>" class="d-flex flex-wrap align-items-center gap-3">
            <?= $this->csrfField() ?>
            <div class="form-check mb-0">
                <input class="form-check-input" type="checkbox" name="require_two_factor" value="1" id="req-2fa"<?= !empty($role['require_two_factor']) ? ' checked' : '' ?>>
                <label class="form-check-label" for="req-2fa">Require two-factor sign-in for members of this role</label>
            </div>
            <button class="btn btn-sm btn-primary">Save</button>
        </form>
        <p class="small text-body-secondary mt-2 mb-0">Members who have not set it up can only reach their profile until they do. API tokens are not affected.</p>
    </div>
</div>
<?php endif; ?>
