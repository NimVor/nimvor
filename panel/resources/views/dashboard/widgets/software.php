<?php
/**
 * Software: one tile per part of the server. A running part offers stop and restart, a stopped one start, one that is not
 * installed a link to the Components page. The buttons are handled by public/assets/js/dashboard.js.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
use NimVor\Http\Controllers\ComponentsController;
use NimVor\Security\Permissions as P;

$icons = ['web' => 'globe2', 'php' => 'filetype-php', 'database' => 'database', 'mail' => 'envelope', 'dns' => 'diagram-3', 'ftp' => 'folder-symlink', 'redis' => 'lightning-charge', 'memcached' => 'lightning', 'mongodb' => 'database-gear', 'firewall' => 'shield-lock', 'fail2ban' => 'shield-exclamation', 'clamav' => 'bug', 'certbot' => 'lock'];
$canControl = $this->can(P::SERVICES_MANAGE);
?>
<div class="nim-software" data-software<?= $canControl ? ' data-services-control="' . e($this->url('services.control')) . '"' : '' ?>>
<?php foreach ((array) $d['items'] as $c):
    $page = ComponentsController::PAGES[$c['id']] ?? null;
    $state = !$c['installed'] ? 'off' : ($c['running'] === false ? 'stopped' : 'ok');
    $unit = (string) ($c['unit'] ?? '');
    $ops = [];
    if ($c['installed'] && $canControl && $unit !== '') {
        $ops = $c['running'] === false ? [['start', 'Start']] : [['restart', 'Restart'], ['stop', 'Stop']];
    } ?>
    <div class="nim-software-tile is-<?= e($state) ?>">
        <i class="bi bi-<?= e($icons[$c['id']] ?? 'box') ?> nim-software-icon" aria-hidden="true"></i>
        <div class="nim-software-name"><?= e($c['name']) ?></div>
        <div class="nim-software-state"><?= ['ok' => 'Running', 'stopped' => 'Stopped', 'off' => 'Not installed'][$state] ?></div>
        <div class="nim-software-ops">
            <?php foreach ($ops as [$op, $label]): ?><button type="button" class="btn btn-sm btn-outline-secondary" data-software-op="<?= e($op) ?>" data-unit="<?= e($unit) ?>" title="<?= e($label . ' ' . $c['name']) ?>"><?= e($label) ?></button><?php endforeach; ?>
            <?php if ($c['installed'] && $page !== null && $this->hasRoute($page) && $this->routeAllowed($page)): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url($page)) ?>" title="Settings of <?= e($c['name']) ?>"><i class="bi bi-gear" aria-hidden="true"></i><span class="visually-hidden">Settings</span></a>
            <?php elseif (!$c['installed'] && $this->hasRoute('components') && $this->routeAllowed('components')): ?><a class="btn btn-sm btn-outline-primary" href="<?= e($this->url('components') . '#c-' . $c['id']) ?>">Install</a><?php endif; ?>
        </div>
    </div>
<?php endforeach; ?>
</div>
