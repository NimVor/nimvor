<?php

declare(strict_types=1);

namespace NimVorSite\Core;

final class Response
{
    /** @var list<string> */
    private array $cookies = [];
    private ?string $streamPath = null;
    private int $streamFrom = 0;
    private int $streamLength = 0;

    /** @param array<string, string> $headers */
    public function __construct(public int $status = 200, public string $body = '', public array $headers = [])
    {
    }

    public static function html(string $body, int $status = 200): self
    {
        return new self($status, $body, ['Content-Type' => 'text/html; charset=UTF-8']);
    }

    public static function text(string $body, string $type = 'text/plain', int $status = 200): self
    {
        return new self($status, $body, ['Content-Type' => $type . '; charset=UTF-8']);
    }

    public static function redirect(string $to, int $status = 303): self
    {
        // Only local paths: no open redirect.
        if (!str_starts_with($to, '/') || str_starts_with($to, '//') || str_contains($to, "\n") || str_contains($to, "\r")) {
            $to = '/';
        }
        return new self($status, '', ['Location' => $to]);
    }

    /**
     * A file sent in pieces, with Range support so a browser can seek in a video.
     * Only one range is served; a bad one gets 416.
     */
    public static function file(string $path, string $type, string $range = '', string $cache = 'public, max-age=86400'): self
    {
        $size = (int) filesize($path);
        $mtime = (int) filemtime($path);
        $r = new self(200, '', [
            'Content-Type' => $type,
            'Accept-Ranges' => 'bytes',
            'Cache-Control' => $cache,
            'ETag' => '"' . dechex($mtime) . '-' . dechex($size) . '"',
            'Last-Modified' => gmdate('D, d M Y H:i:s', $mtime) . ' GMT',
        ]);
        $from = 0;
        $to = $size - 1;
        if ($range !== '') {
            if (preg_match('/^bytes=(\d*)-(\d*)$/', trim($range), $m) !== 1 || ($m[1] === '' && $m[2] === '')) {
                return new self(416, '', ['Content-Range' => 'bytes */' . $size, 'Content-Type' => 'text/plain']);
            }
            if ($m[1] === '') {
                $from = max(0, $size - (int) $m[2]);
            } else {
                $from = (int) $m[1];
                $to = $m[2] === '' ? $size - 1 : min((int) $m[2], $size - 1);
            }
            if ($from > $to || $from >= $size) {
                return new self(416, '', ['Content-Range' => 'bytes */' . $size, 'Content-Type' => 'text/plain']);
            }
            $r->status = 206;
            $r->headers['Content-Range'] = 'bytes ' . $from . '-' . $to . '/' . $size;
        }
        $r->streamPath = $path;
        $r->streamFrom = $from;
        $r->streamLength = $to - $from + 1;
        $r->headers['Content-Length'] = (string) $r->streamLength;
        return $r;
    }

    public function streamLength(): int
    {
        return $this->streamLength;
    }

    public function header(string $name, string $value): self
    {
        $this->headers[$name] = $value;
        return $this;
    }

    public function cookie(string $header): self
    {
        $this->cookies[] = $header;
        return $this;
    }

    /** @return list<string> */
    public function cookies(): array
    {
        return $this->cookies;
    }

    public function send(): void
    {
        http_response_code($this->status);
        foreach ($this->headers as $k => $v) {
            header($k . ': ' . $v);
        }
        foreach ($this->cookies as $c) {
            header('Set-Cookie: ' . $c, false);
        }
        if ($this->streamPath !== null) {
            $f = fopen($this->streamPath, 'rb');
            if ($f !== false) {
                fseek($f, $this->streamFrom);
                $left = $this->streamLength;
                while ($left > 0 && !feof($f) && connection_status() === CONNECTION_NORMAL) {
                    $chunk = fread($f, min(1048576, $left));
                    if ($chunk === false || $chunk === '') {
                        break;
                    }
                    echo $chunk;
                    $left -= strlen($chunk);
                    flush();
                }
                fclose($f);
            }
            return;
        }
        echo $this->body;
    }
}
