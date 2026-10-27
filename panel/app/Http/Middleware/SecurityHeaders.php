<?php

declare(strict_types=1);

namespace NimVor\Http\Middleware;

use NimVor\Http\Request;
use NimVor\Http\Response;

/**
 * Adds a strict Content-Security-Policy and related headers. The UI uses no
 * inline scripts or styles and loads every asset from the panel itself.
 */
final class SecurityHeaders implements Middleware
{
    public function process(Request $request, callable $next): Response
    {
        $response = $next($request);
        $headers = [
            'Content-Security-Policy' => "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'",
            'X-Content-Type-Options' => 'nosniff',
            'X-Frame-Options' => 'DENY',
            'Referrer-Policy' => 'same-origin',
            'Permissions-Policy' => 'camera=(), microphone=(), geolocation=(), payment=()',
            'Cross-Origin-Opener-Policy' => 'same-origin',
            'Cross-Origin-Resource-Policy' => 'same-origin',
        ];
        if ($request->isSecure()) {
            $headers['Strict-Transport-Security'] = 'max-age=31536000';
        }
        foreach ($headers as $k => $v) {
            if ($response->header($k) === null) {
                $response = $response->withHeader($k, $v);
            }
        }
        if ($response->header('Cache-Control') === null) {
            $response = $response->withHeader('Cache-Control', 'no-store');
        }
        return $response;
    }
}
