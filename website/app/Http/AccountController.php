<?php

declare(strict_types=1);

namespace NimVorSite\Http;

use NimVorSite\Core\Ctx;
use NimVorSite\Core\Response;
use NimVorSite\Services\Auth;
use NimVorSite\Support\Validator;

/** The client's own dashboard: everything here is scoped to the signed-in user's id. */
final class AccountController extends Controller
{
    public function dashboard(Ctx $ctx): Response
    {
        $id = (int) $ctx->user()['id'];
        $requests = $this->app->requests->forUser($id);
        $open = count(array_filter($requests, static fn (array $r): bool => !in_array($r['status'], ['done', 'closed'], true)));
        return $ctx->render('account/dashboard', [
            'requests' => array_slice($requests, 0, 5),
            'openRequests' => $open,
            'messageCount' => count($this->app->messages->forUser($id)),
        ]);
    }

    public function requests(Ctx $ctx): Response
    {
        return $ctx->render('account/requests', ['requests' => $this->app->requests->forUser((int) $ctx->user()['id'])]);
    }

    public function messages(Ctx $ctx): Response
    {
        return $ctx->render('account/messages', ['messages' => $this->app->messages->forUser((int) $ctx->user()['id'])]);
    }

    public function profile(Ctx $ctx): Response
    {
        return $ctx->render('account/profile', ['old' => ['name' => (string) $ctx->user()['name']]]);
    }

    public function profileSave(Ctx $ctx): Response
    {
        $user = $ctx->user() ?? [];
        $v = new Validator($ctx->req->post);
        $v->text('name', 1, 120, 'name');
        $changePassword = $ctx->req->input('new_password') !== '';
        if ($changePassword) {
            if (!password_verify($ctx->req->input('current_password'), (string) $user['password_hash'])) {
                $v->fail('current_password', 'The current password is not right.');
            }
            $v->password('new_password', (string) $user['email'], $v->value('name'));
            if ($v->value('new_password') !== $v->value('new_password_confirm')) {
                $v->fail('new_password_confirm', 'The two passwords differ.');
            }
        }
        if ($v->fails()) {
            return $this->invalid($ctx, 'account/profile', $v->errors());
        }
        $this->app->users->setName((int) $user['id'], $v->value('name'));
        if ($changePassword) {
            $this->app->users->setPassword((int) $user['id'], Auth::hash($v->value('new_password')));
            $ctx->session->regenerate();
        }
        $ctx->flash('success', 'Your account was updated.');
        return $ctx->redirect('/account/profile');
    }
}
