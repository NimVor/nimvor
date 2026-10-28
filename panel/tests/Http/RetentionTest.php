<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Services\RetentionService;
use NimVor\Tests\Support\HttpTestCase;

final class RetentionTest extends HttpTestCase
{
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->db = $this->app->container->get(Connection::class);
    }

    private function site(string $domain): int
    {
        $now = gmdate('c');
        $this->db->run(
            "INSERT INTO websites (server_id, owner_id, domain, system_user, root, document_root, kind, created_at, updated_at) VALUES (1, NULL, ?, 'u', '/home/nimvor/x', '/home/nimvor/x/public_html', 'static', ?, ?)",
            [$domain, $now, $now]
        );
        return (int) $this->db->scalar('SELECT id FROM websites WHERE domain = ?', [$domain]);
    }

    private static function ago(int $days): string
    {
        return gmdate('c', time() - $days * 86400);
    }

    public function testOldHistoryIsRemovedAndRecentOrUnfinishedIsKept(): void
    {
        // Jobs: settled long ago (removed), settled recently, never settled.
        foreach ([['a', self::ago(100)], ['b', self::ago(10)], ['c', null]] as [$id, $settled]) {
            $this->db->run("INSERT INTO agent_jobs (job_id, kind, status, created_at, settled_at) VALUES (?, 'backup.create', 'succeeded', ?, ?)", [$id, self::ago(120), $settled]);
        }
        // Deployments: website A has 25 old ones (the latest 20 are kept) and
        // one still running; website B has a single old one (kept: latest 20).
        $a = $this->site('a.test');
        $b = $this->site('b.test');
        for ($i = 0; $i < 25; $i++) {
            $this->db->run("INSERT INTO deployments (website_id, trigger, status, created_at) VALUES (?, 'manual', 'succeeded', ?)", [$a, self::ago(200 - $i)]);
        }
        $this->db->run("INSERT INTO deployments (website_id, trigger, status, created_at) VALUES (?, 'manual', 'running', ?)", [$a, self::ago(300)]);
        $this->db->run("INSERT INTO deployments (website_id, trigger, status, created_at) VALUES (?, 'manual', 'failed', ?)", [$b, self::ago(400)]);
        // Audit: one record older than a year.
        $this->db->run("INSERT INTO audit_logs (action, result, created_at) VALUES ('login', 'success', ?)", [self::ago(400)]);
        $this->db->run("INSERT INTO audit_logs (action, result, created_at) VALUES ('login', 'success', ?)", [self::ago(30)]);

        $removed = $this->app->container->get(RetentionService::class)->prune();

        self::assertSame(['jobs' => 1, 'deployments' => 6, 'audit' => 1], $removed);
        self::assertSame(['b', 'c'], array_map(static fn (array $r): string => $r['job_id'], $this->db->fetchAll('SELECT job_id FROM agent_jobs ORDER BY job_id')));
        // The latest 20 of website A: the running one (newest) and 19 others.
        self::assertSame(20, (int) $this->db->scalar('SELECT COUNT(*) FROM deployments WHERE website_id = ?', [$a]));
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM deployments WHERE status = 'running'"), 'a running deployment is never removed');
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM deployments WHERE website_id = ?', [$b]));
        // The removal is on record; the recent record stays.
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'login'"));
        $note = $this->db->fetch("SELECT details FROM audit_logs WHERE action = 'audit.pruned'");
        self::assertNotNull($note);
        self::assertSame(1, json_decode((string) $note['details'], true)['removed']);

        // Nothing left to remove: no further audit record.
        self::assertSame(['jobs' => 0, 'deployments' => 0, 'audit' => 0], $this->app->container->get(RetentionService::class)->prune());
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'audit.pruned'"));
    }

    public function testAuditRetentionZeroKeepsEverything(): void
    {
        $this->config = ['audit' => ['retention_days' => 0]];
        $this->app = $this->makeApp();
        $db = $this->app->container->get(Connection::class);
        $db->run("INSERT INTO audit_logs (action, result, created_at) VALUES ('login', 'success', ?)", [self::ago(4000)]);
        self::assertSame(0, $this->app->container->get(RetentionService::class)->prune()['audit']);
        self::assertSame(1, (int) $db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'login'"));
    }
}
