<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

/**
 * The administrators' and resellers' list pages: websites (status switch,
 * backups, SSL days), firewall rules (port, source, listening state),
 * the security overview (SSH and panel logins, rating) and the FTP page.
 */
final class AdminListsTest extends HttpTestCase
{
    private int $shopId;
    private int $otherId;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $reseller = $this->seedUser('resell', 'reseller');
        $users = $this->app->container->get(UserRepository::class);
        $this->db = $this->app->container->get(Connection::class);
        $this->db->run('UPDATE users SET parent_id = ? WHERE id = ?', [$reseller, $this->seedUser('cust', 'customer')]);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->shopId = $sites->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3'], (array) $users->find($reseller));
        $this->otherId = $sites->create(['domain' => 'other.test', 'kind' => 'static'], (array) $users->find($admin));
        $this->db->run("UPDATE websites SET https = 1 WHERE id IN (?, ?)", [$this->shopId, $this->otherId]);
        foreach ([1, 2] as $n) {
            $this->db->run("INSERT INTO backups (website_id, kind, status, created_at) VALUES (?, 'website', 'completed', ?)", [$this->shopId, '2026-09-0' . $n]);
        }
        $this->agent->succeed('discovery.inventory', ['websites' => ['nginx_version' => '1.24.0', 'catch_all_vhosts' => 0, 'warnings' => [], 'websites' => [
            ['id' => 'a1', 'domain' => 'shop.test', 'enabled' => true, 'https' => true, 'kind' => 'php', 'config_files' => ['/etc/nginx/sites-available/whacp-shop.test.conf'], 'ssl' => ['days_remaining' => 50, 'status' => 'valid']],
            ['id' => 'a2', 'domain' => 'other.test', 'enabled' => true, 'https' => true, 'kind' => 'static', 'config_files' => ['/etc/nginx/sites-available/whacp-other.test.conf'], 'ssl' => ['days_remaining' => 7, 'status' => 'valid']],
        ]]]);
        $this->agent->succeed('security.pending', ['pending' => null]);
    }

    private function switchTo(string $user): void
    {
        $this->session = new \WhaCP\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login($user);
    }

    public function testWebsitesTable(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/websites')->body();
        self::assertStringContainsString('Nginx 1.24.0', $page);
        self::assertMatchesRegularExpression('#/sites/' . $this->shopId . '/backups"><span class="wcp-good fw-semibold">2</span>#', $page, 'completed backups counted');
        self::assertStringContainsString('>50 Days<', $page);
        self::assertMatchesRegularExpression('#class="wcp-warn" href="/sites/' . $this->otherId . '/ssl">7 Days<#', $page, 'expiring soon is marked');
        self::assertStringContainsString('#delete-website', $page);

        // The status switch disables the website and comes back to the list.
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $res = $this->post('/sites/' . $this->shopId . '/toggle', ['return_to' => 'websites']);
        self::assertSame('/websites', $res->header('Location'));
        self::assertSame(0, (int) $this->db->scalar('SELECT enabled FROM websites WHERE id = ?', [$this->shopId]));
        $res = $this->post('/sites/' . $this->shopId . '/toggle', ['return_to' => 'https://evil.example']);
        self::assertSame('/sites/' . $this->shopId . '/advanced', $res->header('Location'), 'unknown targets go to the website');
    }

    public function testResellersSeeCertificatesOfTheirOwnWebsitesOnly(): void
    {
        $this->login('resell');
        $page = $this->request('GET', '/websites')->body();
        self::assertStringContainsString('shop.test', $page);
        self::assertStringContainsString('>50 Days<', $page);
        self::assertStringNotContainsString('other.test', $page);
        self::assertStringNotContainsString('7 Days', $page);
        self::assertStringNotContainsString('Other websites on this server', $page);
    }

    public function testFirewallRulesTable(): void
    {
        $this->login('admin');
        $this->agent->succeed('firewall.status', ['installed' => true, 'active' => true, 'backend' => 'ufw', 'default_incoming' => 'deny', 'default_outgoing' => 'allow', 'ssh_port' => 22, 'ssh_allowed' => true,
            'rules' => [
                ['spec' => "limit 22/tcp comment 'SSH access'", 'action' => 'limit', 'ssh' => true, 'port' => '22', 'proto' => 'tcp', 'from' => '', 'direction' => 'in', 'comment' => 'SSH access'],
                ['spec' => 'allow from 203.0.113.4 to any port 3306 proto tcp', 'action' => 'allow', 'ssh' => false, 'port' => '3306', 'proto' => 'tcp', 'from' => '203.0.113.4', 'direction' => 'in', 'comment' => ''],
                ['spec' => 'allow 8080', 'action' => 'allow', 'ssh' => false, 'port' => '8080', 'proto' => '', 'from' => '', 'direction' => 'in', 'comment' => ''],
                ['spec' => 'deny from 198.51.100.0/24', 'action' => 'deny', 'ssh' => false, 'port' => '', 'proto' => '', 'from' => '198.51.100.0/24', 'direction' => 'in', 'comment' => ''],
                ['spec' => 'deny out 25', 'action' => 'deny', 'ssh' => false, 'port' => '25', 'proto' => '', 'from' => '', 'direction' => 'out', 'comment' => ''],
            ],
            'listening' => [['proto' => 'tcp', 'port' => 22, 'local' => false], ['proto' => 'tcp', 'port' => 3306, 'local' => true]],
        ]);
        $page = $this->request('GET', '/security/firewall')->body();
        self::assertStringContainsString('Port rules: 4', $page);
        self::assertStringContainsString('IP rules: 1', $page);
        self::assertMatchesRegularExpression('#data-dt-kind="port" data-dt-dir="in" title="limit 22/tcp comment &apos;SSH access&apos;">.*?Listening.*?Allow \(rate-limited\).*?SSH access#s', $page);
        self::assertMatchesRegularExpression('#title="allow from 203.0.113.4 to any port 3306 proto tcp">.*?Local only.*?<code>203.0.113.4</code>#s', $page);
        self::assertMatchesRegularExpression('#title="allow 8080">.*?tcp/udp.*?Not listening#s', $page);
        self::assertMatchesRegularExpression('#data-dt-kind="ip" data-dt-dir="in" title="deny from 198.51.100.0/24">.*?>All<.*?Deny#s', $page);
        self::assertMatchesRegularExpression('#data-dt-dir="out" title="deny out 25">.*?Outbound#s', $page);
        self::assertStringContainsString('name="spec" value="deny out 25"', $page, 'rules are deleted by their spec');
        self::assertStringContainsString('data-wcp-switch-submit checked', $page);
    }

    public function testSecurityOverview(): void
    {
        $this->login('admin');
        $this->db->run("INSERT INTO audit_logs (username, action, result, ip, created_at) VALUES ('admin', 'auth.login', 'failure', '198.51.100.20', '2026-09-27T08:00:00+00:00')");
        $this->agent->succeed('security.audit', [
            ['id' => 'firewall', 'title' => 'Firewall', 'status' => 'fail', 'severity' => 'high', 'detail' => 'UFW is inactive.', 'remedy' => 'Enable the firewall.'],
            ['id' => 'ssh', 'title' => 'SSH server', 'status' => 'pass', 'severity' => 'high', 'detail' => 'Root logs in with keys only.', 'remedy' => ''],
            ['id' => 'updates', 'title' => 'Automatic security updates', 'status' => 'warn', 'severity' => 'medium', 'detail' => 'Off.', 'remedy' => 'Turn them on.'],
            ['id' => 'os', 'title' => 'Operating system', 'status' => 'info', 'severity' => 'low', 'detail' => 'Ubuntu 24.04', 'remedy' => ''],
        ]);
        $this->agent->succeed('ssh.logins', ['available' => true, 'today' => [2, 17], 'yesterday' => [1, 40], 'week' => [9, 300],
            'last_success' => ['time' => '2026-09-27T09:12:00Z', 'user' => 'root', 'ip' => '203.0.113.10', 'port' => 52000, 'method' => 'publickey'],
            'last_failure' => ['time' => '2026-09-27T09:30:00Z', 'user' => 'oracle', 'ip' => '198.51.100.8', 'port' => 40000]]);
        $this->agent->succeed('fail2ban.status', ['installed' => true, 'running' => true, 'jails' => [], 'managed_config' => '']);
        $page = $this->request('GET', '/security')->body();
        // (high pass 3 + medium warn 1 + the panel's own password strength
        // check, medium pass 2) / (3 + 3 + 2 + 2) = 60; info does not count.
        self::assertStringContainsString('60 / 100', $page);
        self::assertStringContainsString('Fair', $page);
        self::assertMatchesRegularExpression('#wcp-stat-num">2<.*?wcp-stat-num">17<#s', $page);
        self::assertStringContainsString('203.0.113.10', $page);
        self::assertStringContainsString('oracle from 198.51.100.8', $page);
        self::assertStringContainsString('admin from 198.51.100.20', $page, 'last failed panel login');
        self::assertStringContainsString('SSH brute-force protection is off', $page, 'fail2ban runs without an sshd jail');
        self::assertLessThan(strpos($page, 'SSH server'), strpos($page, 'Firewall</div>'), 'problems first');

        // An agent without SSH login history: the rest still shows.
        $this->agent->fail('ssh.logins', 404, 'not_found', 'Unknown action');
        $page = $this->request('GET', '/security')->body();
        self::assertStringContainsString('SSH login history is not available', $page);
        self::assertStringContainsString('60 / 100', $page);
    }

    public function testFtpPageShowsTheAddress(): void
    {
        $this->login('admin');
        $this->app->container->get(\WhaCP\Services\SettingsService::class)->update(['dns_default_ipv4' => '203.0.113.50'], null);
        $this->agent->succeed('ftp.status', ['installed' => true, 'configured' => true, 'active' => true, 'settings' => ['tls' => 'required', 'passive_min' => 40110, 'passive_max' => 40210], 'accounts' => [
            ['name' => 'designer@shop.test', 'user' => (string) $this->db->scalar('SELECT system_user FROM websites WHERE id = ?', [$this->shopId]), 'dir' => '/home/whacp/shop.test/public', 'quota_mb' => 0],
        ], 'other_ftp_servers' => []]);
        $page = $this->request('GET', '/ftp')->body();
        self::assertStringContainsString('ftp://203.0.113.50:21', $page);
        self::assertStringContainsString('designer@shop.test', $page);
        self::assertStringContainsString('/sites/' . $this->shopId . '/ftp#new-ftp', $page, 'Add FTP picks a website');
        self::assertStringContainsString('Pure-FTPd running', $page);
    }
}
