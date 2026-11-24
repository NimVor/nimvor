<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Agent\AgentException;
use NimVor\Http\Middleware\ApiAuthenticate;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/** The response envelope and error mapping every API controller shares. */
trait ApiHelpers
{
    /** @return array<string, mixed> the token's user */
    private function user(Request $r): array
    {
        return (array) $r->attribute('user', []);
    }

    private static function ok(mixed $data, int $status = 200): Response
    {
        return Response::json(['success' => true, 'data' => $data], $status);
    }

    /** @return array<string, mixed> */
    private static function body(Request $r): array
    {
        return $r->all();
    }

    private static function notFound(): Response
    {
        return ApiAuthenticate::error(404, 'not_found', 'Not found, or not visible to this token.');
    }

    /** @param callable(): Response $fn */
    private function guard(callable $fn): Response
    {
        try {
            return $fn();
        } catch (ValidationException $e) {
            return ApiAuthenticate::error(422, 'validation_failed', $e->getMessage(), ['fields' => $e->errors()]);
        } catch (AgentException $e) {
            return ApiAuthenticate::error(502, $e->errorCode() !== '' ? $e->errorCode() : 'agent_error', $e->getMessage(), ['reason' => $e->reason()]);
        } catch (UserFacingException $e) {
            if ($e->status() === 404) {
                return self::notFound();
            }
            // A refusal by a rule of the panel (the exception's default status, 400) is a conflict with the current state.
            return ApiAuthenticate::error($e->status() === 400 ? 409 : $e->status(), $e->status() === 403 ? 'forbidden' : 'rejected', $e->getMessage(), ['reason' => $e->reason(), 'action' => $e->remedy()]);
        }
    }
}
