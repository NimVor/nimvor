<?php

declare(strict_types=1);

namespace WhaCP\Http\Middleware;

use WhaCP\Http\Request;
use WhaCP\Http\Response;

interface Middleware
{
    /** @param callable(Request): Response $next */
    public function process(Request $request, callable $next): Response;
}
