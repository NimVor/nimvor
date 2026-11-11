<?php
/** @var \NimVor\View\View $this */
/** The notification bell, dark-mode button and account menu every theme shows in its top bar. */
$unreadCount = isset($notification_count) && is_callable($notification_count) ? $notification_count() : null;
$who = !empty($user) ? ($user['display_name'] !== '' ? $user['display_name'] : $user['username']) : '';
?>
<div class="nt-actions">
    <?php if ($unreadCount !== null): ?>
        <div class="dropdown">
            <a class="btn btn-sm nt-iconbtn position-relative" href="<?= e($this->url('notifications')) ?>" role="button" data-bs-toggle="dropdown" data-bs-auto-close="outside" aria-expanded="false" data-nim-bell data-nim-bell-url="<?= e($this->url('notifications.summary')) ?>" data-nim-bell-read="<?= e($this->url('notifications.read')) ?>" aria-label="Notifications<?= $unreadCount > 0 ? ' (' . (int) $unreadCount . ' unread)' : '' ?>">
                <i class="bi bi-bell" aria-hidden="true"></i>
                <span class="position-absolute top-0 start-100 translate-middle badge rounded-pill text-bg-danger<?= $unreadCount > 0 ? '' : ' d-none' ?>" data-nim-bell-count><?= $unreadCount > 99 ? '99+' : (int) $unreadCount ?></span>
            </a>
            <div class="dropdown-menu dropdown-menu-end p-0 nim-bell-menu" data-nim-bell-menu>
                <div class="d-flex align-items-center justify-content-between px-3 py-2 border-bottom">
                    <span class="fw-semibold">Notifications</span>
                    <button class="btn btn-sm btn-link p-0" type="button" data-nim-bell-all>Mark all read</button>
                </div>
                <div class="nim-bell-list" data-nim-bell-list><div class="px-3 py-3 small text-body-secondary">Loading…</div></div>
                <a class="d-block text-center small px-3 py-2 border-top" href="<?= e($this->url('notifications')) ?>">View all notifications</a>
            </div>
        </div>
    <?php endif; ?>
    <button class="btn btn-sm nt-iconbtn" type="button" data-nim-theme-toggle aria-label="Switch between light and dark"><i class="bi bi-circle-half" aria-hidden="true"></i></button>
    <?php if (!empty($user)): ?>
        <div class="dropdown">
            <button class="btn btn-sm nt-iconbtn nt-userbtn dropdown-toggle" data-bs-toggle="dropdown" aria-expanded="false" aria-label="Account: <?= e($who) ?>">
                <i class="bi bi-person-circle" aria-hidden="true"></i><span class="d-none d-md-inline ms-1"><?= e($who) ?></span>
            </button>
            <ul class="dropdown-menu dropdown-menu-end">
                <li><span class="dropdown-item-text small text-body-secondary"><?= e($user['role_label']) ?></span></li>
                <li><a class="dropdown-item" href="<?= e($this->url('profile')) ?>"><i class="bi bi-person me-2" aria-hidden="true"></i>Profile</a></li>
                <li><a class="dropdown-item" href="<?= e($this->url('appearance')) ?>"><i class="bi bi-palette me-2" aria-hidden="true"></i>Appearance</a></li>
                <li><hr class="dropdown-divider"></li>
                <li>
                    <form method="post" action="<?= e($this->url('logout')) ?>" data-nim-noajax>
                        <?= $this->csrfField() ?>
                        <button class="dropdown-item" type="submit"><i class="bi bi-box-arrow-right me-2" aria-hidden="true"></i>Sign out</button>
                    </form>
                </li>
            </ul>
        </div>
    <?php endif; ?>
</div>
