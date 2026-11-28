<?php
/**
 * Software: one tile per part of the server, its state as a ribbon. On hover a running part offers stop and restart, a stopped one start, one that is not
 * installed is not shown at all. The buttons are handled by public/assets/js/dashboard.js.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
use NimVor\Http\Controllers\ComponentsController;
use NimVor\Security\Permissions as P;

$icons = ['web' => 'globe2', 'php' => 'filetype-php', 'database' => 'database', 'mail' => 'envelope', 'dns' => 'diagram-3', 'ftp' => 'folder-symlink', 'redis' => 'lightning-charge', 'memcached' => 'lightning', 'mongodb' => 'database-gear', 'firewall' => 'shield-lock', 'fail2ban' => 'shield-exclamation', 'clamav' => 'bug', 'certbot' => 'lock'];
$canControl = $this->can(P::SERVICES_MANAGE);
// Parts whose details show in a card while the pointer rests on the tile (DashboardController::popover), by software id.
$details = ['dns' => 'dns', 'mail' => 'mail', 'redis' => 'redis', 'memcached' => 'memcached', 'web' => 'web', 'certbot' => 'ssl', 'php' => 'php', 'database' => 'database', 'firewall' => 'firewall', 'fail2ban' => 'fail2ban', 'clamav' => 'clamav'];
// Only what is installed is a tile: what is missing is installed from the Components page, not offered on the dashboard.
$installed = array_values(array_filter((array) $d['items'], static fn (array $c): bool => !empty($c['installed'])));
?>
<div class="nim-software" data-software<?= $canControl ? ' data-services-control="' . e($this->url('services.control')) . '"' : '' ?>>
<?php if ($installed === []): ?><div class="text-body-secondary">No software is installed yet. Choose what this server should run on the <?= $this->hasRoute('components') && $this->routeAllowed('components') ? '<a href="' . e($this->url('components')) . '">Components</a>' : 'Components' ?> page.</div><?php endif; ?>
<?php foreach ($installed as $c):
    $page = ComponentsController::PAGES[$c['id']] ?? null;
    $state = !$c['installed'] ? 'off' : ($c['running'] === false ? 'stopped' : 'ok');
    $unit = (string) ($c['unit'] ?? '');
    $ops = [];
    if ($c['installed'] && $canControl && $unit !== '') {
        $ops = $c['running'] === false ? [['start', 'Start', 'play-fill']] : [['restart', 'Restart', 'arrow-clockwise'], ['stop', 'Stop', 'stop-fill']];
    } ?>
    <?php $card = ($c['installed'] && isset($details[$c['id']]) && $this->can(\NimVor\Http\Controllers\DashboardController::TILE_DETAILS[$details[$c['id']]])) ? $this->url('dashboard.popover', ['key' => $details[$c['id']]]) : null; ?>
    <div class="nim-software-tile is-<?= e($state) ?>"<?= $card !== null ? ' data-popover-url="' . e($card) . '" tabindex="0"' : '' ?>>
        <span class="nim-software-ribbon"><?= ['ok' => 'Running', 'stopped' => 'Stopped', 'off' => 'Not installed'][$state] ?></span>
        <?php $logo = $this->logo((string) $c['id'], 36, 'nim-logo-tile nim-software-logo'); ?><?= $logo !== '' ? $logo : '<i class="bi bi-' . e($icons[$c['id']] ?? 'box') . ' nim-software-icon" aria-hidden="true"></i>' ?>
        <div class="nim-software-name"><?= e($c['name']) ?></div>
        <div class="nim-software-ops">
            <?php foreach ($ops as [$op, $label, $icon]): ?><button type="button" class="btn btn-sm btn-outline-secondary" data-software-op="<?= e($op) ?>" data-unit="<?= e($unit) ?>" title="<?= e($label . ' ' . $c['name']) ?>" aria-label="<?= e($label . ' ' . $c['name']) ?>"><i class="bi bi-<?= e($icon) ?>" aria-hidden="true"></i></button><?php endforeach; ?>
            <?php if ($c['id'] === 'database' && $c['installed'] && $this->hasRoute('databases.server.settings_modal') && $this->routeAllowed('databases.server.settings_modal')): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('databases.server.settings_modal')) ?>" data-nim-site-modal data-nim-modal-xl title="Settings of <?= e($c['name']) ?>"><i class="bi bi-gear" aria-hidden="true"></i><span class="visually-hidden">Settings</span></a>
            <?php elseif ($c['installed'] && $page !== null && $this->hasRoute($page) && $this->routeAllowed($page)): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url($page)) ?>" title="Settings of <?= e($c['name']) ?>"><i class="bi bi-gear" aria-hidden="true"></i><span class="visually-hidden">Settings</span></a>
            <?php endif; ?>
        </div>
    </div>
<?php endforeach; ?>
</div>
