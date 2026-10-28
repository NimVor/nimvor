<?php

declare(strict_types=1);

namespace NimVorSite\Core;

use NimVorSite\Services\Auth;

/** What one request needs: the request itself, its session, who is signed in and how to answer. */
final class Ctx
{
    public function __construct(
        public readonly App $app,
        public readonly Request $req,
        public readonly Session $session,
        public readonly Auth $auth,
        public readonly View $view,
    ) {
    }

    /** @param array<string, mixed> $data */
    public function render(string $template, array $data = [], int $status = 200): Response
    {
        $this->view->setCsrfProvider(fn (): string => $this->session->csrfToken());
        $this->view->share('flash', $this->session->pullFlash());
        $this->view->share('user', $this->auth->user());
        return Response::html($this->view->render($template, $data), $status);
    }

    public function flash(string $type, string $message): void
    {
        $this->session->flash($type, $message);
    }

    public function redirect(string $to): Response
    {
        return Response::redirect($to);
    }

    public function ip(): string
    {
        return $this->req->ip((string) $this->app->config['trusted_proxy']);
    }

    /** @return array<string, mixed>|null */
    public function user(): ?array
    {
        return $this->auth->user();
    }
}
