<?php
/**
 * Alerts and attention, in two tabs: the alert center (open alerts, acknowledge and resolve) and what needs attention (websites with
 * something to fix, certificates close to expiry). Each tab is shown to those who may see it.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
use NimVor\Security\Permissions as P;

$tabs = [];
if ($this->can(P::MONITORING_VIEW)) { $tabs['alerts'] = 'Alerts'; }
if ($this->can(P::WEBSITES_VIEW)) { $tabs['attention'] = 'Needs attention'; }
$unavailable = '<div class="text-body-secondary">Not available right now. The other tab still shows.</div>';
?>
<?php if ($tabs === []): ?>
    <div class="text-body-secondary">Nothing to show for your account.</div>
<?php else: ?>
<div data-nim-tabs="alerts-attention">
    <?= $this->partial('dashboard/tabs', ['tabs' => $tabs, 'label' => 'Alerts and attention']) ?>
    <?php $first = array_key_first($tabs); ?>
    <?php if (isset($tabs['alerts'])): ?>
        <div data-nim-pane="alerts"<?= $first === 'alerts' ? '' : ' hidden' ?>>
            <?= $d['alerts'] === null ? $unavailable : $this->partial('dashboard/widgets/alerts_list', ['d' => $d['alerts']]) ?>
        </div>
    <?php endif; ?>
    <?php if (isset($tabs['attention'])): ?>
        <div data-nim-pane="attention"<?= $first === 'attention' ? '' : ' hidden' ?>>
            <?= $d['attention'] === null || ($d['attention']['_state'] ?? '') === 'unavailable' ? $unavailable : $this->partial('dashboard/widgets/attention', ['d' => $d['attention']]) ?>
        </div>
    <?php endif; ?>
</div>
<?php endif; ?>
