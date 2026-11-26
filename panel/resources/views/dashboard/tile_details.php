<?php
/**
 * The hover card of a Software tile: the details of that part of the server (they used to be dashboard widgets of their own)
 * and links to the pages that manage it.
 *
 * @var \NimVor\View\View $this
 * @var string $key dns, mail, redis, memcached, web or ssl
 * @var array<string, mixed> $d
 */
$titles = ['dns' => 'DNS server', 'mail' => 'Email', 'redis' => 'Redis', 'memcached' => 'Memcached', 'web' => 'Web server', 'ssl' => "Let's Encrypt certificates"];
$links = [
    'dns' => [['dns', 'DNS zones', 'diagram-3']],
    'mail' => [['mail.admin.server', 'Mail server', 'envelope'], ['mail.admin.queue', 'Mail queue', 'inbox'], ['mail.admin.reports', 'Delivery reports', 'bar-chart']],
    'redis' => [['redis', 'Redis', 'lightning-charge']],
    'memcached' => [['memcached', 'Memcached', 'lightning']],
    'web' => [['nginx', 'Nginx', 'hdd-network'], ['php', 'PHP versions', 'filetype-php'], ['websites', 'Websites', 'globe2']],
    'ssl' => [['ssl.hosts', 'Certificates', 'lock'], ['ssl.autossl', 'AutoSSL', 'arrow-repeat']],
];
?>
<div class="nim-pop-title mb-2"><?= e($titles[$key] ?? $key) ?></div>
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
    default:
        echo $this->partial('dashboard/widgets/ssl_overview', ['d' => $d]);
}
?>
<div class="d-flex flex-wrap gap-2 mt-2">
    <?php foreach ($links[$key] ?? [] as [$route, $label, $icon]): if ($this->hasRoute($route) && $this->routeAllowed($route)): ?>
        <a class="btn btn-sm btn-outline-primary" href="<?= e($this->url($route)) ?>"><i class="bi bi-<?= e($icon) ?> me-1" aria-hidden="true"></i><?= e($label) ?></a>
    <?php endif; endforeach; ?>
</div>
