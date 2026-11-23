<?php

declare(strict_types=1);

namespace NimVorSite\Http;

use NimVorSite\Core\Ctx;
use NimVorSite\Core\Response;
use NimVorSite\Repositories\Tickets;
use NimVorSite\Services\TicketService;
use NimVorSite\Support\Validator;

/**
 * Tickets from three sides: a visitor with the private link, a signed-in customer, and the administrators.
 * Every change is a POST with a CSRF token (the router checks it); a customer only ever sees their own tickets
 * and never the internal notes.
 */
final class TicketController extends Controller
{
    // ---------------------------------------------------------------- visitor with the private link

    /** @param array<string, string> $params */
    public function guestShow(Ctx $ctx, array $params): Response
    {
        $t = $this->guestTicket($params);
        if ($t === null) {
            return $this->app->error($ctx, 404);
        }
        return $ctx->render('pages/ticket', $this->thread($t, '/support/tickets/' . $t['ref'] . '/' . $t['access_token']));
    }

    /** @param array<string, string> $params */
    public function guestReply(Ctx $ctx, array $params): Response
    {
        $t = $this->guestTicket($params);
        if ($t === null) {
            return $this->app->error($ctx, 404);
        }
        return $this->reply($ctx, $t, null, '/support/tickets/' . $t['ref'] . '/' . $t['access_token'], 'pages/ticket');
    }

    /** @param array<string, string> $params */
    public function guestClose(Ctx $ctx, array $params): Response
    {
        $t = $this->guestTicket($params);
        if ($t === null) {
            return $this->app->error($ctx, 404);
        }
        $this->app->ticketService->customerClose($t);
        $ctx->flash('success', 'The ticket was closed.');
        return $ctx->redirect('/support/tickets/' . $t['ref'] . '/' . $t['access_token']);
    }

    // ---------------------------------------------------------------- signed-in customer

    public function accountList(Ctx $ctx): Response
    {
        return $ctx->render('account/tickets', ['tickets' => $this->app->tickets->forUser((int) $ctx->user()['id'])]);
    }

    public function accountNew(Ctx $ctx): Response
    {
        return $ctx->render('account/ticket-new', ['old' => ['category' => 'support'], 'categories' => Tickets::CATEGORIES]);
    }

    public function accountCreate(Ctx $ctx): Response
    {
        $user = (array) $ctx->user();
        $v = new Validator($ctx->req->post);
        $v->text('subject', 3, 200, 'Subject')->text('message', 10, 5000, 'Message')->in('category', Tickets::CATEGORIES, 'Choose a category.');
        if ($this->app->limiter->hit('ticket:ip:' . $ctx->ip(), 10, 3600)) {
            return $this->app->error($ctx, 429);
        }
        if (($bad = $this->captchaError($ctx)) !== null) {
            $v->fail('captcha', $bad);
        }
        if ($v->fails()) {
            return $this->invalid($ctx, 'account/ticket-new', $v->errors(), ['categories' => Tickets::CATEGORIES]);
        }
        $t = $this->app->ticketService->open((int) $user['id'], (string) $user['name'], (string) $user['email'], $v->value('subject'), $v->value('message'), $v->value('category'));
        $this->app->cache->delete('report:overview');
        $ctx->flash('success', 'Your ticket ' . $t['ref'] . ' was opened.');
        return $ctx->redirect('/account/tickets/' . $t['ref']);
    }

    /** @param array<string, string> $params */
    public function accountShow(Ctx $ctx, array $params): Response
    {
        $t = $this->ownTicket($ctx, $params);
        return $t === null ? $this->app->error($ctx, 404) : $ctx->render('account/ticket', $this->thread($t, '/account/tickets/' . $t['ref']));
    }

    /** @param array<string, string> $params */
    public function accountReply(Ctx $ctx, array $params): Response
    {
        $t = $this->ownTicket($ctx, $params);
        return $t === null ? $this->app->error($ctx, 404) : $this->reply($ctx, $t, (int) $ctx->user()['id'], '/account/tickets/' . $t['ref'], 'account/ticket');
    }

    /** @param array<string, string> $params */
    public function accountClose(Ctx $ctx, array $params): Response
    {
        $t = $this->ownTicket($ctx, $params);
        if ($t === null) {
            return $this->app->error($ctx, 404);
        }
        $this->app->ticketService->customerClose($t);
        $ctx->flash('success', 'The ticket was closed.');
        return $ctx->redirect('/account/tickets/' . $t['ref']);
    }

    // ---------------------------------------------------------------- administrators

    public function adminList(Ctx $ctx): Response
    {
        // Resolved tickets that nobody answered for a week close themselves; the list is the natural moment to check.
        $this->app->tickets->autoClose(TicketService::AUTO_CLOSE_DAYS);
        $q = $ctx->req->query;
        $f = [];
        foreach (['status', 'priority', 'category', 'assignee', 'q', 'sort'] as $k) {
            $f[$k] = isset($q[$k]) && is_string($q[$k]) ? $q[$k] : '';
        }
        $f['status'] = $f['status'] === '' ? 'active' : $f['status'];
        $page = isset($q['page']) && is_string($q['page']) && ctype_digit($q['page']) ? (int) $q['page'] : 1;
        return $ctx->render('admin/tickets', [
            'result' => $this->app->tickets->search($f, $page),
            'filters' => $f,
            'counts' => $this->app->tickets->counts(),
            'stats' => $this->app->tickets->stats(),
            'staff' => $this->app->tickets->staff(),
            'me' => (int) $ctx->user()['id'],
        ]);
    }

    /** @param array<string, string> $params */
    public function adminShow(Ctx $ctx, array $params): Response
    {
        $t = $this->adminTicket($params);
        return $t === null ? $this->app->error($ctx, 404) : $ctx->render('admin/ticket', $this->adminThread($t));
    }

    /** @param array<string, string> $params */
    public function adminReply(Ctx $ctx, array $params): Response
    {
        $t = $this->adminTicket($params);
        if ($t === null) {
            return $this->app->error($ctx, 404);
        }
        $v = new Validator($ctx->req->post);
        $v->text('body', 2, 10000, 'Reply')->in('mode', ['reply', 'note'], 'Choose reply or note.')->in('status', ['answered', 'on_hold', 'resolved', 'closed'], 'Choose a status.');
        if ($v->fails()) {
            return $this->invalid($ctx, 'admin/ticket', $v->errors(), $this->adminThread($t));
        }
        $staff = (array) $ctx->user();
        if ($v->value('mode') === 'note') {
            $this->app->ticketService->note($t, $staff, $v->value('body'));
            $ctx->flash('success', 'The note was added. The customer cannot see it.');
        } else {
            $this->app->ticketService->staffReply($t, $staff, $v->value('body'), $v->value('status'));
            $ctx->flash('success', 'Your reply was added.');
        }
        $this->app->cache->delete('report:overview');
        return $ctx->redirect('/admin/tickets/' . $t['id']);
    }

    /** @param array<string, string> $params */
    public function adminUpdate(Ctx $ctx, array $params): Response
    {
        $t = $this->adminTicket($params);
        if ($t === null) {
            return $this->app->error($ctx, 404);
        }
        $p = $ctx->req->post;
        $in = [];
        foreach (['status', 'priority', 'category', 'assignee'] as $k) {
            if (isset($p[$k]) && is_string($p[$k])) {
                $in[$k] = $p[$k];
            }
        }
        $this->app->ticketService->update($t, (array) $ctx->user(), $in);
        $this->app->cache->delete('report:overview');
        $ctx->flash('success', 'The ticket was updated.');
        return $ctx->redirect('/admin/tickets/' . $t['id']);
    }

    public function adminBulk(Ctx $ctx): Response
    {
        $ids = $ctx->req->post['ids'] ?? [];
        $ids = is_array($ids) ? array_values(array_unique(array_filter(array_map(static fn (mixed $i): int => is_string($i) && ctype_digit($i) ? (int) $i : 0, $ids)))) : [];
        $action = $ctx->req->input('action');
        $staff = (array) $ctx->user();
        $back = $this->safeNext($ctx->req->input('back'), '/admin/tickets');
        if ($ids === [] || !in_array($action, ['resolve', 'close', 'assign_me', 'high', 'normal', 'low', 'delete'], true)) {
            $ctx->flash('error', 'Select at least one ticket and choose an action.');
            return $ctx->redirect($back);
        }
        $n = 0;
        foreach (array_slice($ids, 0, 100) as $id) {
            $t = $this->app->tickets->find($id);
            if ($t === null) {
                continue;
            }
            match ($action) {
                'delete' => $this->app->tickets->delete($id),
                'resolve' => $this->app->ticketService->setStatus($t, $staff, 'resolved'),
                'close' => $this->app->ticketService->setStatus($t, $staff, 'closed'),
                'assign_me' => $this->app->ticketService->update($t, $staff, ['assignee' => (string) $staff['id']]),
                default => $this->app->ticketService->update($t, $staff, ['priority' => $action]),
            };
            $n++;
        }
        $this->app->cache->delete('report:overview');
        $ctx->flash('success', $n . ($action === 'delete' ? ' ticket(s) deleted.' : ' ticket(s) updated.'));
        return $ctx->redirect($back);
    }

    /** @param array<string, string> $params */
    public function adminDelete(Ctx $ctx, array $params): Response
    {
        $t = $this->adminTicket($params);
        if ($t === null) {
            return $this->app->error($ctx, 404);
        }
        $this->app->tickets->delete((int) $t['id']);
        $this->app->cache->delete('report:overview');
        $ctx->flash('success', 'Ticket ' . $t['ref'] . ' was deleted.');
        return $ctx->redirect('/admin/tickets');
    }

    public function cannedList(Ctx $ctx): Response
    {
        return $ctx->render('admin/canned', ['canned' => $this->app->tickets->canned()]);
    }

    public function cannedAdd(Ctx $ctx): Response
    {
        $v = new Validator($ctx->req->post);
        $v->text('title', 2, 120, 'Title')->text('body', 2, 5000, 'Text');
        if ($v->fails()) {
            return $this->invalid($ctx, 'admin/canned', $v->errors(), ['canned' => $this->app->tickets->canned()]);
        }
        $this->app->tickets->addCanned($v->value('title'), $v->value('body'));
        $ctx->flash('success', 'The canned reply was added.');
        return $ctx->redirect('/admin/tickets/canned');
    }

    /** @param array<string, string> $params */
    public function cannedDelete(Ctx $ctx, array $params): Response
    {
        $id = $this->intParam($params, 'id');
        if ($id === null) {
            return $this->app->error($ctx, 404);
        }
        $this->app->tickets->deleteCanned($id);
        return $ctx->redirect('/admin/tickets/canned');
    }

    // ---------------------------------------------------------------- helpers

    /** @param array<string, string> $params @return array<string, mixed>|null */
    private function guestTicket(array $params): ?array
    {
        $t = $this->app->tickets->byRef((string) ($params['ref'] ?? ''));
        return $t !== null && hash_equals((string) $t['access_token'], (string) ($params['token'] ?? '')) ? $t : null;
    }

    /** @param array<string, string> $params @return array<string, mixed>|null */
    private function ownTicket(Ctx $ctx, array $params): ?array
    {
        $t = $this->app->tickets->byRef((string) ($params['ref'] ?? ''));
        return $t !== null && $t['user_id'] !== null && (int) $t['user_id'] === (int) $ctx->user()['id'] ? $t : null;
    }

    /** @param array<string, string> $params @return array<string, mixed>|null */
    private function adminTicket(array $params): ?array
    {
        $id = $this->intParam($params, 'id');
        return $id === null ? null : $this->app->tickets->find($id);
    }

    /**
     * What a customer's thread page needs: the ticket, the public replies and whether they may still write.
     *
     * @param array<string, mixed> $t
     * @return array<string, mixed>
     */
    private function thread(array $t, string $base): array
    {
        return ['ticket' => $t, 'replies' => $this->app->tickets->replies((int) $t['id'], false), 'base' => $base, 'mayReply' => $this->app->ticketService->mayReply($t)];
    }

    /**
     * @param array<string, mixed> $t
     * @return array<string, mixed>
     */
    private function adminThread(array $t): array
    {
        return [
            'ticket' => $t,
            'replies' => $this->app->tickets->replies((int) $t['id'], true),
            'staff' => $this->app->tickets->staff(),
            'canned' => $this->app->tickets->canned(),
            'guestUrl' => $this->app->ticketService->guestUrl($t),
        ];
    }

    /**
     * The customer's answer, from either the private link or the account.
     *
     * @param array<string, mixed> $t
     */
    private function reply(Ctx $ctx, array $t, ?int $userId, string $base, string $template): Response
    {
        $v = new Validator($ctx->req->post);
        $v->text('body', 2, 5000, 'Reply');
        if ($this->app->limiter->hit('ticket:ip:' . $ctx->ip(), 20, 3600)) {
            return $this->app->error($ctx, 429);
        }
        // A visitor answering by the private link is asked; a signed-in customer answering their own ticket is not.
        if ($userId === null && ($bad = $this->captchaError($ctx)) !== null) {
            $v->fail('captcha', $bad);
        }
        if ($v->fails()) {
            return $this->invalid($ctx, $template, $v->errors(), $this->thread($t, $base));
        }
        if (!$this->app->ticketService->customerReply($t, $userId, $v->value('body'))) {
            $ctx->flash('error', 'This ticket was closed a while ago. Please open a new one.');
            return $ctx->redirect($base);
        }
        $this->app->cache->delete('report:overview');
        $ctx->flash('success', 'Your reply was sent.');
        return $ctx->redirect($base);
    }
}
