<?php

declare(strict_types=1);

namespace WhaCP\Http\Middleware;

use WhaCP\Http\Request;
use WhaCP\Http\Response;

/**
 * Forms submitted in the background by whacp.js (header X-WhaCP-Ajax: 1)
 * get the redirect that ends every form action as JSON instead of a 303,
 * so the page can load that address itself (keeping its #section) and
 * swap in the result without a full reload. The action, its validation,
 * CSRF check and authorization are exactly those of a normal submit.
 */
final class AjaxForms implements Middleware
{
    public function process(Request $request, callable $next): Response
    {
        $response = $next($request);
        if ($request->method() !== 'POST' || $request->header('X-WhaCP-Ajax') !== '1') {
            return $response;
        }
        $status = $response->status();
        $location = $response->header('Location');
        if ($status < 300 || $status >= 400 || $location === null) {
            return $response;
        }
        return Response::json(['success' => true, 'redirect' => $location]);
    }
}
