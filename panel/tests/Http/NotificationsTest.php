<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\JobTracker;
use WhaCP\Services\MonitoringService;
use WhaCP\Services\NotificationService;
use WhaCP\Tests\Support\ArrayMailer;
use WhaCP\Tests\Support\HttpTestCase;

final class NotificationsTest extends HttpTestCase
{
    private Connection $db;
    private ArrayMailer $mailer;
    private int $admin;
    private int $reseller;
    private int $customer;
    private int $stranger;
    private int $site;
    private int $strangerSite;

    protected function setUp(): void
    {
        parent::setUp();
        $this->mailer = new ArrayMailer();
        $this->app->container->instance(\WhaCP\Mail\Mailer::class, $this->mailer);
        $this->db = $this->app->container->get(Connection::class);
        $this->admin = $this->seedUser('boss', 'admin');
        $this->reseller = $this->seedUser('res', 'reseller');
        $this->customer = $this->seedUser('cust', 'customer');
        $this->stranger = $this->seedUser('stranger', 'customer');
        $this->db->run('UPDATE users SET parent_id = ? WHERE id = ?', [$this->reseller, $this->customer]);
        $this->site = $this->website('shop.test', $this->customer);
        $this->strangerSite = $this->website('other.test', $this->stranger);
    }

    private function website(string $domain, int $owner): int
    {
        $now = gmdate('c');
        $this->db->run(
            "INSERT INTO websites (server_id, owner_id, domain, system_user, root, document_root, kind, created_at, updated_at) VALUES (1, ?, ?, 'u', '/home/whacp/x', '/home/whacp/x/public_html', 'static', ?, ?)",
            [$owner, $domain, $now, $now]
        );
        return $this->db->lastInsertId();
    }

    /** @return array<string, mixed> */
    private function user(int $id): array
    {
        return (array) $this->app->container->get(UserRepository::class)->find($id);
    }

    private function notifications(): NotificationService
    {
        return $this->app->container->get(NotificationService::class);
    }

    /** @return list<string> titles the user sees */
    private function titles(int $user): array
    {
        return array_column($this->notifications()->list($this->user($user)), 'title');
    }

    public function testEachUserSeesOnlyWhatConcernsThem(): void
    {
        $n = $this->notifications();
        $n->notify('a', 'job', 'error', 'site job', ['website_id' => $this->site]);
        $n->notify('b', 'job', 'error', 'stranger job', ['website_id' => $this->strangerSite]);
        $n->notify('c', 'alert', 'warning', 'customer quota', ['owner_id' => $this->customer]);
        $n->notify('d', 'job', 'error', 'server job');

        self::assertEqualsCanonicalizing(['site job', 'stranger job', 'customer quota', 'server job'], $this->titles($this->admin));
        self::assertEqualsCanonicalizing(['site job', 'customer quota'], $this->titles($this->reseller), 'reseller: own customers');
        self::assertEqualsCanonicalizing(['site job', 'customer quota'], $this->titles($this->customer));
        self::assertSame(['stranger job'], $this->titles($this->stranger));

        // A website that changes hands takes its history with it.
        $this->db->run('UPDATE websites SET owner_id = ? WHERE id = ?', [$this->stranger, $this->site]);
        self::assertEqualsCanonicalizing(['site job', 'stranger job'], $this->titles($this->stranger));
        self::assertSame(['customer quota'], $this->titles($this->customer));

        // Recorded once per dedupe key.
        self::assertNull($n->notify('a', 'job', 'error', 'again', ['website_id' => $this->site]));
        // Deleting the website removes its notifications.
        $this->db->run('DELETE FROM websites WHERE id = ?', [$this->site]);
        self::assertNotContains('site job', $this->titles($this->admin));
    }

    public function testReadStateIsPerUserAndLimitedToVisibleNotifications(): void
    {
        $n = $this->notifications();
        $mine = (int) $n->notify('a', 'job', 'error', 'site job', ['website_id' => $this->site]);
        $theirs = (int) $n->notify('b', 'job', 'error', 'stranger job', ['website_id' => $this->strangerSite]);
        $cust = $this->user($this->customer);
        self::assertSame(1, $n->unreadCount($cust));
        self::assertSame(0, $n->markRead($cust, [$theirs]), 'cannot mark what it cannot see');
        self::assertSame(1, $n->markRead($cust, null));
        self::assertSame(0, $n->unreadCount($cust));
        self::assertSame(1, $n->unreadCount($this->user($this->reseller)), 'read state is per user');
        self::assertSame(2, $n->unreadCount($this->user($this->admin)));
        self::assertSame(1, $n->markRead($this->user($this->admin), [$mine]));
        self::assertSame(1, $n->unreadCount($this->user($this->admin)));
    }

    public function testFinishedJobsAreReportedOnceWithSecretsRemoved(): void
    {
        $jobs = $this->app->container->get(JobTracker::class);
        $jobs->track('aaaaaaaaaaaaaaaa', 'Backup shop.test', 'website', (string) $this->site, $this->admin);
        $jobs->track('bbbbbbbbbbbbbbbb', 'Cron: php artisan schedule:run', 'website', (string) $this->site, $this->customer);
        $jobs->track('cccccccccccccccc', 'Install PHP 8.4', 'php', '8.4', $this->admin);

        // Nobody watches them: the scheduler pass settles them.
        $this->agent->succeed('jobs.get', ['status' => 'running']);
        self::assertSame([], $jobs->settlePending());
        $this->agent->succeed('jobs.get', ['status' => 'failed', 'error' => 'upload refused: password=hunter2 token=abc123']);
        self::assertCount(3, $jobs->settlePending());
        self::assertSame([], $jobs->settlePending(), 'settled once');

        $rows = $this->notifications()->list($this->user($this->admin));
        self::assertCount(3, $rows);
        foreach ($rows as $r) {
            self::assertSame('error', $r['severity']);
            self::assertStringNotContainsString('hunter2', $r['body']);
            self::assertStringNotContainsString('abc123', $r['body']);
        }
        // The website owner sees their website's jobs (even one an admin
        // started) and may open its log; not the server job.
        self::assertEqualsCanonicalizing(['Backup shop.test failed', 'Cron: php artisan schedule:run failed'], $this->titles($this->customer));
        self::assertTrue($jobs->visibleTo((array) $jobs->find('aaaaaaaaaaaaaaaa'), $this->user($this->customer)));
        self::assertFalse($jobs->visibleTo((array) $jobs->find('cccccccccccccccc'), $this->user($this->customer)));
        self::assertFalse($jobs->visibleTo((array) $jobs->find('aaaaaaaaaaaaaaaa'), $this->user($this->stranger)));
        self::assertSame([], $this->titles($this->stranger));
    }

    public function testQuickJobsOnlyReportFailuresLongJobsAlsoSuccess(): void
    {
        $jobs = $this->app->container->get(JobTracker::class);
        $jobs->track('aaaaaaaaaaaaaaaa', 'Deploy shop.test', 'website', (string) $this->site, $this->customer);
        $jobs->track('bbbbbbbbbbbbbbbb', 'Cron: echo hi', 'website', (string) $this->site, $this->customer);
        $this->agent->succeed('jobs.get', ['status' => 'succeeded']);
        $jobs->settlePending();
        self::assertSame(['Deploy shop.test finished'], $this->titles($this->customer));
        self::assertSame([], $this->mailer->sent, 'successes are not emailed');

        // A job someone watches is settled by the poll, and not again later.
        $jobs->track('cccccccccccccccc', 'Restore shop.test', 'website', (string) $this->site, $this->customer);
        $this->agent->succeed('jobs.log', ['job' => ['status' => 'failed', 'error' => 'disk full'], 'log' => '', 'offset' => 0, 'done' => true]);
        $jobs->poll('cccccccccccccccc', 0);
        $jobs->poll('cccccccccccccccc', 0);
        $this->agent->succeed('jobs.get', ['status' => 'failed', 'error' => 'disk full']);
        $jobs->settlePending();
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM notifications WHERE title = 'Restore shop.test failed'"));

        // A job the agent forgot (restart) is closed without a notification.
        $jobs->track('dddddddddddddddd', 'Backup shop.test', 'website', (string) $this->site, $this->customer);
        $this->agent->fail('jobs.get', 404, 'not_found', 'no such job');
        $jobs->settlePending();
        self::assertSame('unknown', $this->db->scalar("SELECT status FROM agent_jobs WHERE job_id = 'dddddddddddddddd'"));
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM notifications WHERE job_id = 'dddddddddddddddd'"));
    }

    public function testFailuresAreEmailedToOptedInUsersWhoMaySeeThem(): void
    {
        $this->db->run('UPDATE users SET notify_email = 1 WHERE id IN (?, ?, ?)', [$this->customer, $this->stranger, $this->admin]);
        $n = $this->notifications();
        $n->notify('a', 'job', 'error', 'Backup shop.test failed', ['website_id' => $this->site, 'body' => 'disk full']);
        $to = array_merge(...array_column($this->mailer->sent, 'to'));
        self::assertEqualsCanonicalizing(['cust@example.test', 'boss@example.test'], $to, 'one message each, only to those who may see it');
        self::assertCount(2, $this->mailer->sent);
        self::assertStringContainsString('disk full', $this->mailer->sent[0]['text']);

        $this->mailer->sent = [];
        $n->notify('b', 'job', 'success', 'Backup shop.test finished', ['website_id' => $this->site]);
        self::assertSame([], $this->mailer->sent);
        $this->db->run('UPDATE users SET is_active = 0 WHERE id = ?', [$this->customer]);
        $n->notify('c', 'job', 'error', 'Deploy shop.test failed', ['website_id' => $this->site]);
        self::assertSame([['boss@example.test']], array_column($this->mailer->sent, 'to'), 'disabled users get nothing');
    }

    public function testOldNotificationsArePruned(): void
    {
        $n = $this->notifications();
        $n->notify('old', 'job', 'error', 'old', ['website_id' => $this->site]);
        $n->notify('new', 'job', 'error', 'new', ['website_id' => $this->site]);
        $this->db->run("UPDATE notifications SET created_at = ? WHERE dedupe_key = 'old'", [gmdate('c', time() - 91 * 86400)]);
        self::assertSame(1, $n->prune());
        self::assertSame(['new'], $this->titles($this->admin));
    }

    public function testMonitoringAlertsReachTheWebsiteOwner(): void
    {
        $this->agent->succeed('system.info', ['system' => ['cpu' => ['cores' => 2]]]);
        $this->agent->succeed('system.metrics', ['cpu_percent' => 1, 'memory' => ['used_percent' => 10], 'load' => ['load1' => 0.1], 'disks' => [['mount_point' => '/', 'used_percent' => 99]], 'network' => []]);
        $this->agent->succeed('services.status', []);
        $this->agent->succeed('sites.usage', []);
        $this->agent->succeed('discovery.certificates', [['name' => 'shop.test', 'certificate' => ['days_remaining' => 2]]]);
        $mon = $this->app->container->get(MonitoringService::class);
        $mon->sender = static fn (): bool => true;
        $mon->run();
        self::assertSame(['Certificate shop.test expires in 2 day(s).'], $this->titles($this->customer), 'not the disk alert');
        self::assertCount(2, $this->titles($this->admin));
        self::assertSame([], $this->titles($this->stranger));

        $this->agent->succeed('discovery.certificates', []);
        $mon->run(time() + 600);
        self::assertContains('Resolved: Certificate shop.test expires in 2 day(s).', $this->titles($this->customer));
    }

    public function testPageShowsOwnNotificationsEscapedAndMarksRead(): void
    {
        $n = $this->notifications();
        $mine = (int) $n->notify('a', 'job', 'error', 'Backup shop.test failed <script>alert(1)</script>', ['website_id' => $this->site, 'job_id' => 'aaaaaaaaaaaaaaaa', 'link' => '/sites/' . $this->site]);
        $theirs = (int) $n->notify('b', 'job', 'error', 'Backup other.test failed', ['website_id' => $this->strangerSite]);
        $n->notify('c', 'job', 'error', 'Evil link', ['website_id' => $this->site, 'link' => '//evil.example/x']);
        $this->login('cust');

        $page = $this->request('GET', '/notifications')->body();
        self::assertStringContainsString('Backup shop.test failed &lt;script&gt;', $page);
        self::assertStringNotContainsString('<script>alert(1)', $page);
        self::assertStringNotContainsString('other.test', $page);
        self::assertStringContainsString('data-wcp-job-open="aaaaaaaaaaaaaaaa"', $page);
        self::assertStringNotContainsString('evil.example', $page, 'only local links are shown');
        self::assertMatchesRegularExpression('/data-wcp-bell-count>2</', $page, 'unread count in the top bar');

        // Marking another tenant's notification read does nothing.
        $this->post('/notifications/read', ['ids' => [(string) $theirs]]);
        self::assertSame(2, $n->unreadCount($this->user($this->customer)));
        self::assertNull($this->db->scalar('SELECT read_at FROM notification_reads WHERE notification_id = ?', [$theirs]));
        $this->post('/notifications/read', ['ids' => [(string) $mine]]);
        self::assertSame(1, $n->unreadCount($this->user($this->customer)));
        self::assertStringNotContainsString('failed &lt;script&gt;', $this->request('GET', '/notifications')->body(), 'unread view hides read items');
        self::assertStringContainsString('failed &lt;script&gt;', $this->request('GET', '/notifications', [], [], '', ['show' => 'all'])->body());
        $this->post('/notifications/read', ['all' => '1']);
        self::assertSame(0, $n->unreadCount($this->user($this->customer)));
        self::assertSame(1, $n->unreadCount($this->user($this->stranger)), 'the stranger still has their own unread');
    }

    public function testSummaryPollDoesNotKeepAnIdleSessionAlive(): void
    {
        $this->notifications()->notify('a', 'job', 'error', 'x', ['website_id' => $this->site]);
        $this->login('cust');
        $this->session->set('_auth_last_activity', time() - 600);
        $res = $this->request('GET', '/notifications/summary', [], ['accept' => 'application/json']);
        self::assertSame(200, $res->status());
        self::assertSame(1, json_decode($res->body(), true)['unread']);
        self::assertLessThan(time() - 500, (int) $this->session->get('_auth_last_activity'), 'the poll is not activity');
        // Each request gets its own application, as under PHP-FPM.
        $this->app = $this->makeApp();
        $this->app->container->instance(\WhaCP\Mail\Mailer::class, $this->mailer);
        $this->request('GET', '/notifications');
        self::assertGreaterThan(time() - 5, (int) $this->session->get('_auth_last_activity'), 'a page view is');
    }

    /** The bell's dropdown: newest items (read or not), local links only, marked read over AJAX. */
    public function testBellDropdownItems(): void
    {
        $n = $this->notifications();
        $n->notify('b1', 'job', 'error', 'Backup <b>x</b> failed', ['website_id' => $this->site, 'link' => '/backups']);
        $n->notify('b2', 'job', 'warning', 'Odd link', ['website_id' => $this->site, 'link' => '//evil.test/x']);
        $this->login('cust');
        $json = ['accept' => 'application/json'];
        $res = json_decode($this->request('GET', '/notifications/summary', [], $json)->body(), true);
        $items = array_column($res['items'], null, 'title');
        self::assertSame('/backups', $items['Backup <b>x</b> failed']['link']);
        self::assertSame('', $items['Odd link']['link'], 'only links inside the panel');
        self::assertFalse($items['Odd link']['read']);
        $csrf = ['accept' => 'application/json', 'content-type' => 'application/json', 'x-csrf-token' => $this->app->container->get(\WhaCP\Security\Csrf::class)->token()];
        $read = json_decode($this->request('POST', '/notifications/read', [], $csrf, json_encode(['ids' => [$items['Odd link']['id']]]))->body(), true);
        self::assertSame(1, $read['unread']);
        $res = json_decode($this->request('GET', '/notifications/summary', [], $json)->body(), true);
        self::assertTrue(array_column($res['items'], null, 'title')['Odd link']['read'], 'read items stay in the list, marked read');
        // The layout has the dropdown with a link to the full page.
        $page = $this->request('GET', '/notifications')->body();
        self::assertStringContainsString('data-wcp-bell-menu', $page);
        self::assertStringContainsString('View all notifications', $page);
    }

    public function testEmailPreference(): void
    {
        $this->login('cust');
        $this->post('/notifications/email', ['notify_email' => '1']);
        self::assertSame(1, (int) $this->db->scalar('SELECT notify_email FROM users WHERE id = ?', [$this->customer]));
        self::assertStringContainsString('checked', $this->request('GET', '/notifications')->body());
        $this->post('/notifications/email', ['notify_email' => '0']);
        self::assertSame(0, (int) $this->db->scalar('SELECT notify_email FROM users WHERE id = ?', [$this->customer]));
    }

    public function testApi(): void
    {
        $n = $this->notifications();
        $mine = (int) $n->notify('a', 'job', 'error', 'Backup shop.test failed', ['website_id' => $this->site, 'job_id' => 'aaaaaaaaaaaaaaaa']);
        $n->notify('b', 'job', 'error', 'Backup other.test failed', ['website_id' => $this->strangerSite]);
        $tokens = $this->app->container->get(\WhaCP\Services\ApiTokenService::class);
        $read = $tokens->create($this->customer, ['name' => 'r', 'ability' => 'read', 'expires_days' => 30]);
        $write = $tokens->create($this->customer, ['name' => 'w', 'ability' => 'write', 'expires_days' => 30]);
        $api = fn (string $method, string $path, string $token, array $json = [], array $query = []) => $this->app->handle(new \WhaCP\Http\Request(
            $method, $path, $query, [], ['REMOTE_ADDR' => '192.0.2.50'], ['content-type' => 'application/json', 'authorization' => 'Bearer ' . $token], $json === [] ? '' : (string) json_encode($json)
        ));
        $res = $api('GET', '/api/v1/notifications', $read);
        self::assertSame(200, $res->status(), $res->body());
        $data = json_decode($res->body(), true)['data'];
        self::assertSame(['Backup shop.test failed'], array_column($data, 'title'));
        self::assertSame('aaaaaaaaaaaaaaaa', $data[0]['job_id']);
        self::assertFalse($data[0]['read']);
        self::assertSame(403, $api('POST', '/api/v1/notifications/read', $read, ['all' => true])->status(), 'read-only token');
        $res = $api('POST', '/api/v1/notifications/read', $write, ['ids' => [$mine]]);
        self::assertSame(['marked' => 1, 'unread' => 0], json_decode($res->body(), true)['data']);
        self::assertSame([], json_decode($api('GET', '/api/v1/notifications', $read, [], ['unread' => '1'])->body(), true)['data']);
    }

    public function testRolesWithoutThePermissionAreRefused(): void
    {
        $this->db->run("INSERT INTO roles (name, label, description, is_system, scope, created_at) VALUES ('nonotify', 'No notifications', '', 0, 'own', ?)", [gmdate('c')]);
        $role = (int) $this->db->lastInsertId();
        $this->db->run("INSERT INTO role_permissions (role_id, permission_id) SELECT ?, id FROM permissions WHERE name IN ('dashboard.view', 'websites.view')", [$role]);
        $uid = $this->seedUser('quiet', 'customer');
        $this->db->run('UPDATE users SET role_id = ? WHERE id = ?', [$role, $uid]);
        $this->db->run('UPDATE websites SET owner_id = ? WHERE id = ?', [$uid, $this->site]);
        $id = (int) $this->notifications()->notify('a', 'job', 'error', 'x', ['website_id' => $this->site]);
        $this->login('quiet');
        self::assertSame(403, $this->request('GET', '/notifications')->status());
        self::assertSame(403, $this->request('GET', '/notifications/summary', [], ['accept' => 'application/json'])->status());
        self::assertSame(403, $this->post('/notifications/read', ['all' => '1'])->status());
        self::assertSame(403, $this->post('/notifications/email', ['notify_email' => '1'])->status());
        self::assertNull($this->db->scalar('SELECT read_at FROM notification_reads WHERE notification_id = ?', [$id]));
        self::assertStringNotContainsString('data-wcp-bell', $this->request('GET', '/')->body());
        $token = $this->app->container->get(\WhaCP\Services\ApiTokenService::class)->create($uid, ['name' => 't', 'ability' => 'write', 'expires_days' => 1]);
        $res = $this->app->handle(new \WhaCP\Http\Request('GET', '/api/v1/notifications', [], [], ['REMOTE_ADDR' => '192.0.2.50'], ['authorization' => 'Bearer ' . $token], ''));
        self::assertSame(403, $res->status());
    }
}
