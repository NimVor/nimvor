<?php

declare(strict_types=1);

namespace NimVor\Http\Middleware;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Route;
use NimVor\Http\Session\SessionInterface;
use NimVor\Security\Csrf;

/**
 * Lets go of the session file before a read-only background request does its work.
 *
 * PHP locks the session file for the whole request, so the requests of one browser run one after the other. The
 * dashboard asks for its widgets, series and status all at once; with the lock held by each in turn the last of
 * them waited for all the others (a page of 27 requests of 0.3 s took 9 s). These requests only read: the user is
 * already signed in and checked by now, and they write nothing to the session, so they can let go of it.
 *
 * Only GET requests to the routes listed here, after authentication, and never a route that sets a flash message,
 * the user or anything else in the session.
 */
final class ReleaseSession implements Middleware
{
    /** Background requests that only read (see the controllers: none of them writes to the session). */
    public const READ_ONLY_ROUTES = [
        'dashboard.widget', 'dashboard.series', 'dashboard.live', 'dashboard.popover',
        'topbar.status', 'notifications.summary', 'monitoring.live', 'changelog.content',
    ];

    public function __construct(private readonly SessionInterface $session, private readonly Csrf $csrf)
    {
    }

    public function process(Request $request, callable $next): Response
    {
        /** @var Route|null $route */
        $route = $request->attribute('route');
        if ($request->method() === 'GET' && $route !== null && in_array($route->name, self::READ_ONLY_ROUTES, true)) {
            // A fragment may carry a form: make sure the token exists (and is saved) before the session is let go.
            $this->csrf->token();
            $this->session->close();
        }
        return $next($request);
    }
}
