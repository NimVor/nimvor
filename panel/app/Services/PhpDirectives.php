<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Support\ValidationException;

/**
 * php.ini directives a website's owner may set for the website (its
 * PHP-FPM pool). The agent checks them again (agent/internal/sites/
 * phpini.go); this list gives the form its groups and friendly errors.
 *
 * Not offered: directives removed from PHP (magic_quotes_*, safe_mode*,
 * register_globals, asp_tags, the mysql.* extension, …), open_basedir and
 * sys_temp_dir (the website's confinement), allow_url_include (includes
 * remote code), enable_dl (no dl() under PHP-FPM) and mysqli.default_pw
 * (the pool file is readable by every local account). Paths must be
 * inside the website's folder; disable_functions and disable_classes add
 * to the server's list.
 */
final class PhpDirectives
{
    /** [group, kind, hint] kind: bool, boolint, int:lo:hi, size, path, enum:a|b, re:<pattern>, list, tz */
    public const ALL = [
        'display_errors' => ['Errors and logging', 'bool', 'Show errors in pages (Off on live sites)'],
        'display_startup_errors' => ['Errors and logging', 'bool', ''],
        'error_reporting' => ['Errors and logging', 're:/^[A-Z0-9_ &|~^()!-]{1,120}$/', 'e.g. E_ALL & ~E_DEPRECATED'],
        'log_errors' => ['Errors and logging', 'bool', ''],
        'error_log' => ['Errors and logging', 'path', 'A file in the website folder'],
        'ignore_repeated_errors' => ['Errors and logging', 'bool', ''],
        'ignore_repeated_source' => ['Errors and logging', 'bool', ''],
        'report_memleaks' => ['Errors and logging', 'bool', ''],
        'html_errors' => ['Errors and logging', 'bool', ''],
        'post_max_size' => ['Requests and uploads', 'size', 'e.g. 128M'],
        'max_input_time' => ['Requests and uploads', 'int:-1:86400', 'Seconds'],
        'max_input_vars' => ['Requests and uploads', 'int:100:1000000', ''],
        'max_file_uploads' => ['Requests and uploads', 'int:1:1000', ''],
        'file_uploads' => ['Requests and uploads', 'bool', ''],
        'upload_tmp_dir' => ['Requests and uploads', 'path', 'A folder in the website folder'],
        'default_socket_timeout' => ['Requests and uploads', 'int:-1:3600', 'Seconds'],
        'allow_url_fopen' => ['Requests and uploads', 'bool', ''],
        'variables_order' => ['Requests and uploads', 're:/^[EGPCS]{1,5}$/', 'e.g. GPCS'],
        'register_argc_argv' => ['Requests and uploads', 'bool', ''],
        'auto_globals_jit' => ['Requests and uploads', 'bool', ''],
        'output_buffering' => ['Output', 'boolint', 'On, Off or bytes (e.g. 4096)'],
        'implicit_flush' => ['Output', 'bool', ''],
        'zlib.output_compression' => ['Output', 'boolint', 'On, Off or a buffer size'],
        'expose_php' => ['Output', 'bool', 'Send the X-Powered-By header'],
        'default_mimetype' => ['Output', 're:/^[a-z]+\/[a-z0-9.+-]{1,60}$/', 'e.g. text/html'],
        'precision' => ['Output', 'int:-1:50', ''],
        'serialize_precision' => ['Output', 'int:-1:50', ''],
        'date.timezone' => ['Scripts', 'tz', 'e.g. Asia/Karachi'],
        'short_open_tag' => ['Scripts', 'bool', ''],
        'auto_prepend_file' => ['Scripts', 'path', 'A file in the website folder'],
        'auto_append_file' => ['Scripts', 'path', 'A file in the website folder'],
        'unserialize_callback_func' => ['Scripts', 're:/^[A-Za-z_][A-Za-z0-9_\\\\]{0,100}$/', ''],
        'disable_functions' => ['Scripts', 'list', 'Added to the server\'s list, e.g. mail,exec'],
        'disable_classes' => ['Scripts', 'list', 'Comma-separated class names'],
        'session.save_path' => ['Sessions', 'path', 'A folder in the website folder'],
        'session.save_handler' => ['Sessions', 'enum:files|redis|memcached', ''],
        'session.serialize_handler' => ['Sessions', 'enum:php|php_binary|php_serialize', ''],
        'session.name' => ['Sessions', 're:/^[A-Za-z][A-Za-z0-9_]{0,63}$/', 'e.g. PHPSESSID'],
        'session.auto_start' => ['Sessions', 'bool', ''],
        'session.cache_limiter' => ['Sessions', 'enum:nocache|private|private_no_expire|public', ''],
        'session.cache_expire' => ['Sessions', 'int:0:525600', 'Minutes'],
        'session.cookie_lifetime' => ['Sessions', 'int:0:31536000', 'Seconds (0: until the browser closes)'],
        'session.cookie_path' => ['Sessions', 're:#^/[A-Za-z0-9._~/-]{0,200}$#', 'e.g. /'],
        'session.cookie_domain' => ['Sessions', 're:/^\.?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/', ''],
        'session.cookie_httponly' => ['Sessions', 'bool', ''],
        'session.cookie_secure' => ['Sessions', 'bool', ''],
        'session.cookie_samesite' => ['Sessions', 'enum:Lax|Strict|None', ''],
        'session.use_cookies' => ['Sessions', 'bool', ''],
        'session.use_only_cookies' => ['Sessions', 'bool', ''],
        'session.use_strict_mode' => ['Sessions', 'bool', ''],
        'session.use_trans_sid' => ['Sessions', 'bool', ''],
        'session.referer_check' => ['Sessions', 're:#^[A-Za-z0-9.:/-]{1,200}$#', ''],
        'session.gc_probability' => ['Sessions', 'int:0:1000000', ''],
        'session.gc_divisor' => ['Sessions', 'int:1:1000000', ''],
        'session.gc_maxlifetime' => ['Sessions', 'int:1:31536000', 'Seconds'],
        'url_rewriter.tags' => ['Sessions', 're:/^[a-z]+=[a-z]*(,[a-z]+=[a-z]*){0,20}$/', 'e.g. a=href,form='],
        'mysqli.default_host' => ['MySQLi defaults', 're:/^[A-Za-z0-9.:-]{1,253}$/', ''],
        'mysqli.default_port' => ['MySQLi defaults', 'int:1:65535', ''],
        'mysqli.default_socket' => ['MySQLi defaults', 're:#^/[A-Za-z0-9._/-]{1,400}$#', ''],
        'mysqli.default_user' => ['MySQLi defaults', 're:/^[A-Za-z0-9_.-]{1,80}$/', ''],
        'mysqli.max_links' => ['MySQLi defaults', 'int:-1:100000', ''],
    ];

    /**
     * Checks the submitted directives (empty values are dropped) and
     * returns them normalised.
     *
     * @param array<mixed> $input
     * @return array<string, string>
     */
    public static function validate(array $input, string $root): array
    {
        $out = [];
        $errors = [];
        foreach ($input as $k => $v) {
            $k = (string) $k;
            $v = trim((string) (is_scalar($v) ? $v : ''));
            if ($v === '') {
                continue;
            }
            if (!isset(self::ALL[$k])) {
                $errors['ini'] = $k . ' is not a PHP setting a website may change.';
                continue;
            }
            $kind = self::ALL[$k][1];
            $err = null;
            [$type, $arg] = array_pad(explode(':', $kind, 2), 2, '');
            switch ($type) {
                case 'bool':
                case 'boolint':
                    $b = match (strtolower($v)) {
                        '1', 'on', 'true', 'yes' => 'On',
                        '0', 'off', 'false', 'no' => 'Off',
                        default => null,
                    };
                    if ($b !== null) {
                        $v = $b;
                    } elseif ($type === 'bool' || !ctype_digit($v) || (int) $v > (1 << 24)) {
                        $err = $type === 'bool' ? 'Use On or Off.' : 'Use On, Off or a number.';
                    }
                    break;
                case 'int':
                    [$lo, $hi] = array_map('intval', explode(':', $arg));
                    $n = filter_var($v, FILTER_VALIDATE_INT);
                    if ($n === false || $n < $lo || $n > $hi) {
                        $err = "Enter a whole number between {$lo} and {$hi}.";
                    } else {
                        $v = (string) $n;
                    }
                    break;
                case 'size':
                    $v = strtoupper($v);
                    if (!preg_match('/^[0-9]{1,6}[KMG]?$/', $v)) {
                        $err = 'Enter a size such as 64M.';
                    }
                    break;
                case 'path':
                    $base = rtrim($root, '/') . '/';
                    if (!preg_match('#^/[A-Za-z0-9._/-]{1,400}$#', $v) || str_contains($v, '..') || str_contains($v, '//') || str_ends_with($v, '/') || !str_starts_with($v, $base)) {
                        $err = 'Enter a path inside the website folder (' . $base . '…).';
                    }
                    break;
                case 'enum':
                    if (!in_array($v, explode('|', $arg), true)) {
                        $err = 'Choose one of ' . str_replace('|', ', ', $arg) . '.';
                    }
                    break;
                case 'tz':
                    if (!in_array($v, \DateTimeZone::listIdentifiers(), true)) {
                        $err = 'Choose a time zone such as Europe/London.';
                    }
                    break;
                case 'list':
                    $v = str_replace(' ', '', $v);
                    foreach (explode(',', $v) as $item) {
                        if (!preg_match('/^[A-Za-z_][A-Za-z0-9_\\\\]{0,100}$/', $item)) {
                            $err = 'Enter names separated by commas.';
                            break;
                        }
                    }
                    break;
                case 're':
                    if (!preg_match($arg, $v)) {
                        $err = 'This value is not accepted.';
                    }
                    break;
            }
            if ($err !== null) {
                $errors['ini.' . $k] = $err;
                continue;
            }
            $out[$k] = $v;
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        ksort($out);
        return $out;
    }

    /** @return array<string, list<string>> directive names by group */
    public static function groups(): array
    {
        $out = [];
        foreach (self::ALL as $k => [$group]) {
            $out[$group][] = $k;
        }
        return $out;
    }
}
