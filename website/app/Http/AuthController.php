<?php

declare(strict_types=1);

namespace NimVorSite\Http;

use NimVorSite\Core\Ctx;
use NimVorSite\Core\Response;
use NimVorSite\Services\Auth;
use NimVorSite\Support\Validator;

final class AuthController extends Controller
{
    public function loginForm(Ctx $ctx): Response
    {
        if ($ctx->user() !== null) {
            return $ctx->redirect($ctx->auth->isAdmin() ? '/admin' : '/account');
        }
        return $ctx->render('auth/login', ['next' => $ctx->req->input('next')]);
    }

    public function login(Ctx $ctx): Response
    {
        $email = trim($ctx->req->input('email'));
        $res = $ctx->auth->attempt($email, $ctx->req->input('password'), $ctx->ip());
        if ($res['user'] === null) {
            $msg = $res['error'] === 'throttled' ? 'Too many failed attempts. Wait a few minutes and try again.' : 'The email or password is not right.';
            return $this->invalid($ctx, 'auth/login', ['form' => $msg], ['next' => $ctx->req->input('next')], $res['error'] === 'throttled' ? 429 : 422);
        }
        $ctx->auth->login($res['user']);
        $home = $res['user']['role'] === 'admin' ? '/admin' : '/account';
        return $ctx->redirect($this->safeNext($ctx->req->input('next'), $home));
    }

    public function registerForm(Ctx $ctx): Response
    {
        return $ctx->user() !== null ? $ctx->redirect('/account') : $ctx->render('auth/register');
    }

    public function register(Ctx $ctx): Response
    {
        if ($this->app->limiter->hit('register:ip:' . $ctx->ip(), 5, 3600)) {
            return $this->app->error($ctx, 429);
        }
        $v = new Validator($ctx->req->post);
        $v->text('name', 1, 120, 'name')->email();
        $v->password('password', $v->value('email'), $v->value('name'));
        if ($v->value('password') !== $v->value('password_confirm')) {
            $v->fail('password_confirm', 'The two passwords differ.');
        }
        if (!$v->fails() && $this->app->users->byEmail($v->value('email')) !== null) {
            $v->fail('email', 'An account with this email already exists. Sign in instead.');
        }
        if ($v->fails()) {
            return $this->invalid($ctx, 'auth/register', $v->errors());
        }
        $id = $this->app->users->create($v->value('email'), $v->value('name'), Auth::hash($v->value('password')));
        $user = $this->app->users->find($id);
        $ctx->auth->login($user ?? []);
        $this->app->cache->delete('report:overview');
        $ctx->flash('success', 'Welcome to NimVor. Your account is ready.');
        return $ctx->redirect('/account');
    }

    public function logout(Ctx $ctx): Response
    {
        $ctx->auth->logout();
        return $ctx->redirect('/');
    }
}
