<?php
/**
 * Global search (Ctrl+K): every area the viewer may open, and quick
 * actions. nimvor.js filters the list as one types; Enter opens the first.
 *
 * @var \NimVor\View\View $this
 */
use NimVor\Security\Permissions as P;
use NimVor\View\Navigation;

$quick = [];
foreach ([
    ['Create website', 'plus-circle', 'sites.create', P::WEBSITES_MANAGE, 'New website with PHP, SSL and DNS'],
    ['Create hosting account', 'person-plus', 'accounts.create', P::USERS_MANAGE, 'Customer with plan, website and email'],
    ['Create panel user', 'person-add', 'users.create', P::USERS_MANAGE, 'Administrator, operator or reseller'],
    ['My profile', 'person-circle', 'profile', P::DASHBOARD_VIEW, 'Password, two-factor sign-in, API tokens'],
] as [$label, $icon, $route, $perm, $desc]) {
    if ($this->hasRoute($route) && $this->can($perm)) {
        $quick[] = ['label' => $label, 'icon' => $icon, 'url' => $this->url($route), 'description' => $desc];
    }
}
$groups = ['Quick actions' => $quick] + Navigation::visible($this);
?>
<div class="modal fade" id="nim-palette" tabindex="-1" aria-label="Search tools" aria-hidden="true" data-nim-palette>
    <div class="modal-dialog modal-dialog-scrollable nim-palette-dialog">
        <div class="modal-content">
            <div class="nim-palette-head">
                <i class="bi bi-search" aria-hidden="true"></i>
                <input class="form-control" type="search" placeholder="Search tools and actions…" aria-label="Search tools and actions" autocomplete="off" data-nim-palette-input>
                <kbd>Esc</kbd>
            </div>
            <div class="modal-body p-2" data-nim-palette-list>
                <?php foreach ($groups as $group => $items): if ($items === []) { continue; } ?>
                    <div class="nim-palette-group" data-nim-palette-group><?= e($group) ?></div>
                    <?php foreach ($items as $i): ?>
                        <?php foreach ($i['children'] ?? [] as $ch): ?>
                            <a class="nim-palette-item" href="<?= e($ch['url']) ?>" data-nim-palette-item data-search="<?= e(mb_strtolower($i['label'] . ' ' . $ch['label'])) ?>">
                                <span class="nim-palette-icon"><i class="bi bi-<?= e($i['icon']) ?>" aria-hidden="true"></i></span>
                                <span class="min-w-0"><span class="d-block fw-medium"><?= e($ch['label']) ?></span><span class="d-block small text-body-secondary text-truncate"><?= e($i['label']) ?></span></span>
                            </a>
                        <?php endforeach; ?>
                        <a class="nim-palette-item" href="<?= e($i['url']) ?>" data-nim-palette-item data-search="<?= e(mb_strtolower($i['label'] . ' ' . $i['description'] . ' ' . $group)) ?>">
                            <span class="nim-palette-icon"><i class="bi bi-<?= e($i['icon']) ?>" aria-hidden="true"></i></span>
                            <span class="min-w-0"><span class="d-block fw-medium"><?= e($i['label']) ?></span><span class="d-block small text-body-secondary text-truncate"><?= e($i['description']) ?></span></span>
                        </a>
                    <?php endforeach; ?>
                <?php endforeach; ?>
                <div class="text-center text-body-secondary small py-4 d-none" data-nim-palette-empty>Nothing matches.</div>
            </div>
        </div>
    </div>
</div>
