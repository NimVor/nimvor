<?php

declare(strict_types=1);

namespace WhaCP\Http\Session;

use RuntimeException;

/**
 * PHP native session with hardened settings.
 */
final class NativeSession implements SessionInterface
{
    public function __construct(
        private readonly string $name,
        private readonly string $savePath,
        private readonly bool $secureCookie,
    ) {
    }

    public function start(): void
    {
        if (session_status() === PHP_SESSION_ACTIVE) {
            return;
        }
        if (!is_dir($this->savePath) && !@mkdir($this->savePath, 0700, true) && !is_dir($this->savePath)) {
            throw new RuntimeException('Session directory is not writable');
        }
        ini_set('session.use_strict_mode', '1');
        ini_set('session.use_only_cookies', '1');
        ini_set('session.use_trans_sid', '0');
        ini_set('session.gc_maxlifetime', '86400');
        session_save_path($this->savePath);
        session_name($this->name);
        session_set_cookie_params([
            'lifetime' => 0,
            'path' => '/',
            'secure' => $this->secureCookie,
            'httponly' => true,
            'samesite' => 'Lax',
        ]);
        session_start();
    }

    public function get(string $key, mixed $default = null): mixed
    {
        return $_SESSION[$key] ?? $default;
    }

    public function set(string $key, mixed $value): void
    {
        $_SESSION[$key] = $value;
    }

    public function remove(string $key): void
    {
        unset($_SESSION[$key]);
    }

    public function regenerate(): void
    {
        session_regenerate_id(true);
    }

    public function invalidate(): void
    {
        $_SESSION = [];
        session_regenerate_id(true);
    }

    public function flash(string $key, mixed $value): void
    {
        $_SESSION['_flash'][$key] = $value;
    }

    public function pullFlash(string $key, mixed $default = null): mixed
    {
        $value = $_SESSION['_flash'][$key] ?? $default;
        unset($_SESSION['_flash'][$key]);
        return $value;
    }
}
