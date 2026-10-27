<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Route;
use WhaCP\Plugins\PluginManager;

/**
 * Invokes the handler a plugin registered for the matched route. The
 * route itself already passed CSRF, authentication and its permission.
 */
final class PluginDispatchController
{
    public function __construct(private readonly PluginManager $plugins)
    {
    }

    public function dispatch(Request $request, string ...$params): Response
    {
        $route = $request->attribute('route');
        $handler = $route instanceof Route && $route->name !== null ? $this->plugins->handler($route->name) : null;
        if ($handler === null) {
            return Response::html('Not found', 404);
        }
        $res = $handler($request, ...$params);
        return $res instanceof Response ? $res : Response::html((string) $res);
    }
}
