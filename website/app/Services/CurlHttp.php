<?php

declare(strict_types=1);

namespace NimVorSite\Services;

/** HTTPS only, no redirects followed, answers capped at 8 MB. Uses cURL when present, PHP streams otherwise. */
final class CurlHttp implements Http
{
    private const MAX = 8 * 1024 * 1024;

    public function request(string $method, string $url, array $headers = [], ?string $body = null, int $timeout = 10): array
    {
        if (!str_starts_with($url, 'https://')) {
            throw new \RuntimeException('Only HTTPS addresses are fetched.');
        }
        $headers += ['User-Agent' => 'nimvor-site (+https://nimvor.com)', 'Accept' => '*/*'];
        return function_exists('curl_init') ? $this->curl($method, $url, $headers, $body, $timeout) : $this->stream($method, $url, $headers, $body, $timeout);
    }

    /** @param array<string, string> $headers */
    private function curl(string $method, string $url, array $headers, ?string $body, int $timeout): array
    {
        $ch = curl_init($url);
        $got = '';
        curl_setopt_array($ch, [
            CURLOPT_CUSTOMREQUEST => $method,
            CURLOPT_HTTPHEADER => array_map(static fn (string $k, string $v): string => $k . ': ' . $v, array_keys($headers), $headers),
            CURLOPT_TIMEOUT => $timeout,
            CURLOPT_CONNECTTIMEOUT => 5,
            CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_PROTOCOLS => CURLPROTO_HTTPS,
            CURLOPT_WRITEFUNCTION => static function ($c, string $chunk) use (&$got): int {
                $got .= $chunk;
                return strlen($got) > self::MAX ? 0 : strlen($chunk);
            },
        ]);
        if ($body !== null) {
            curl_setopt($ch, CURLOPT_POSTFIELDS, $body);
        }
        if (curl_exec($ch) === false) {
            $err = curl_error($ch);
            curl_close($ch);
            throw new \RuntimeException('The request failed: ' . $err);
        }
        $status = (int) curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
        curl_close($ch);
        return ['status' => $status, 'body' => $got];
    }

    /** @param array<string, string> $headers */
    private function stream(string $method, string $url, array $headers, ?string $body, int $timeout): array
    {
        $ctx = stream_context_create(['http' => [
            'method' => $method,
            'header' => implode("\r\n", array_map(static fn (string $k, string $v): string => $k . ': ' . $v, array_keys($headers), $headers)),
            'content' => $body ?? '',
            'timeout' => $timeout,
            'ignore_errors' => true,
            'follow_location' => 0,
        ]]);
        $got = @file_get_contents($url, false, $ctx, 0, self::MAX);
        if ($got === false) {
            throw new \RuntimeException('The request failed.');
        }
        $status = 0;
        foreach ($http_response_header ?? [] as $h) {
            if (preg_match('#^HTTP/\S+\s+(\d{3})#', $h, $m) === 1) {
                $status = (int) $m[1];
            }
        }
        return ['status' => $status, 'body' => $got];
    }
}
