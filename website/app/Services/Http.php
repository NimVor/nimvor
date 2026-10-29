<?php

declare(strict_types=1);

namespace NimVorSite\Services;

/** Outgoing requests (GitHub only). A fake stands in for it in the tests. */
interface Http
{
    /**
     * @param array<string, string> $headers
     * @return array{status: int, body: string}
     * @throws \RuntimeException when the server cannot be reached at all
     */
    public function request(string $method, string $url, array $headers = [], ?string $body = null, int $timeout = 10): array;
}
