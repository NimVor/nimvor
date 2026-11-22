<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Services\Mailer;
use NimVorSite\Tests\Support\SiteTestCase;

final class TicketsTest extends SiteTestCase
{
    /** @return array{id: int, ref: string, token: string} */
    private function open(?int $user = null, string $email = 'grace@example.test', string $category = 'support'): array
    {
        return $this->app->ticketService->open($user, 'Grace', $email, 'Cannot log in', 'I cannot sign in to the panel at all.', $category);
    }

    private function customerMessages(): int
    {
        return (int) $this->row("SELECT COUNT(*) c FROM ticket_replies WHERE author = 'customer'")['c'];
    }

    private function url(array $t, string $tail = ''): string
    {
        return '/support/tickets/' . $t['ref'] . '/' . $t['token'] . $tail;
    }

    public function testTheContactFormOpensATicketAndSendsTheVisitorToItsPrivatePage(): void
    {
        $res = $this->post('/support/contact', ['name' => 'Grace', 'email' => 'grace@example.test', 'subject' => 'A question', 'message' => 'Hello, I have a question about NimVor.', 'category' => 'sales']);
        self::assertSame(303, $res->status);
        $page = $this->get($res->headers['Location']);
        self::assertSame(200, $page->status);
        self::assertStringContainsString('Hello, I have a question', $page->body);
        self::assertSame('sales', $this->row('SELECT category FROM tickets')['category']);
        self::assertMatchesRegularExpression('/^NV-[A-Z0-9]{8}$/', (string) $this->row('SELECT ref FROM tickets')['ref']);
    }

    public function testASecurityReportStartsAtHighPriorityAndAnUnknownCategoryBecomesGeneral(): void
    {
        $this->open(null, 'a@example.test', 'security');
        $this->open(null, 'b@example.test', 'nonsense');
        self::assertSame('high', $this->row("SELECT priority FROM tickets WHERE email = 'a@example.test'")['priority']);
        self::assertSame('general', $this->row("SELECT category FROM tickets WHERE email = 'b@example.test'")['category']);
    }

    public function testThePrivateLinkNeedsTheRightTokenAndIsNotIndexed(): void
    {
        $t = $this->open();
        self::assertSame(200, $this->get($this->url($t))->status);
        self::assertSame(404, $this->get('/support/tickets/' . $t['ref'] . '/' . str_repeat('a', 32))->status);
        self::assertSame(404, $this->get('/support/tickets/NV-NOPE0000/' . $t['token'])->status);
        self::assertStringContainsString('noindex', $this->get($this->url($t))->body);
    }

    public function testTheCustomerCanAnswerAndTheTicketIsOpenAgain(): void
    {
        $t = $this->open();
        $this->app->tickets->setStatus($t['id'], 'answered');
        $this->post($this->url($t, '/reply'), ['body' => 'Here is more detail on the problem.']);
        self::assertSame('open', $this->row('SELECT status FROM tickets')['status']);
        self::assertSame(2, $this->customerMessages());
        self::assertSame(422, $this->post($this->url($t, '/reply'), ['body' => 'x'])->status);
    }

    public function testClosingAndReopeningWithinTheWindow(): void
    {
        $t = $this->open();
        $this->post($this->url($t, '/close'));
        self::assertSame('closed', $this->row('SELECT status FROM tickets')['status']);
        $this->post($this->url($t, '/reply'), ['body' => 'It is still happening for me.']);
        self::assertSame('open', $this->row('SELECT status FROM tickets')['status'], 'a closed ticket can be reopened by answering');
        $this->post($this->url($t, '/close'));
        $this->app->db->run("UPDATE tickets SET closed_at = '2000-01-01 00:00:00' WHERE id = ?", [$t['id']]);
        $this->post($this->url($t, '/reply'), ['body' => 'Writing after a very long time.']);
        self::assertSame('closed', $this->row('SELECT status FROM tickets')['status'], 'too old to reopen');
        self::assertSame(2, $this->customerMessages());
    }

    public function testStaffAnswersAreSeenByTheCustomerButInternalNotesAreNot(): void
    {
        $t = $this->open();
        $this->loginAs('admin', 'boss@example.test');
        $this->post("/admin/tickets/{$t['id']}/reply", ['mode' => 'reply', 'body' => 'Please reset your password.', 'status' => 'answered']);
        $this->post("/admin/tickets/{$t['id']}/reply", ['mode' => 'note', 'body' => 'Probably the account is locked.', 'status' => 'answered']);
        self::assertSame('answered', $this->row('SELECT status FROM tickets')['status']);
        self::assertNotNull($this->row('SELECT first_response_at FROM tickets')['first_response_at']);
        $admin = $this->get("/admin/tickets/{$t['id']}")->body;
        self::assertStringContainsString('Probably the account is locked.', $admin);
        $this->post('/logout');
        $guest = $this->get($this->url($t))->body;
        self::assertStringContainsString('Please reset your password.', $guest);
        self::assertStringNotContainsString('account is locked', $guest);
    }

    public function testAClientOnlySeesTheirOwnTickets(): void
    {
        $a = $this->createUser('a@example.test', 'Alice');
        $b = $this->createUser('b@example.test', 'Bob');
        $ta = $this->app->ticketService->open($a, 'Alice', 'a@example.test', 'Alice subject', 'Alice private text here');
        $tb = $this->app->ticketService->open($b, 'Bob', 'b@example.test', 'Bob subject', 'Bob private text here');
        $this->login('a@example.test');
        self::assertStringContainsString('Alice subject', $this->get('/account/tickets')->body);
        self::assertStringNotContainsString('Bob subject', $this->get('/account/tickets')->body);
        self::assertSame(200, $this->get('/account/tickets/' . $ta['ref'])->status);
        self::assertSame(404, $this->get('/account/tickets/' . $tb['ref'])->status);
        self::assertSame(404, $this->post('/account/tickets/' . $tb['ref'] . '/reply', ['body' => 'Hacking in here now.'])->status);
    }

    public function testASignedInPersonOpensATicketFromTheirAccount(): void
    {
        $id = $this->createUser('ada@example.test', 'Ada');
        $this->login('ada@example.test');
        $res = $this->post('/account/tickets/new', ['subject' => 'Billing question', 'message' => 'How do I change my plan?', 'category' => 'sales']);
        self::assertSame(303, $res->status);
        self::assertSame($id, (int) $this->row('SELECT user_id FROM tickets')['user_id']);
    }

    public function testTheAdminListFiltersAndSearches(): void
    {
        $this->open(null, 'one@example.test', 'support');
        $this->open(null, 'two@example.test', 'security');
        $this->loginAs('admin', 'boss@example.test');
        $all = $this->get('/admin/tickets')->body;
        self::assertStringContainsString('one@example.test', $all);
        self::assertStringContainsString('two@example.test', $all);
        $sec = $this->get('/admin/tickets?category=security')->body;
        self::assertStringContainsString('two@example.test', $sec);
        self::assertStringNotContainsString('one@example.test', $sec);
        self::assertStringContainsString('one@example.test', $this->get('/admin/tickets?q=one%40example')->body);
    }

    public function testBulkActionsAndDetailsAreAppliedAndRecorded(): void
    {
        $a = $this->open(null, 'one@example.test');
        $b = $this->open(null, 'two@example.test');
        $me = $this->loginAs('admin', 'boss@example.test');
        $this->post('/admin/tickets/bulk', ['ids' => [(string) $a['id'], (string) $b['id']], 'action' => 'assign_me']);
        self::assertSame(2, (int) $this->row('SELECT COUNT(*) c FROM tickets WHERE assigned_to = ?', [$me])['c']);
        $this->post('/admin/tickets/bulk', ['ids' => [(string) $a['id']], 'action' => 'resolve']);
        self::assertSame('resolved', $this->row('SELECT status FROM tickets WHERE id = ?', [$a['id']])['status']);
        $this->post("/admin/tickets/{$b['id']}/update", ['status' => 'on_hold', 'priority' => 'urgent', 'category' => 'press', 'assignee' => 'none']);
        $row = $this->row('SELECT * FROM tickets WHERE id = ?', [$b['id']]);
        self::assertSame(['on_hold', 'urgent', 'press', null], [$row['status'], $row['priority'], $row['category'], $row['assigned_to']]);
        self::assertGreaterThanOrEqual(1, (int) $this->row("SELECT COUNT(*) c FROM ticket_replies WHERE ticket_id = ? AND author = 'system'", [$b['id']])['c']);
        $before = $this->rows('tickets');
        $this->post('/admin/tickets/bulk', ['ids' => [(string) $a['id']], 'action' => 'drop-everything']);
        self::assertSame($before, $this->rows('tickets'));
    }

    public function testCannedRepliesCanBeAddedAndDeleted(): void
    {
        $this->loginAs('admin', 'boss@example.test');
        $this->post('/admin/tickets/canned', ['title' => 'Reset password', 'body' => 'Use the reset link on the sign-in page.']);
        self::assertSame(1, $this->rows('ticket_canned'));
        $t = $this->open();
        self::assertStringContainsString('Use the reset link', $this->get("/admin/tickets/{$t['id']}")->body);
        $id = (int) $this->row('SELECT id FROM ticket_canned')['id'];
        $this->post("/admin/tickets/canned/{$id}/delete");
        self::assertSame(0, $this->rows('ticket_canned'));
    }

    public function testResolvedTicketsCloseThemselvesAfterAWeekAndNotBefore(): void
    {
        $a = $this->open(null, 'one@example.test');
        $b = $this->open(null, 'two@example.test');
        $this->app->tickets->setStatus($a['id'], 'resolved');
        $this->app->tickets->setStatus($b['id'], 'resolved');
        $this->app->db->run("UPDATE tickets SET updated_at = DATE_SUB(UTC_TIMESTAMP(), INTERVAL 8 DAY) WHERE id = ?", [$a['id']]);
        self::assertSame(1, $this->app->tickets->autoClose(7));
        self::assertSame('closed', $this->row('SELECT status FROM tickets WHERE id = ?', [$a['id']])['status']);
        self::assertSame('resolved', $this->row('SELECT status FROM tickets WHERE id = ?', [$b['id']])['status']);
    }

    public function testMailGoesToTheCustomerAndTheTeamWhenAMailerIsSet(): void
    {
        $sent = [];
        $this->app->ticketService->setMailer(new class ($sent) implements Mailer {
            /** @param array<int, array<string, string>> $log */
            public function __construct(public array &$log)
            {
            }

            public function send(string $to, string $subject, string $body): bool
            {
                $this->log[] = ['to' => $to, 'subject' => $subject, 'body' => $body];
                return true;
            }
        });
        $t = $this->open();
        self::assertSame('grace@example.test', $sent[0]['to']);
        self::assertStringContainsString($t['ref'], $sent[0]['subject']);
        self::assertStringContainsString($this->url($t), $sent[0]['body']);
    }

    public function testTheOldMessagePagesRedirectToTickets(): void
    {
        $this->loginAs('admin', 'boss@example.test');
        $r = $this->get('/admin/messages');
        self::assertSame([301, '/admin/tickets'], [$r->status, $r->headers['Location']]);
    }
}
