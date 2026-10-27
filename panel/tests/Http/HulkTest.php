<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Services\GeoIpService;
use WhaCP\Services\HulkService;
use WhaCP\Services\SettingsService;
use WhaCP\Tests\Support\HttpTestCase;

/** Security → cPHulk Brute Force Protection. */
final class HulkTest extends HttpTestCase
{
    private const BASE = [
        'hulk_enabled' => '1', 'hulk_user_enabled' => '1', 'hulk_user_period' => '5', 'hulk_user_max' => '3', 'hulk_user_scope' => 'all', 'hulk_user_lock_root' => '0',
        'hulk_ip_enabled' => '1', 'hulk_ip_period' => '15', 'hulk_ip_max' => '3', 'hulk_ip_command' => '', 'hulk_ip_firewall' => '0',
        'hulk_oneday_max' => '8', 'hulk_oneday_command' => '', 'hulk_oneday_firewall' => '1', 'hulk_retention' => '360',
        'hulk_notify_root' => '1', 'hulk_notify_root_netblock' => '0', 'hulk_notify_brute' => '1', 'hulk_whitelist' => "192.0.2.10\n2001:db8:ff::/48",
    ];

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $this->agent->succeed('hulk.sync', ['changed' => true, 'active' => true]);
        $this->agent->succeed('hulk.status', ['available' => true, 'active' => true]);
        $this->agent->succeed('hulk.failures', ['events' => [], 'journal' => true]);
    }

    private function db(): Connection
    {
        return $this->app->container->get(Connection::class);
    }

    private function browser(string $ip): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->remoteIp = $ip;
    }

    /** @param array<string, string> $over */
    private function configure(array $over = []): void
    {
        $this->remoteIp = '192.0.2.10';
        $this->login('admin');
        $this->post('/security/hulk', $over + self::BASE);
        self::assertNull($this->flash('error'), (string) json_encode($this->session->get('_flash')));
    }

    public function testPageShowsSettingsCountriesAndReport(): void
    {
        $this->remoteIp = '192.0.2.10';
        $this->login('admin');
        $page = $this->request('GET', '/security/hulk')->body();
        foreach ([
            'cPHulk provides protection from brute force attacks against your web services.',
            'Username-based protection tracks login attempts for user accounts.', 'Maximum Failures by Account',
            'Apply protection to local addresses only', 'Apply protection to local and remote addresses', 'Allow username protection to lock the “root” user',
            'IP Address-based protection tracks login attempts from specific IP addresses.', 'Maximum Failures per IP Address',
            'The command must complete within 15 seconds', '%exptime%', '%max_allowed_failures%', '%current_failures%', '%excessive_failures%',
            '%reason%', '%remote_ip%', '%authservice%', '%user%', '%logintime%', '%ip_version%',
            'Block IP addresses at the firewall level if they trigger brute force protection', 'Maximum Failures per IP Address before the IP Address is Blocked for One Day',
            'Command to Run When an IP Address Triggers a One-Day Block', 'Block IP addresses at the firewall level if they trigger a one-day block',
            'Duration for Retaining Failed Logins (in minutes)', 'not on the whitelist, but from a known netblock', 'Send a notification when the system detects a brute force user',
            'Countries Management', 'Whitelisted', 'Not Specified', 'Blacklisted', 'Pakistan', 'name="country[PK]"',
            'Failed Logins Report', 'which is currently set to 360 minutes', 'Authentication Service', 'Minutes Remaining', 'Remove Blocks and clear report',
        ] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertStringContainsString('data-wcp-section-menu="security"', $page);
    }

    public function testTablesAreSearchableAndCountriesHaveSelectAll(): void
    {
        $this->remoteIp = '192.0.2.10';
        $this->login('admin');
        $page = $this->request('GET', '/security/hulk')->body();
        // The data-table search only filters rows marked data-wcp-row.
        self::assertGreaterThan(200, substr_count($page, '<tr data-wcp-row>'));
        self::assertStringContainsString('data-wcp-select-all="selected[]"', $page);
        self::assertSame(2, substr_count($page, 'data-wcp-dt-pager'));
    }

    public function testSettingsAreValidatedAndApplied(): void
    {
        $this->remoteIp = '192.0.2.10';
        $this->login('admin');
        $this->post('/security/hulk', ['hulk_whitelist' => 'not-an-ip'] + self::BASE);
        self::assertStringContainsString('not an IP address', (string) $this->flash('error'));
        $this->post('/security/hulk', ['hulk_user_scope' => 'everything'] + self::BASE);
        self::assertNotNull($this->flash('error'));
        self::assertFalse($this->app->container->get(SettingsService::class)->bool('hulk_enabled'));
        $this->post('/security/hulk', self::BASE);
        $sync = $this->lastCall('hulk.sync');
        self::assertTrue($sync['enabled']);
        self::assertSame(['192.0.2.10', '2001:db8:ff::/48'], $sync['whitelist']);
        // First switched on without a whitelist: the administrator's own address is added.
        $this->post('/security/hulk', ['hulk_enabled' => '0'] + self::BASE);
        $this->post('/security/hulk', ['hulk_whitelist' => ''] + self::BASE);
        self::assertSame(['192.0.2.10'], $this->lastCall('hulk.sync')['whitelist']);
    }

    public function testPanelSignInsBlockTheAddressAndLockTheAccount(): void
    {
        $this->configure();
        // Three bad passwords from one address: the address is blocked, even
        // for the right password.
        $this->browser('198.51.100.20');
        for ($i = 0; $i < 3; $i++) {
            $this->post('/login', ['username' => 'nobody-' . $i, 'password' => 'wrong-password-123']);
        }
        self::assertSame(1, (int) $this->db()->scalar("SELECT COUNT(*) FROM hulk_blocks WHERE kind = 'ip' AND ip = '198.51.100.20'"));
        self::assertSame([['ip' => '198.51.100.20', 'all' => false]], array_map(static fn (array $b): array => ['ip' => $b['ip'], 'all' => $b['all']], $this->lastCall('hulk.sync')['blocks']));
        $this->post('/login', ['username' => 'cust', 'password' => 'a-long-test-password']);
        self::assertStringContainsString('Your IP address is blocked', (string) $this->flash('error'));
        self::assertNotNull($this->db()->scalar("SELECT id FROM notifications WHERE title LIKE 'Brute force attack detected: 198.51.100.20'"));

        // Failures for one account from many addresses lock the account.
        foreach (['203.0.113.1', '203.0.113.2', '203.0.113.3'] as $ip) {
            $this->browser($ip);
            $this->post('/login', ['username' => 'cust', 'password' => 'wrong-password-123']);
        }
        $this->browser('203.0.113.4');
        $this->post('/login', ['username' => 'cust', 'password' => 'a-long-test-password']);
        self::assertStringContainsString('This account is locked', (string) $this->flash('error'));
        // Administrators follow the "root" option (off): not locked.
        foreach (['203.0.113.5', '203.0.113.6', '203.0.113.7'] as $ip) {
            $this->browser($ip);
            $this->post('/login', ['username' => 'admin', 'password' => 'wrong-password-123']);
        }
        $this->browser('203.0.113.8');
        $this->login('admin');

        // A whitelisted address is never blocked.
        for ($i = 0; $i < 5; $i++) {
            $this->browser('192.0.2.10');
            $this->post('/login', ['username' => 'x' . $i, 'password' => 'wrong-password-123']);
        }
        $this->browser('192.0.2.10');
        $this->login('admin');
        self::assertSame(0, (int) $this->db()->scalar("SELECT COUNT(*) FROM hulk_blocks WHERE ip = '192.0.2.10'"));

        // Report, unblock, and "Remove Blocks and clear report".
        $page = $this->request('GET', '/security/hulk')->body();
        self::assertStringContainsString('198.51.100.20', $page);
        self::assertStringContainsString('whacpd', $page);
        $id = (int) $this->db()->scalar("SELECT id FROM hulk_blocks WHERE ip = '198.51.100.20'");
        $this->post('/security/hulk/unblock', ['id' => (string) $id]);
        self::assertSame(0, (int) $this->db()->scalar('SELECT COUNT(*) FROM hulk_blocks WHERE id = ?', [$id]));
        $this->post('/security/hulk/clear');
        self::assertSame(0, (int) $this->db()->scalar('SELECT COUNT(*) FROM hulk_blocks'));
        self::assertSame(0, (int) $this->db()->scalar('SELECT COUNT(*) FROM hulk_events'));
        self::assertSame([], $this->lastCall('hulk.sync')['blocks']);
    }

    public function testServiceFailuresFromTheAgent(): void
    {
        $out = sys_get_temp_dir() . '/whacp-hulk-cmd-' . bin2hex(random_bytes(4));
        $pwned = $out . '-pwned';
        $this->configure(['hulk_ip_command' => 'printf "%s|%s|%s|%s" %remote_ip% %user% %current_failures% %ip_version% > ' . $out, 'hulk_oneday_command' => 'true']);
        $now = time();
        $events = [];
        for ($i = 0; $i < 3; $i++) {
            $events[] = ['id' => 'e' . $i, 'time' => $now - 60 + $i, 'service' => 'system', 'authservice' => 'sshd', 'user' => 'x; touch ' . $pwned, 'ip' => '2001:db8::66', 'ok' => false];
        }
        $events[] = ['id' => 'root1', 'time' => $now - 5, 'service' => 'system', 'authservice' => 'sshd', 'user' => 'root', 'ip' => '198.51.100.99', 'ok' => true];
        $events[] = ['id' => 'root2', 'time' => $now - 4, 'service' => 'system', 'authservice' => 'sshd', 'user' => 'root', 'ip' => '192.0.2.10', 'ok' => true];
        $this->agent->succeed('hulk.failures', ['events' => $events, 'journal' => true]);
        $this->app->container->get(HulkService::class)->run();
        // The IP block, its command with quoted values, no injection.
        self::assertSame(1, (int) $this->db()->scalar("SELECT COUNT(*) FROM hulk_blocks WHERE kind = 'ip' AND ip = '2001:db8::66'"));
        self::assertSame('2001:db8::66|x; touch ' . $pwned . '|3|6', (string) @file_get_contents($out));
        self::assertFileDoesNotExist($pwned);
        @unlink($out);
        // Root login from a non-whitelisted address is reported; from a whitelisted one it is not.
        self::assertNotNull($this->db()->scalar("SELECT id FROM notifications WHERE title = 'Root login from 198.51.100.99'"));
        self::assertNull($this->db()->scalar("SELECT id FROM notifications WHERE title = 'Root login from 192.0.2.10'"));
        // The same events again are not counted twice.
        $this->app->container->get(HulkService::class)->run();
        self::assertSame(3, (int) $this->db()->scalar("SELECT COUNT(*) FROM hulk_events WHERE id NOT LIKE 'p:%' AND ok = 0"));

        // Enough in a day: a one-day block at the firewall level.
        $more = [];
        for ($i = 0; $i < 8; $i++) {
            $more[] = ['id' => 'd' . $i, 'time' => $now - 30 + $i, 'service' => 'mail', 'authservice' => 'dovecot (imap)', 'user' => 'bob@shop.test', 'ip' => '203.0.113.50', 'ok' => false];
        }
        $this->agent->succeed('hulk.failures', ['events' => $more, 'journal' => true]);
        $this->app->container->get(HulkService::class)->run();
        $b = $this->db()->fetch("SELECT * FROM hulk_blocks WHERE kind = 'oneday' AND ip = '203.0.113.50'");
        self::assertNotNull($b);
        self::assertSame(1, (int) $b['firewall']);
        self::assertGreaterThan($now + 86000, (int) $b['expires_at']);
        $all = array_column($this->lastCall('hulk.sync')['blocks'], 'all', 'ip');
        self::assertTrue($all['203.0.113.50']);
        self::assertFalse($all['2001:db8::66']);
        // The mailbox account got locked too (3 failures for it).
        self::assertSame(1, (int) $this->db()->scalar("SELECT COUNT(*) FROM hulk_blocks WHERE kind = 'user' AND user = 'bob@shop.test'"));
    }

    public function testCountries(): void
    {
        $csv = sys_get_temp_dir() . '/whacp-geo-' . bin2hex(random_bytes(4)) . '.csv.gz';
        file_put_contents($csv, (string) gzencode("198.51.100.0,198.51.100.255,KP\n203.0.113.0,203.0.113.255,DE\n2001:db8:77::,2001:db8:77:ffff:ffff:ffff:ffff:ffff,KP\nbad,line,XX\n"));
        $geo = $this->app->container->get(GeoIpService::class);
        self::assertSame(3, $geo->import($csv));
        @unlink($csv);
        self::assertSame('KP', $geo->country('198.51.100.7'));
        self::assertSame('KP', $geo->country('2001:db8:77::1'));
        self::assertSame('DE', $geo->country('203.0.113.9'));
        self::assertSame('', $geo->country('192.0.2.1'));

        $this->configure();
        $this->post('/security/hulk/countries', ['country' => ['KP' => 'blacklist', 'DE' => 'whitelist', 'FR' => 'none', 'ZZ' => 'blacklist']]);
        self::assertEquals(['DE' => 'whitelist', 'KP' => 'blacklist'], $this->app->container->get(HulkService::class)->countries());
        $sync = $this->lastCall('hulk.sync');
        self::assertSame([['from' => '198.51.100.0', 'to' => '198.51.100.255'], ['from' => '2001:db8:77::', 'to' => '2001:db8:77:ffff:ffff:ffff:ffff:ffff']], $sync['countries']);
        self::assertFalse($sync['countries_all'], 'login services only by default');
        // Every port (CC_DENY).
        $this->post('/security/hulk/countries', ['scope' => 'all', 'country' => ['KP' => 'blacklist']]);
        self::assertTrue($this->lastCall('hulk.sync')['countries_all']);
        $this->post('/security/hulk/countries', ['scope' => 'everywhere']);
        self::assertTrue($this->lastCall('hulk.sync')['countries_all'], 'an unknown scope changes nothing');
        $this->post('/security/hulk/countries', ['scope' => 'login']);
        self::assertFalse($this->lastCall('hulk.sync')['countries_all']);
        // Bulk marking.
        $this->post('/security/hulk/countries', ['bulk' => 'blacklist', 'selected' => ['FR', 'IT']]);
        self::assertSame('blacklist', $this->app->container->get(HulkService::class)->countries()['IT']);

        // A blacklisted country cannot sign in; a whitelisted one is never blocked.
        $this->browser('198.51.100.7');
        $this->post('/login', ['username' => 'cust', 'password' => 'a-long-test-password']);
        self::assertStringContainsString('not allowed', (string) $this->flash('error'));
        for ($i = 0; $i < 4; $i++) {
            $this->browser('203.0.113.9');
            $this->post('/login', ['username' => 'z' . $i, 'password' => 'wrong-password-123']);
        }
        self::assertSame(0, (int) $this->db()->scalar("SELECT COUNT(*) FROM hulk_blocks WHERE ip = '203.0.113.9'"));
        $this->browser('192.0.2.10');
        $this->login('admin');
        self::assertStringContainsString('Germany', $this->request('GET', '/security/hulk')->body(), 'the report names the country');
    }

    public function testDisabledProtectionRemovesTheRules(): void
    {
        $this->app->container->get(HulkService::class)->run();
        self::assertFalse($this->lastCall('hulk.sync')['enabled']);
        $n = $this->calls('hulk.sync');
        $this->app->container->get(HulkService::class)->run();
        self::assertSame($n, $this->calls('hulk.sync'), 'only once');
        self::assertSame(0, $this->calls('hulk.failures'));
    }

    public function testOnlyAdministrators(): void
    {
        $this->login('cust');
        self::assertNotSame(200, $this->request('GET', '/security/hulk')->status());
        $this->post('/security/hulk', self::BASE);
        $this->post('/security/hulk/clear');
        self::assertFalse($this->app->container->get(SettingsService::class)->bool('hulk_enabled'));
        self::assertSame(0, $this->calls('hulk.sync'));
    }
}
