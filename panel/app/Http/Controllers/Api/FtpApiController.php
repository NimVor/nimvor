<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\FtpAccountService;
use NimVor\Services\ServerContext;

/**
 * REST API v1: a website's FTP accounts and SFTP access. Every endpoint calls FtpAccountService, as the
 * web interface does: names, folders and passwords are validated, plan limits apply and each change is
 * audited. An account only ever reaches the folders of its own website.
 */
final class FtpApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly FtpAccountService $ftp,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
    ) {
    }

    /** @return array<string, mixed>|null */
    private function site(Request $r, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($r));
    }

    /**
     * @param array<string, mixed> $a
     * @return array<string, mixed>
     */
    private static function accountOut(array $a): array
    {
        return [
            'login' => (string) ($a['name'] ?? ''), 'dir' => (string) ($a['dir'] ?? ''),
            'quota_mb' => (int) ($a['quota_mb'] ?? 0),
        ];
    }

    /**
     * @param array<string, mixed> $st
     * @return array<string, mixed>
     */
    private static function sftpOut(array $st): array
    {
        $s = (array) (((array) ($st['sftp'] ?? []))[0] ?? []);
        return [
            'enabled' => (bool) ($s['enabled'] ?? false), 'password_login' => (bool) ($s['password_login'] ?? false),
            'keys' => array_values(array_map(static fn ($k): string => (string) (((array) $k)['key'] ?? $k), (array) ($s['keys'] ?? []))),
            'root' => (string) ($st['sftp_root'] ?? ''),
        ];
    }

    public function listAccounts(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s): Response {
            $st = $this->ftp->status($s);
            return self::ok([
                'server_ready' => !empty($st['active']), 'tls' => (string) ($st['settings']['tls'] ?? ''),
                'accounts' => array_map([self::class, 'accountOut'], array_map(static fn ($a): array => (array) $a, (array) ($st['accounts'] ?? []))),
            ]);
        });
    }

    public function createAccount(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $in = self::body($r);
            $res = $this->ftp->save($s, [
                'name' => (string) ($in['name'] ?? ''), 'dir' => (string) ($in['dir'] ?? ''), 'password' => (string) ($in['password'] ?? ''),
                'generate' => !empty($in['generate']), 'quota_mb' => $in['quota_mb'] ?? 0,
            ], false);
            $out = ['login' => $res['login'], 'dir' => trim((string) ($in['dir'] ?? ''), ' /'), 'quota_mb' => max(0, (int) ($in['quota_mb'] ?? 0))];
            if ($res['generated']) {
                $out['password'] = $res['password'];
            }
            return self::ok($out, 201);
        });
    }

    public function updateAccount(Request $r, string $id, string $name): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s, $name): Response {
            $login = FtpAccountService::login($name, (string) $s['domain']);
            $cur = null;
            foreach ((array) ($this->ftp->status($s)['accounts'] ?? []) as $a) {
                if (strtolower((string) (((array) $a)['name'] ?? '')) === $login) {
                    $cur = (array) $a;
                }
            }
            if ($cur === null) {
                return self::notFound();
            }
            // A PATCH changes only what it names.
            $in = self::body($r);
            $res = $this->ftp->save($s, [
                'name' => $login, 'dir' => (string) ($in['dir'] ?? $cur['dir'] ?? ''), 'password' => (string) ($in['password'] ?? ''),
                'generate' => !empty($in['generate']), 'quota_mb' => $in['quota_mb'] ?? $cur['quota_mb'] ?? 0,
            ], true);
            $out = ['login' => $res['login'], 'dir' => trim((string) ($in['dir'] ?? $cur['dir'] ?? ''), ' /'), 'quota_mb' => max(0, (int) ($in['quota_mb'] ?? $cur['quota_mb'] ?? 0))];
            if ($res['generated']) {
                $out['password'] = $res['password'];
            }
            return self::ok($out);
        });
    }

    public function deleteAccount(Request $r, string $id, string $name): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s, $name): Response {
            $login = FtpAccountService::login($name, (string) $s['domain']);
            $found = false;
            foreach ((array) ($this->ftp->status($s)['accounts'] ?? []) as $a) {
                $found = $found || strtolower((string) (((array) $a)['name'] ?? '')) === $login;
            }
            if (!$found) {
                return self::notFound();
            }
            return self::ok(['deleted' => $this->ftp->delete($s, $login)]);
        });
    }

    public function showSftp(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(fn (): Response => self::ok(self::sftpOut($this->ftp->status($s))));
    }

    public function updateSftp(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $cur = self::sftpOut($this->ftp->status($s));
            $in = self::body($r);
            $keys = array_key_exists('keys', $in) ? array_map('strval', (array) $in['keys']) : $cur['keys'];
            $this->ftp->sftp($s, array_key_exists('enabled', $in) ? (bool) $in['enabled'] : $cur['enabled'], $keys, array_key_exists('password_login', $in) ? (bool) $in['password_login'] : $cur['password_login'], (string) ($in['password'] ?? ''));
            return self::ok(['enabled' => array_key_exists('enabled', $in) ? (bool) $in['enabled'] : $cur['enabled'], 'password_login' => array_key_exists('password_login', $in) ? (bool) $in['password_login'] : $cur['password_login'], 'keys' => array_values($keys), 'root' => $cur['root']]);
        });
    }
}
