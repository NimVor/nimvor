<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Tests\Support\HttpTestCase;

/** The administrator dashboard's widgets: disk, backups and what needs attention. */
final class DashboardWidgetsTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->agent->succeed('system.info', ['system' => ['hostname' => 'srv1'], 'os' => ['pretty_name' => 'Ubuntu', 'supported' => true]]);
        $this->agent->succeed('disk.filesystems', [['mount' => '/', 'device' => '/dev/vda1', 'type' => 'ext4', 'total_bytes' => 100000000000, 'used_bytes' => 91000000000, 'avail_bytes' => 9000000000, 'used_percent' => 91.0, 'inodes' => 100, 'inodes_used' => 10, 'inodes_percent' => 10.0]]);
        $this->login('admin');
    }

    public function testTheDiskWidgetShowsFilesystemsAndAHintToScan(): void
    {
        $page = $this->request('GET', '/')->body();
        self::assertStringContainsString('data-widget="disk"', $page);
        self::assertStringContainsString('nim-donut-seg c-bad', $page, 'a nearly full disk is a red donut');
        self::assertStringContainsString('>91%<', $page, 'the donut says how full it is');
        self::assertStringContainsString('Scan the disk on the Disk usage page', $page);
    }

    public function testTheSeparateBackupsWidgetIsGone(): void
    {
        $page = $this->request('GET', '/')->body();
        self::assertStringNotContainsString('data-widget="backups"', $page, 'Backup health already covers it');
        self::assertStringContainsString('data-widget="backup_health"', $page);
    }

    public function testSoftwareTilesOfDnsMailCachesWebAndCertbotOpenDetailsOnHover(): void
    {
        $this->agent->succeed('components.status', [
            ['id' => 'dns', 'name' => 'DNS server', 'installed' => true, 'units' => ['pdns'], 'steps' => [['id' => 'running', 'status' => 'ok']]],
            ['id' => 'database', 'name' => 'Database server', 'installed' => true, 'units' => ['mariadb'], 'steps' => [['id' => 'running', 'status' => 'ok']]],
            ['id' => 'php', 'name' => 'PHP', 'installed' => true, 'units' => ['php8.3-fpm'], 'steps' => [['id' => 'running', 'status' => 'ok']]],
        ]);
        $page = $this->request('GET', '/dashboard/widgets/software', [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        $html = (string) ((array) json_decode($page->body(), true))['html'];
        self::assertStringContainsString('data-popover-url="/dashboard/popover/dns"', $html);
        self::assertStringContainsString('/dashboard/popover/php', $html, 'PHP has a hover card');
        self::assertStringContainsString('/dashboard/popover/database', $html, 'so has the database server');
        self::assertStringContainsString('/databases/server/settings/modal', $html, 'the database tile\'s settings open in a window');
        self::assertStringContainsString('data-nim-site-modal', $html);
    }

    private function widgetHtml(string $id): string
    {
        $res = $this->request('GET', '/dashboard/widgets/' . $id, [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        self::assertSame(200, $res->status(), $res->body());
        return (string) ((array) json_decode($res->body(), true))['html'];
    }

    public function testWebsiteHealthAndStatisticsAndTheWebsitesCardAreOneWidgetInTabs(): void
    {
        $page = $this->request('GET', '/')->body();
        foreach (['website_health', 'websites_summary'] as $gone) {
            self::assertStringNotContainsString('data-widget="' . $gone . '"', $page, $gone . ' is a tab of Website statistics now');
        }
        self::assertStringNotContainsString('data-net-rx', $page, 'the Network card is gone from the dashboard');
        $html = $this->widgetHtml('website_overview');
        self::assertStringContainsString('data-nim-tabs="website-statistics"', $html);
        foreach (['overview', 'slowest', 'created', 'websites'] as $tab) {
            self::assertStringContainsString('data-nim-tab="' . $tab . '"', $html);
            self::assertStringContainsString('data-nim-pane="' . $tab . '"', $html);
        }
        foreach (['Slowest websites', 'Created per month', 'Using HTTPS'] as $text) {
            self::assertStringContainsString($text, $html);
        }
    }

    public function testAlertsAndNeedsAttentionAreOneWidgetInTwoTabs(): void
    {
        $page = $this->request('GET', '/')->body();
        self::assertStringNotContainsString('data-widget-host="attention"', $page, 'Needs attention is a tab of the alerts widget now');
        $html = $this->widgetHtml('alert_center');
        self::assertStringContainsString('data-nim-tabs="alerts-attention"', $html);
        self::assertStringContainsString('data-nim-pane="alerts"', $html);
        self::assertStringContainsString('data-nim-pane="attention"', $html);
        self::assertStringContainsString('Needs attention', $html);
    }

    public function testTheDatabasesWidgetHasTheChartAndOneTabPerServer(): void
    {
        $this->agent->succeed('db.server_status', ['running' => true, 'unit' => 'mariadb', 'databases' => 2, 'bytes' => 1024, 'variables' => ['version' => '10.11.6-MariaDB', 'max_connections' => '100'], 'status' => ['Uptime' => '90000', 'Threads_connected' => '3']]);
        $this->agent->succeed('pg.status', ['installed' => false]);
        $html = $this->widgetHtml('database_overview');
        self::assertStringContainsString('data-nim-tabs="databases"', $html);
        foreach (['conn', 'mysql'] as $pane) {
            self::assertStringContainsString('data-nim-pane="' . $pane . '"', $html);
        }
        self::assertStringContainsString('data-nim-chart', $html, 'the chart is in the first tab');
        self::assertStringNotContainsString('data-nim-pane="postgres"', $html, 'no PostgreSQL, no PostgreSQL tab');
    }

    public function testTheWideWidgetsAreSizedByTheirNewTabs(): void
    {
        $reg = new \NimVor\Dashboard\WidgetRegistry();
        self::assertNull($reg->find('top_processes'), 'the Top processes widget is gone (the Processes page has the list)');
        self::assertSame('full', $reg->find('software')->size, 'Software spans the 12 columns');
        self::assertSame('half', $reg->find('traffic_overview')->size);
        self::assertSame('half', $reg->find('request_trends')->size);
        foreach (['backup_health', 'disk', 'alert_center'] as $id) {
            self::assertSame('half', $reg->find($id)->size, $id);
        }
        self::assertNull($reg->find('attention'));
        self::assertSame('nim-span-half', \NimVor\Dashboard\Widget::SIZES['half']);
        self::assertNull($reg->find('website_health'));
        self::assertNull($reg->find('websites_summary'));
    }

    public function testTheLatestDiskScanListsTheBiggestPlaces(): void
    {
        $db = $this->app->container->get(Connection::class);
        $result = ['scanned_at' => gmdate('c'), 'areas' => [
            ['key' => 'sites', 'label' => 'Websites', 'path' => '/home/nimvor', 'bytes' => 9000000000, 'files' => 1],
            ['key' => 'logs', 'label' => 'System and service logs', 'path' => '/var/log', 'bytes' => 500000000, 'files' => 1],
        ]];
        $db->run("INSERT INTO server_disk_scans (server_id, job_id, status, result, started_at, finished_at) VALUES (1, 'abcdef0123456789', 'done', ?, ?, ?)", [json_encode($result), gmdate('c'), gmdate('c')]);
        $page = $this->request('GET', '/')->body();
        self::assertStringContainsString('Biggest places', $page);
        self::assertStringContainsString('System and service logs', $page);
    }

    public function testTheTopCardsAreWidgetsDrawnWithThePage(): void
    {
        $page = $this->request('GET', '/')->body();
        foreach (['tile_cpu', 'tile_memory', 'tile_disk', 'tile_load', 'software', 'website_overview'] as $id) {
            self::assertStringContainsString('data-widget-host="' . $id . '"', $page, $id);
        }
        self::assertStringContainsString('data-metric', $page, 'the live poll still finds the cards');
    }

    public function testRequestTrendsAddUpTheSitesAndSayNotAvailableWithoutLogs(): void
    {
        $db = $this->app->container->get(Connection::class);
        $db->run("INSERT INTO websites (server_id, domain, kind, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, 'shop.test', 'static', '/h/s', '/h/s/p', 'u1', 1, 'active', 'x', 'x')");
        $now = intdiv(time(), 3600) * 3600;
        $this->agent->succeed('webreport.site', [
            'exists' => true, 'truncated' => false, 'hours' => 48, 'requests' => 900, 'refused' => 10,
            'classes' => [0, 0, 800, 50, 40, 10],
            'timeline' => [['start' => $now - 7200, 'requests' => 100, 'refused' => 0], ['start' => $now - 3600, 'requests' => 200, 'refused' => 10]],
        ]);
        $res = $this->request('GET', '/dashboard/widgets/request_trends', [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        $w = (array) json_decode($res->body(), true);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame('ok', $w['state']);
        self::assertStringContainsString('shop.test', $w['html']);
    }

    public function testRequestTrendsAreNotAvailableWhenNothingCanBeRead(): void
    {
        $res = $this->request('GET', '/dashboard/widgets/request_trends', [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        $w = (array) json_decode($res->body(), true);
        self::assertSame(200, $res->status(), $res->body());
        self::assertNotSame('ok', $w['state']);
    }

    public function testDatabasePhpFirewallFail2banAndMalwareScannerTilesOpenDetailsOnHover(): void
    {
        $unit = static fn (string $id, string $name, string $svc): array => ['id' => $id, 'name' => $name, 'installed' => true, 'units' => [$svc], 'steps' => [['id' => 'running', 'status' => 'ok']]];
        $this->agent->succeed('components.status', [$unit('database', 'Database server', 'mariadb'), $unit('php', 'PHP', 'php8.3-fpm'), $unit('firewall', 'Firewall', 'ufw'), $unit('fail2ban', 'fail2ban', 'fail2ban'), $unit('clamav', 'Malware scanner', 'clamav-freshclam')]);
        $this->agent->succeed('db.server_status', ['running' => true, 'unit' => 'mariadb', 'variables' => ['version' => '10.11.14-MariaDB', 'max_connections' => '200'],
            'status' => ['Uptime' => '90000', 'Threads_connected' => '12', 'Threads_running' => '2', 'Slow_queries' => '5', 'Innodb_buffer_pool_read_requests' => '1000', 'Innodb_buffer_pool_reads' => '10'], 'databases' => 7, 'bytes' => 5242880]);
        $this->agent->succeed('pg.status', ['installed' => false]);
        $this->agent->succeed('nginx.status', ['installed' => true, 'running' => true, 'config_ok' => true]);
        $this->agent->succeed('php.fpm_health', ['versions' => [['version' => '8.3', 'installed' => true, 'active' => true, 'log_read' => true, 'max_children_reached' => 0, 'busy_warnings' => 0, 'child_crashes' => 0]]]);
        $this->agent->succeed('firewall.status', ['installed' => true, 'active' => true]);
        $this->agent->succeed('fail2ban.status', ['installed' => true, 'running' => true, 'jails' => [['name' => 'sshd', 'currently_banned' => 3]]]);
        $this->agent->succeed('ssh.logins', ['available' => true, 'today' => [1, 14], 'yesterday' => [0, 0], 'week' => [9, 120], 'last_failure' => ['ip' => '203.0.113.7']]);
        $this->agent->succeed('av.status', ['installed' => true, 'version' => 'ClamAV 1.4/1/2026', 'freshclam' => true]);
        $this->agent->succeed('modsec.status', ['active' => false]);
        $this->agent->succeed('security.audit', []);

        $page = $this->request('GET', '/dashboard/widgets/software', [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        $html = (string) ((array) json_decode($page->body(), true))['html'];
        $expect = [
            'database' => ['Running', '10.11.14-MariaDB', '1 d 1 h', '12 / 200', 'Databases, size', 'Database server'],
            'php' => ['PHP-FPM', 'No enabled website uses PHP', 'PHP versions'],
            'firewall' => ['Active', 'Failed SSH sign-ins today', 'Firewall'],
            'fail2ban' => ['Active', 'Banned addresses', '203.0.113.7'],
            'clamav' => ['Active', 'ClamAV 1.4/1/2026', 'Malware scanner'],
        ];
        foreach ($expect as $key => $wants) {
            self::assertStringContainsString('data-popover-url="/dashboard/popover/' . $key . '"', $html, $key . ' tile has a hover card');
            $res = json_decode($this->request('GET', '/dashboard/popover/' . $key)->body(), true);
            self::assertTrue($res['success'], $key . ': ' . json_encode($res));
            foreach ($wants as $want) {
                self::assertStringContainsString($want, $res['html'], $key . ': ' . $want);
            }
        }
        // The PHP card shows PHP-FPM only: the web server's part is the Web server tile's.
        self::assertStringNotContainsString('configuration test', json_decode($this->request('GET', '/dashboard/popover/php')->body(), true)['html']);
    }

    public function testASoftwareCardIsOnlyForThoseWhoMaySeeIt(): void
    {
        $roles = $this->app->container->get(\NimVor\Repositories\RoleRepository::class);
        $id = $roles->create('dash_only2', 'Dashboard only', 'x');
        $roles->setPermissions($id, [\NimVor\Security\Permissions::DASHBOARD_VIEW]);
        $this->app->container->get(\NimVor\Repositories\UserRepository::class)->create([
            'username' => 'bare', 'email' => 'bare@example.test', 'display_name' => '',
            'password_hash' => (new \NimVor\Security\PasswordHasher())->hash('a-long-test-password'), 'role_id' => $id, 'is_active' => true,
        ]);
        $this->login('bare');
        foreach (['database', 'php', 'firewall', 'fail2ban', 'clamav'] as $key) {
            self::assertSame(403, $this->request('GET', '/dashboard/popover/' . $key)->status(), $key);
        }
    }
}
