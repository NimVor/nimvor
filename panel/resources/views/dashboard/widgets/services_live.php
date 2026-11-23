<?php
/**
 * The Services card: the rows are drawn by the live poll (public/assets/js/dashboard.js); start, stop and restart show
 * only for those who may manage services.
 *
 * @var \NimVor\View\View $this
 */
?>
<ul class="list-group list-group-flush nim-flush" data-services<?php if ($this->can(\NimVor\Security\Permissions::SERVICES_MANAGE)): ?> data-services-control="<?= e($this->url('services.control')) ?>"<?php endif; ?>>
    <li class="list-group-item text-body-secondary"><span class="placeholder col-8"></span></li>
    <li class="list-group-item text-body-secondary"><span class="placeholder col-6"></span></li>
    <li class="list-group-item text-body-secondary"><span class="placeholder col-7"></span></li>
</ul>
