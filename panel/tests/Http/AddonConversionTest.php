<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Tests\Support\HttpTestCase;

/** Subdomains & addons → "Convert to account". */
final class AddonConversionTest extends HttpTestCase
{
    private int $shop;
    private int $addon;
    private int $sub;
    private int $custId;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('resell', 'reseller');
        $this->custId = $this->seedUser('cust', 'customer');
        $db = $this->db();
        $add = static function (string $domain, ?int $parent) use ($db): int {
            $db->run("INSERT INTO websites (server_id, domain, kind, php_version, root, document_root, system_user, owner_id, parent_id, status, created_at, updated_at) VALUES (1, ?, 'php', '8.3', ?, ?, 'shop', ?, ?, 'active', 'x', 'x')",
                [$domain, '/home/nimvor/' . $domain, '/home/nimvor/' . $domain . '/public_html', (int) $db->scalar("SELECT id FROM users WHERE username = 'cust'"), $parent]);
            return (int) $db->lastInsertId();
        };
        $this->shop = $add('shop.test', null);
        $this->addon = $add('other.test', $this->shop);
        $this->sub = $add('blog.other.test', $this->addon);
        // Databases: one of the addon's, one of the parent's; a user only
        // the addon's database uses, and one both use.
        foreach ([['other_db', $this->addon], ['shop_db', $this->shop]] as [$n, $w]) {
            $db->run("INSERT INTO databases (server_id, website_id, owner_id, name, created_at) VALUES (1, ?, ?, ?, 'x')", [$w, $this->custId, $n]);
        }
        foreach (['other_u', 'shared_u'] as $u) {
            $db->run("INSERT INTO database_users (server_id, owner_id, username, created_at) VALUES (1, ?, ?, 'x')", [$this->custId, $u]);
        }
        $g = fn (string $u, string $d) => $db->run("INSERT INTO database_grants (database_id, user_id, privileges) VALUES ((SELECT id FROM databases WHERE name = ?), (SELECT id FROM database_users WHERE username = ?), 'ALL')", [$d, $u]);
        $g('other_u', 'other_db');
        $g('shared_u', 'other_db');
        $g('shared_u', 'shop_db');
        $db->run("INSERT INTO dns_zones (server_id, name, primary_ns, hostmaster, website_id, owner_id, created_at, updated_at) VALUES (1, 'other.test', 'ns1.x', 'h.x', ?, ?, 'x', 'x')", [$this->addon, $this->custId]);
        $db->run("INSERT INTO mail_domains (server_id, domain, website_id, owner_id, created_at) VALUES (1, 'other.test', ?, ?, 'x')", [$this->addon, $this->custId]);
        $db->run("INSERT INTO cron_jobs (website_id, schedule, command, created_at, updated_at) VALUES (?, '* * * * *', 'php cron.php', 'x', 'x')", [$this->addon]);
        foreach (['sites.apply', 'sites.permissions', 'ftp.move_site', 'cron.apply', 'workers.apply'] as $a) {
            $this->agent->succeed($a, ['fixed' => 3, 'moved' => 1, 'checked' => 10, 'issues' => 3, 'counts' => [], 'sample' => [], 'skipped_count' => 0, 'dry_run' => false]);
        }
    }

    private function db(): Connection
    {
        return $this->app->container->get(Connection::class);
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    /** @return list<array<string, mixed>> */
    private function agentCalls(string $action): array
    {
        $out = [];
        foreach ($this->agent->requests as $r) {
            if ($r['path'] === '/v1/actions/' . $action) {
                $out[] = (array) json_decode((string) $r['body'], true);
            }
        }
        return $out;
    }

    private function form(): array
    {
        return ['username' => 'otheracct', 'email' => 'owner@other.test', 'password' => 'a-long-new-password-1', 'password_confirmation' => 'a-long-new-password-1'];
    }

    public function testAddonBecomesAnAccountWithWhatBelongsToIt(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/sites/' . $this->shop . '/addons')->body();
        self::assertStringContainsString('Convert to account', $page);
        self::assertStringContainsString('other_db', $page, 'the window lists what moves');

        $res = $this->post('/sites/' . $this->addon . '/convert', $this->form());
        $db = $this->db();
        $newId = (int) $db->scalar("SELECT id FROM users WHERE username = 'otheracct'");
        self::assertGreaterThan(0, $newId, (string) $this->flash('error'));
        self::assertSame('/users/' . $newId . '/edit', $res->header('Location'));
        self::assertSame('customer', $db->scalar('SELECT r.name FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = ?', [$newId]));

        $w = fn (int $id) => $db->fetch('SELECT system_user, owner_id, parent_id FROM websites WHERE id = ?', [$id]);
        self::assertSame(['system_user' => 'otheracct', 'owner_id' => $newId, 'parent_id' => null], $w($this->addon));
        self::assertSame(['system_user' => 'otheracct', 'owner_id' => $newId, 'parent_id' => $this->addon], $w($this->sub), 'its subdomain goes with it');
        self::assertSame(['system_user' => 'shop', 'owner_id' => $this->custId, 'parent_id' => null], $w($this->shop), 'the parent is untouched');

        // On the server: the new account is created (must not exist), then the rest.
        $applies = $this->agentCalls('sites.apply');
        self::assertSame('other.test', $applies[0]['domain']);
        self::assertSame('otheracct', $applies[0]['user']);
        self::assertTrue($applies[0]['new_user']);
        self::assertArrayNotHasKey('new_user', $applies[1]);
        self::assertSame(['other.test', 'blog.other.test'], array_column($this->agentCalls('sites.permissions'), 'domain'));
        self::assertSame(['otheracct', 'otheracct'], array_column($this->agentCalls('ftp.move_site'), 'user'));
        self::assertContains('otheracct', array_column($this->agentCalls('cron.apply'), 'user'));
        self::assertGreaterThan(0, count($this->agentCalls('limits.status')), 'resource limits are reconciled for the new account');

        // Ownership in the panel.
        self::assertSame($newId, (int) $db->scalar("SELECT owner_id FROM databases WHERE name = 'other_db'"));
        self::assertSame($this->custId, (int) $db->scalar("SELECT owner_id FROM databases WHERE name = 'shop_db'"));
        self::assertSame($newId, (int) $db->scalar("SELECT owner_id FROM database_users WHERE username = 'other_u'"));
        self::assertSame($this->custId, (int) $db->scalar("SELECT owner_id FROM database_users WHERE username = 'shared_u'"), 'a user the old account still uses stays');
        self::assertSame($newId, (int) $db->scalar("SELECT owner_id FROM dns_zones WHERE name = 'other.test'"));
        self::assertSame($newId, (int) $db->scalar("SELECT owner_id FROM mail_domains WHERE domain = 'other.test'"));
        self::assertSame(1, (int) $db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'account.convert_addon' AND result = 'success'"));
        self::assertStringNotContainsString('a-long-new-password-1', (string) json_encode($db->fetchAll('SELECT * FROM audit_logs')));
    }

    public function testAFailureOnTheServerPutsEverythingBack(): void
    {
        $this->agent->fail('sites.permissions', 500, 'failed', 'disk trouble');
        $this->login('admin');
        $this->post('/sites/' . $this->addon . '/convert', $this->form());
        $db = $this->db();
        self::assertSame(0, (int) $db->scalar("SELECT COUNT(*) FROM users WHERE username = 'otheracct'"), 'the new login is removed');
        self::assertSame(['system_user' => 'shop', 'owner_id' => $this->custId, 'parent_id' => $this->shop], $db->fetch('SELECT system_user, owner_id, parent_id FROM websites WHERE id = ?', [$this->addon]));
        self::assertSame($this->custId, (int) $db->scalar("SELECT owner_id FROM databases WHERE name = 'other_db'"));
        $last = $this->agentCalls('sites.apply');
        self::assertSame('shop', end($last)['user'], 'the website was applied again as the old account');
        self::assertSame(1, (int) $db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'account.convert_addon' AND result = 'failure'"));
    }

    public function testOnlyAddonsOnlyPermittedPeopleValidNames(): void
    {
        $this->login('admin');
        // A subdomain is not an addon domain; the main website neither.
        foreach ([$this->sub, $this->shop] as $id) {
            $this->post('/sites/' . $id . '/convert', $this->form());
        }
        // A name in use by a website account.
        $this->post('/sites/' . $this->addon . '/convert', ['username' => 'shop'] + $this->form());
        self::assertSame(0, count($this->agentCalls('sites.apply')));
        self::assertSame(0, (int) $this->db()->scalar("SELECT COUNT(*) FROM users WHERE username IN ('otheracct', 'shop')"));

        // Customers cannot; a reseller cannot reach another account's website.
        $this->switchTo('cust');
        self::assertStringNotContainsString('Convert to account', $this->request('GET', '/sites/' . $this->shop . '/addons')->body());
        $this->post('/sites/' . $this->addon . '/convert', $this->form());
        self::assertSame(0, count($this->agentCalls('sites.apply')), 'customers cannot convert');
        $this->switchTo('resell');
        $this->post('/sites/' . $this->addon . '/convert', $this->form());
        self::assertSame(0, count($this->agentCalls('sites.apply')));
        self::assertSame(0, (int) $this->db()->scalar("SELECT COUNT(*) FROM users WHERE username = 'otheracct'"));
    }
}
