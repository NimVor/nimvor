<?php
/**
 * The hover card of a Software tile: the details of that part of the server (they used to be dashboard widgets of their own)
 * and links to the pages that manage it.
 *
 * @var \NimVor\View\View $this
 * @var string $key dns, mail, redis, memcached, web, php, database, firewall, fail2ban, clamav or ssl
 * @var array<string, mixed> $d
 */
$titles = ['dns' => 'DNS server', 'mail' => 'Email', 'redis' => 'Redis', 'memcached' => 'Memcached', 'web' => 'Web server', 'ssl' => "Let's Encrypt certificates", 'php' => 'PHP-FPM', 'database' => 'Database server', 'firewall' => 'Firewall', 'fail2ban' => 'fail2ban', 'clamav' => 'Malware scanner'];
$links = [
    'dns' => [['dns', 'DNS zones', 'diagram-3']],
    'mail' => [['mail.admin.server', 'Mail server', 'envelope'], ['mail.admin.queue', 'Mail queue', 'inbox'], ['mail.admin.reports', 'Delivery reports', 'bar-chart']],
    'redis' => [['redis', 'Redis', 'lightning-charge']],
    'memcached' => [['memcached', 'Memcached', 'lightning']],
    'web' => [['nginx', 'Nginx', 'hdd-network'], ['php', 'PHP versions', 'filetype-php'], ['websites', 'Websites', 'globe2']],
    'ssl' => [['ssl.hosts', 'Certificates', 'lock'], ['ssl.autossl', 'AutoSSL', 'arrow-repeat']],
    'php' => [['php', 'PHP versions', 'filetype-php'], ['websites', 'Websites', 'globe2']],
    'database' => [['databases.server', 'Database server', 'database'], ['databases', 'Databases and users', 'table'], ['databases.server.connections', 'Connections', 'plug']],
    'firewall' => [['security.firewall', 'Firewall', 'shield-lock']],
    'fail2ban' => [['security.fail2ban', 'fail2ban', 'shield-exclamation']],
    'clamav' => [['security.malware', 'Malware scanner', 'bug']],
];
?>
<div class="nim-pop-title mb-2 d-flex align-items-center gap-2"><?= $this->logo($key, 22, 'nim-logo-tile') ?><?= e($titles[$key] ?? $key) ?></div>
<?php
switch ($key) {
    case 'dns':
    case 'mail':
        echo $this->partial('dashboard/widgets/dns_mail', ['d' => $d, 'only' => $key]);
        break;
    case 'redis':
    case 'memcached':
        echo $this->partial('dashboard/widgets/cache_overview', ['d' => $d, 'only' => $key]);
        break;
    case 'web':
        echo $this->partial('dashboard/widgets/web_stack', ['d' => $d]);
        break;
    case 'php':
        echo $this->partial('dashboard/widgets/web_stack', ['d' => $d, 'only' => 'php']);
        break;
    case 'database':
        echo $this->partial('dashboard/tile_database', ['d' => $d]);
        break;
    case 'firewall':
    case 'fail2ban':
    case 'clamav':
        echo $this->partial('dashboard/tile_security', ['d' => $d, 'key' => $key]);
        break;
    default:
        echo $this->partial('dashboard/widgets/ssl_overview', ['d' => $d]);
}
?>
<div class="d-flex flex-wrap gap-2 mt-2">
    <?php foreach ($links[$key] ?? [] as [$route, $label, $icon]): if ($this->hasRoute($route) && $this->routeAllowed($route)): ?>
        <a class="btn btn-sm btn-outline-primary" href="<?= e($this->url($route)) ?>"><i class="bi bi-<?= e($icon) ?> me-1" aria-hidden="true"></i><?= e($label) ?></a>
    <?php endif; endforeach; ?>
</div>
