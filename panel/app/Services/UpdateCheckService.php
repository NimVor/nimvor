<?php

declare(strict_types=1);

namespace WhaCP\Services;

use Closure;
use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;

/**
 * Is a newer NimVor available? The agent reports the installed checkout
 * (commit, branch, remote; it has no network access itself); the panel
 * asks GitHub for the branch's newest commit. The answer is kept in a
 * small file, refreshed by the scheduler every six hours or on request,
 * so pages never wait for GitHub. A remote that is not on GitHub, or a
 * private repository, is reported as "unknown", never as "up to date".
 */
final class UpdateCheckService
{
    public const MAX_AGE = 21600;

    /** @var Closure(string): array{0: int, 1: ?string} */
    private Closure $fetch;

    /** @param (Closure(string): array{0: int, 1: ?string})|null $fetch returns [HTTP status, body] */
    public function __construct(private readonly AgentClient $agent, private readonly string $file, ?Closure $fetch = null)
    {
        $this->fetch = $fetch ?? static function (string $url): array {
            $ch = curl_init($url);
            curl_setopt_array($ch, [CURLOPT_RETURNTRANSFER => true, CURLOPT_TIMEOUT => 10, CURLOPT_CONNECTTIMEOUT => 5, CURLOPT_FOLLOWLOCATION => false,
                CURLOPT_PROTOCOLS => CURLPROTO_HTTPS, CURLOPT_USERAGENT => 'NimVor update check', CURLOPT_HTTPHEADER => ['Accept: application/vnd.github.sha']]);
            $body = curl_exec($ch);
            $code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
            curl_close($ch);
            return [$code, is_string($body) ? $body : null];
        };
    }

    /** @return array<string, mixed>|null the last check */
    public function cached(): ?array
    {
        if (!is_file($this->file)) {
            return null;
        }
        $d = json_decode((string) @file_get_contents($this->file), true);
        return is_array($d) ? $d : null;
    }

    public function available(): bool
    {
        return !empty($this->cached()['available']);
    }

    public function due(): bool
    {
        $c = $this->cached();
        return $c === null || (int) ($c['checked_at'] ?? 0) < time() - self::MAX_AGE;
    }

    /** "owner/repo" of a GitHub remote URL, or null. */
    public static function githubRepo(string $remote): ?string
    {
        if (preg_match('#^(?:https://github\.com/|git@github\.com:|ssh://git@github\.com/)([A-Za-z0-9_.-]{1,100})/([A-Za-z0-9_.-]{1,100}?)(?:\.git)?/?$#', $remote, $m)) {
            return $m[1] . '/' . $m[2];
        }
        return null;
    }

    /** @return array<string, mixed> */
    public function check(): array
    {
        $res = ['checked_at' => time(), 'available' => false, 'state' => 'unknown', 'current' => '', 'latest' => '', 'branch' => '', 'updatable' => false, 'reason' => '', 'command' => ''];
        try {
            $src = (array) json_decode((string) json_encode($this->agent->call('whacp.update_status')), true);
        } catch (AgentException $e) {
            $res['reason'] = 'The agent could not report the installed version: ' . $e->getMessage();
            return $this->save($res);
        }
        $res['current'] = (string) ($src['commit'] ?? '');
        $res['branch'] = (string) ($src['branch'] ?? '');
        $res['updatable'] = !empty($src['updatable']);
        $res['reason'] = (string) ($src['reason'] ?? '');
        $res['command'] = (string) ($src['command'] ?? '');
        $repo = self::githubRepo((string) ($src['remote'] ?? ''));
        if (empty($src['git']) || $repo === null || $res['branch'] === '' || !preg_match('/^[0-9a-f]{40}$/', $res['current'])) {
            $res['reason'] = $res['reason'] !== '' ? $res['reason'] : 'Updates can only be looked up for a checkout of a GitHub repository.';
            return $this->save($res);
        }
        [$code, $body] = ($this->fetch)('https://api.github.com/repos/' . $repo . '/commits/' . rawurlencode($res['branch']));
        $sha = trim((string) $body);
        if ($code !== 200 || !preg_match('/^[0-9a-f]{40}$/', $sha)) {
            $res['reason'] = $code === 404 ? 'GitHub does not show this repository without signing in (private repository); update over SSH.' : 'GitHub could not be reached (HTTP ' . $code . ').';
            return $this->save($res);
        }
        $res['latest'] = $sha;
        $res['state'] = $sha === $res['current'] ? 'current' : 'available';
        $res['available'] = $sha !== $res['current'];
        return $this->save($res);
    }

    /** @param array<string, mixed> $res @return array<string, mixed> */
    private function save(array $res): array
    {
        $dir = dirname($this->file);
        if (!is_dir($dir)) {
            @mkdir($dir, 0o750, true);
        }
        @file_put_contents($this->file, (string) json_encode($res), LOCK_EX);
        return $res;
    }
}
