<?php

declare(strict_types=1);

namespace NimVor\Http\Middleware;

use NimVor\Http\Request;
use NimVor\Http\Response;

interface Middleware
{
    /** @param callable(Request): Response $next */
    public function process(Request $request, callable $next): Response;
}
