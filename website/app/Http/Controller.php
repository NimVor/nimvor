<?php

declare(strict_types=1);

namespace NimVorSite\Http;

use NimVorSite\Core\App;
use NimVorSite\Core\Ctx;
use NimVorSite\Core\Response;

abstract class Controller
{
    public function __construct(protected readonly App $app)
    {
    }

    /** Re-shows a form with its errors and what was typed (never a password). */
    /**
     * @param array<string, string> $errors
     * @param array<string, mixed> $extra
     */
    protected function invalid(Ctx $ctx, string $template, array $errors, array $extra = [], int $status = 422): Response
    {
        $old = $ctx->req->post;
        unset($old['password'], $old['password_confirm'], $old['current_password'], $old['new_password'], $old['_token']);
        return $ctx->render($template, ['errors' => $errors, 'old' => $old] + $extra, $status);
    }

    protected function intParam(array $params, string $key): ?int
    {
        return isset($params[$key]) && ctype_digit($params[$key]) ? (int) $params[$key] : null;
    }

    /** A local path after sign-in, never another site. */
    protected function safeNext(string $next, string $fallback): string
    {
        return str_starts_with($next, '/') && !str_starts_with($next, '//') && !str_contains($next, '\\') && !preg_match('/[\x00-\x1f]/', $next) ? $next : $fallback;
    }

    /** The text shown when the check in front of a public form failed, or null when it passed (or is off). */
    protected function captchaError(Ctx $ctx): ?string
    {
        if ($ctx->user() !== null || $this->app->captcha->verify($ctx->req, $ctx->ip())) {
            return null;
        }
        return $this->app->captcha->provider() === 'builtin'
            ? 'That answer was not right, or the question took too long. Please answer the new question.'
            : 'The check could not be confirmed. Please try it again.';
    }
}
