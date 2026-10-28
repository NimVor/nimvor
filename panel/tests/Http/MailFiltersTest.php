<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Tests\Support\HttpTestCase;

/** Email filters, autoresponder and mail routing. */
final class MailFiltersTest extends HttpTestCase
{
    private Connection $db;
    private int $domain;
    private int $box;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $this->seedUser('other', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->db->run("INSERT INTO mail_domains (server_id, domain, owner_id, dkim_selector, dkim_txt, created_at) VALUES (1, 'shop.test', ?, 's', 't', 'x')", [$cust]);
        $this->domain = (int) $this->db->lastInsertId();
        $this->db->run("INSERT INTO mail_mailboxes (domain_id, local, quota_mb, created_at) VALUES (?, 'me', 0, 'x')", [$this->domain]);
        $this->box = (int) $this->db->lastInsertId();
        $this->agent->succeed('mail.filters_get', ['address' => 'me@shop.test', 'filters' => [
            ['name' => 'Old', 'enabled' => true, 'any' => false, 'rules' => [['part' => 'from', 'match' => 'is', 'value' => 'a@b.c']], 'actions' => [['type' => 'discard']]],
        ], 'responder' => ['enabled' => false, 'subject' => '', 'body' => '', 'days' => 1]]);
        $this->agent->succeed('mail.filters_save', ['ok' => true]);
        $this->agent->succeed('mail.routing_set', ['changed' => true]);
    }

    private function url(string $tail = ''): string
    {
        return '/mail/domains/' . $this->domain . '/mailboxes/' . $this->box . '/filters' . $tail;
    }

    public function testTheOwnerManagesFiltersAndTheAutoresponder(): void
    {
        $this->login('cust');
        $page = $this->request('GET', $this->url())->body();
        foreach (['Filters for', 'me@shop.test', '>Old<', 'From is “a@b.c”', 'id="filter-new"', 'Autoresponder'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $this->post($this->url(), [
            'name' => 'Invoices', 'any' => '0',
            'rules' => [['part' => 'subject', 'match' => 'contains', 'value' => 'invoice'], ['part' => 'from', 'match' => 'is', 'value' => '']],
            'actions' => [['type' => 'fileinto', 'value' => 'Invoices'], ['type' => '', 'value' => '']],
        ]);
        $sent = $this->lastCall('mail.filters_save');
        self::assertSame('me@shop.test', $sent['address']);
        self::assertCount(2, $sent['filters'], 'appended to the existing filter');
        self::assertSame(['name' => 'Invoices', 'enabled' => true, 'any' => false, 'rules' => [['part' => 'subject', 'match' => 'contains', 'value' => 'invoice']], 'actions' => [['type' => 'fileinto', 'value' => 'Invoices']]], $sent['filters'][1]);

        // Editing by index replaces; toggling and deleting act on one filter.
        $this->post($this->url(), ['index' => '0', 'name' => 'Renamed', 'rules' => [['part' => 'to', 'match' => 'contains', 'value' => 'x']], 'actions' => [['type' => 'stop']]]);
        self::assertSame('Renamed', $this->lastCall('mail.filters_save')['filters'][0]['name']);
        self::assertCount(1, $this->lastCall('mail.filters_save')['filters']);
        $this->post($this->url('/0/toggle'));
        self::assertFalse($this->lastCall('mail.filters_save')['filters'][0]['enabled']);
        $this->post($this->url('/0/delete'));
        self::assertSame([], $this->lastCall('mail.filters_save')['filters']);

        $this->post($this->url(), ['name' => '', 'rules' => [], 'actions' => []]);
        self::assertStringContainsString('Name the filter', (string) $this->flash('error'));

        $this->post('/mail/domains/' . $this->domain . '/mailboxes/' . $this->box . '/responder', ['enabled' => '1', 'subject' => 'Away', 'body' => "Back Monday.\nThanks", 'days' => '3', 'start' => '2026-10-01', 'end' => '']);
        $r = $this->lastCall('mail.filters_save')['responder'];
        self::assertSame([true, 'Away', "Back Monday.\nThanks", 3, '2026-10-01'], [$r['enabled'], $r['subject'], $r['body'], $r['days'], $r['start']]);
        self::assertSame('mail.filters', $this->db->scalar("SELECT action FROM audit_logs WHERE action = 'mail.filters'"));
    }

    public function testOtherAccountsCannotSeeOrChangeTheFilters(): void
    {
        $this->login('other');
        self::assertSame(404, $this->request('GET', $this->url())->status());
        $this->post($this->url(), ['name' => 'x', 'rules' => [['part' => 'subject', 'match' => 'is', 'value' => 'y']], 'actions' => [['type' => 'discard']]]);
        $this->post('/mail/domains/' . $this->domain . '/routing', ['routing' => 'remote']);
        self::assertSame(0, $this->calls('mail.filters_save'));
        self::assertSame(0, $this->calls('mail.routing_set'));
        // Another domain's mailbox cannot be reached through this domain.
        $this->db->run("INSERT INTO mail_domains (server_id, domain, owner_id, dkim_selector, dkim_txt, created_at) VALUES (1, 'mine.test', (SELECT id FROM users WHERE username = 'other'), 's', 't', 'x')");
        $mine = (int) $this->db->lastInsertId();
        self::assertSame(404, $this->request('GET', '/mail/domains/' . $mine . '/mailboxes/' . $this->box . '/filters')->status());
    }

    public function testMailRouting(): void
    {
        $this->login('cust');
        self::assertStringContainsString('id="routing"', $this->request('GET', '/mail/domains/' . $this->domain)->body());
        $this->post('/mail/domains/' . $this->domain . '/routing', ['routing' => 'backup']);
        self::assertSame(['domain' => 'shop.test', 'routing' => 'backup'], $this->lastCall('mail.routing_set'));
        self::assertSame('backup', $this->db->scalar('SELECT routing FROM mail_domains WHERE id = ?', [$this->domain]));
        $this->post('/mail/domains/' . $this->domain . '/routing', ['routing' => 'nowhere']);
        self::assertSame(1, $this->calls('mail.routing_set'));
    }
}
