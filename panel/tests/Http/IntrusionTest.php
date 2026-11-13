<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Tests\Support\HttpTestCase;

/** Security → Anti-intrusion: logging, protection with a program blacklist, the program log and the operation log. */
final class IntrusionTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $this->agent->succeed('intrusion.status', [
            'enabled' => true,
            'interceptions' => 3,
            'accounts' => [
                ['name' => 'www-data', 'uid' => 33, 'kind' => 'system', 'can_login' => false, 'remark' => 'www-data (nologin)', 'log' => true, 'protect' => true, 'total' => 12, 'today' => 4, 'blocked' => 3],
                ['name' => 'shop', 'uid' => 1600, 'kind' => 'website', 'can_login' => true, 'remark' => 'shop (can login)', 'log' => false, 'protect' => false, 'total' => 0, 'today' => 0, 'blocked' => 0],
            ],
            'blacklist' => [['path' => '/usr/bin/nc.openbsd', 'exists' => true, 'denied' => ['www-data']]],
            'suggested' => ['/usr/bin/nmap'],
            'operations' => [['time' => '2026-11-12T08:25:55Z', 'text' => 'Turned the anti-intrusion master switch on']],
            'auditd' => ['installed' => true, 'active' => true, 'rules' => true, 'note' => ''],
            'acl' => ['installed' => true],
        ]);
        $this->agent->succeed('intrusion.executions', ['executions' => [
            ['time' => '2026-11-12T08:30:00Z', 'user' => 'www-data', 'uid' => 33, 'program' => '/usr/bin/nc.openbsd', 'command' => 'nc -e /bin/sh', 'blocked' => true],
            ['time' => '2026-11-12T08:29:00Z', 'user' => 'www-data', 'uid' => 33, 'program' => '/usr/bin/convert', 'command' => 'convert a.png b.jpg', 'blocked' => false],
        ], 'note' => '']);
        $this->agent->succeed('intrusion.configure', []);
        $this->agent->succeed('intrusion.blacklist', []);
    }

    public function testTabsShowTheStateAndTheLog(): void
    {
        $this->login('admin');
        foreach ([
            'overview' => ['Anti-intrusion master switch', 'id="ai-enabled" name="enabled" value="1" checked', 'www-data', 'Refused', 'name="protect[]" value="www-data"', 'Anti-Intrusion'],
            'blacklist' => ['/usr/bin/nc.openbsd', 'www-data', '/usr/bin/nmap', 'a wall', 'Remove'],
            'executions' => ['nc -e /bin/sh', 'Refused', 'convert a.png b.jpg', 'Started'],
            'operations' => ['Turned the anti-intrusion master switch on', '2026-11-12 08:25:55'],
        ] as $tab => $wants) {
            $res = $this->request('GET', '/security/intrusion', [], [], '', ['tab' => $tab]);
            self::assertSame(200, $res->status(), $tab);
            foreach ($wants as $want) {
                self::assertStringContainsString($want, $res->body(), $tab . ': ' . $want);
            }
            self::assertStringContainsString('data-nim-section-menu="security"', $res->body());
        }
        $this->request('GET', '/security/intrusion', [], [], '', ['tab' => 'executions', 'user' => 'www-data']);
        self::assertSame(['limit' => 200, 'user' => 'www-data'], $this->lastCall('intrusion.executions'));
    }

    public function testMissingToolsAreExplained(): void
    {
        $this->agent->succeed('intrusion.status', ['enabled' => false, 'accounts' => [], 'blacklist' => [], 'suggested' => [], 'operations' => [], 'interceptions' => 0,
            'auditd' => ['installed' => false, 'active' => false, 'rules' => false, 'note' => ''], 'acl' => ['installed' => false]]);
        $this->login('admin');
        $body = $this->request('GET', '/security/intrusion')->body();
        self::assertStringContainsString('Logging needs the audit system', $body);
        self::assertStringContainsString('Protection needs the ACL tools', $body);
        self::assertStringContainsString('No service or website accounts were found', $body);
    }

    public function testAdministratorsChangeItAndChangesAreAudited(): void
    {
        $this->login('admin');
        $this->post('/security/intrusion/accounts', ['enabled' => '1', 'log' => ['www-data', 'shop'], 'protect' => ['www-data']]);
        $call = $this->lastCall('intrusion.configure');
        self::assertTrue($call['enabled']);
        self::assertEquals(['www-data' => ['log' => true, 'protect' => true], 'shop' => ['log' => true, 'protect' => false]], $call['accounts']);

        $this->post('/security/intrusion/accounts', ['enabled' => '1', 'log' => ['root']]);
        self::assertSame(1, $this->calls('intrusion.configure'), 'an account the agent does not offer is refused before the agent');

        $this->post('/security/intrusion/accounts', ['enabled' => '0']);
        self::assertFalse($this->lastCall('intrusion.configure')['enabled']);

        $this->post('/security/intrusion/blacklist', ['programs' => 'nmap, /usr/bin/tcpdump', 'suggested' => ['/usr/bin/strace']]);
        self::assertSame(['/usr/bin/nc.openbsd', 'nmap', '/usr/bin/tcpdump', '/usr/bin/strace'], $this->lastCall('intrusion.blacklist')['programs']);

        $this->post('/security/intrusion/blacklist/remove', ['path' => '/usr/bin/nc.openbsd']);
        self::assertSame([], $this->lastCall('intrusion.blacklist')['programs']);
        $this->post('/security/intrusion/blacklist/remove', ['path' => '/usr/bin/other']);
        self::assertSame(2, $this->calls('intrusion.blacklist'), 'a program that is not on the list is not sent to the agent');
        $this->post('/security/intrusion/blacklist', ['programs' => '   ']);
        self::assertSame(2, $this->calls('intrusion.blacklist'), 'an empty entry is refused');

        $db = $this->app->container->get(Connection::class);
        foreach (['intrusion.configure', 'intrusion.blacklist'] as $a) {
            self::assertNotNull($db->scalar('SELECT id FROM audit_logs WHERE action = ?', [$a]), $a . ' audited');
        }
    }

    public function testOthersCannotSeeOrChangeIt(): void
    {
        $this->login('cust');
        self::assertNotSame(200, $this->request('GET', '/security/intrusion')->status());
        $this->post('/security/intrusion/accounts', ['enabled' => '1', 'log' => ['www-data']]);
        $this->post('/security/intrusion/blacklist', ['programs' => 'nc']);
        foreach (['intrusion.status', 'intrusion.executions', 'intrusion.configure', 'intrusion.blacklist'] as $a) {
            self::assertSame(0, $this->calls($a), $a);
        }
    }
}
