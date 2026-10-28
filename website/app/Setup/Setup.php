<?php

declare(strict_types=1);

namespace NimVorSite\Setup;

use NimVorSite\Cache\CacheFactory;
use NimVorSite\Core\Db;
use NimVorSite\Core\Request;
use NimVorSite\Core\Response;
use NimVorSite\Core\View;
use NimVorSite\Services\Auth;
use NimVorSite\Support\Validator;

/**
 * First-run setup in the browser, for a host without a terminal: it connects to
 * the database, creates the tables, creates the first administrator and writes
 * config/local.php. It works once: after it succeeds it locks itself and the page
 * disappears.
 *
 * Because anyone who reaches an unlocked setup page could take the site over, it
 * needs a secret that only someone with access to the files can know: a file
 * storage/setup.key (a random string of at least 16 characters), created through
 * the host's file manager. The key file is deleted when setup finishes.
 */
final class Setup
{
    public const COOKIE = 'nimvor_setup';

    public function __construct(private readonly string $root)
    {
    }

    /** Whether the site is already configured (setup must then be invisible). */
    public static function isInstalled(string $root): bool
    {
        return is_file($root . '/storage/installed.lock')
            || is_file($root . '/config/local.php')
            || getenv('DB_PASS') !== false
            || getenv('SITE_CONFIG') !== false;
    }

    public function handle(Request $req): Response
    {
        if (self::isInstalled($this->root)) {
            return Response::html('<!doctype html><meta charset="utf-8"><title>Not found</title><h1>Not found</h1>', 404)->header('Cache-Control', 'no-store');
        }
        if ($req->path !== '/setup') {
            return Response::redirect('/setup');
        }
        $token = $req->cookie(self::COOKIE);
        $fresh = false;
        if ($token === null || preg_match('/^[a-f0-9]{64}$/', $token) !== 1) {
            $token = bin2hex(random_bytes(32));
            $fresh = true;
        }
        $res = $req->isPost() ? $this->submit($req, $token) : $this->form($token, [], []);
        if ($fresh) {
            $res->cookie(self::COOKIE . '=' . $token . '; Path=/setup; Max-Age=3600; HttpOnly; SameSite=Strict' . ($req->isHttps() ? '; Secure' : ''));
        }
        return $res->header('Cache-Control', 'no-store')
            ->header('X-Frame-Options', 'DENY')
            ->header('X-Content-Type-Options', 'nosniff')
            ->header('Content-Security-Policy', "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'");
    }

    /**
     * @param array<string, string> $errors
     * @param array<string, string> $old
     */
    private function form(string $token, array $errors, array $old, int $status = 200): Response
    {
        $keyFile = $this->keyFile();
        $view = new View($this->root . '/resources/views', $this->root . '/public', [
            'path' => '/setup', 'siteUrl' => '', 'flash' => null, 'user' => null, 'errors' => $errors, 'old' => $old,
        ]);
        $view->share('v', $view);
        $view->setCsrfProvider(static fn (): string => $token);
        $html = $view->render('setup/form', [
            'keyReady' => is_file($keyFile) && strlen(trim((string) file_get_contents($keyFile))) >= 16,
            'keyFile' => 'storage/setup.key',
            'writable' => is_writable($this->root . '/config') && is_writable($this->root . '/storage'),
            'extensions' => ['pdo_mysql' => extension_loaded('pdo_mysql'), 'redis' => extension_loaded('redis'), 'memcached' => extension_loaded('memcached'), 'sodium' => extension_loaded('sodium'), 'mbstring' => extension_loaded('mbstring')],
        ]);
        return Response::html($html, $status);
    }

    private function keyFile(): string
    {
        return $this->root . '/storage/setup.key';
    }

    private function submit(Request $req, string $token): Response
    {
        $old = $req->post;
        unset($old['setup_key'], $old['db_pass'], $old['admin_password'], $old['admin_password_confirm'], $old['_token']);
        $old = array_map(static fn (mixed $v): string => is_string($v) ? $v : '', $old);
        $fail = fn (array $errors, int $status = 422): Response => $this->form($token, $errors, $old, $status);

        if (!hash_equals($token, $req->input('_token'))) {
            return $fail(['form' => 'The form expired. Reload the page and try again.'], 419);
        }
        // The setup key proves you can reach the site's files.
        $keyFile = $this->keyFile();
        $expected = is_file($keyFile) ? trim((string) file_get_contents($keyFile)) : '';
        if (strlen($expected) < 16) {
            return $fail(['setup_key' => 'Create the file ' . 'storage/setup.key' . ' first (see the instructions above).']);
        }
        if (!hash_equals($expected, trim($req->input('setup_key')))) {
            usleep(800000);
            return $fail(['setup_key' => 'That setup key is not right.']);
        }

        $v = new Validator($req->post);
        $v->text('db_host', 1, 190, 'Database host')->text('db_name', 1, 64, 'Database name')->text('db_user', 1, 80, 'Database user');
        $v->text('admin_name', 1, 120, 'name')->email('admin_email');
        $v->password('admin_password', $v->value('admin_email'), $v->value('admin_name'));
        if ($v->value('admin_password') !== $v->value('admin_password_confirm')) {
            $v->fail('admin_password_confirm', 'The two passwords differ.');
        }
        $port = filter_var($v->value('db_port') === '' ? '3306' : $v->value('db_port'), FILTER_VALIDATE_INT, ['options' => ['min_range' => 1, 'max_range' => 65535]]);
        if ($port === false) {
            $v->fail('db_port', 'Enter a port number.');
        }
        $driver = $v->value('cache_driver') === '' ? 'file' : $v->value('cache_driver');
        $v->in('cache_driver', ['file', 'redis', 'memcached'], 'Choose a cache.');
        $url = rtrim($v->value('site_url'), '/');
        if (preg_match('#^https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?$#', $url) !== 1) {
            $v->fail('site_url', 'Enter the address of the site, for example https://nimvor.com');
        }
        if ($v->fails()) {
            return $fail($v->errors());
        }

        $config = [
            'url' => $url,
            'db' => ['host' => $v->value('db_host'), 'port' => (int) $port, 'name' => $v->value('db_name'), 'user' => $v->value('db_user'), 'pass' => $req->input('db_pass')],
            'cache' => ['driver' => $driver, 'redis' => ['host' => $v->value('cache_host') ?: '127.0.0.1', 'port' => (int) ($v->value('cache_port') ?: 6379), 'password' => $req->input('cache_password') ?: null], 'memcached' => ['host' => $v->value('cache_host') ?: '127.0.0.1', 'port' => (int) ($v->value('cache_port') ?: 11211)]],
        ];

        // Each step reports its own problem, without the password.
        try {
            $db = new Db($config['db']);
        } catch (\Throwable) {
            return $fail(['db_host' => 'The database refused the connection. Check the host, port, name, user and password, and that the user may use this database.']);
        }
        // Loaded in its own scope: config.php assigns a variable called $config.
        $full = (static fn (string $file): array => require $file)($this->root . '/config/config.php');
        $cacheConfig = array_replace_recursive($full['cache'], $config['cache']);
        try {
            $cache = CacheFactory::make($cacheConfig);
            $cache->set('setup:probe', 1, 10);
            if ($cache->get('setup:probe') !== 1) {
                throw new \RuntimeException('cache did not store');
            }
        } catch (\Throwable) {
            return $fail(['cache_driver' => 'The cache could not be used. Choose "Files" if Redis or Memcached is not available.']);
        }
        try {
            $db->migrate($this->root . '/database/migrations');
            if ((int) $db->scalar('SELECT COUNT(*) FROM posts') === 0 && is_file($this->root . '/database/seed.php')) {
                foreach ((array) (require $this->root . '/database/seed.php')['posts'] as $p) {
                    $db->run("INSERT INTO posts (slug, title, excerpt, body, status, published_at, created_at, updated_at) VALUES (?, ?, ?, ?, 'published', UTC_TIMESTAMP(), UTC_TIMESTAMP(), UTC_TIMESTAMP())", [$p['slug'], $p['title'], $p['excerpt'], $p['body']]);
                }
            }
            if ($db->one('SELECT id FROM users WHERE email = ?', [strtolower($v->value('admin_email'))]) === null) {
                $db->run("INSERT INTO users (email, name, password_hash, role, created_at) VALUES (?, ?, ?, 'admin', UTC_TIMESTAMP())", [strtolower($v->value('admin_email')), $v->value('admin_name'), Auth::hash($v->value('admin_password'))]);
            }
        } catch (\Throwable $e) {
            error_log('nimvor-site setup: ' . $e->getMessage());
            return $fail(['form' => 'The database tables could not be created. Does the database user have the right to create tables? Details are in the server error log.']);
        }
        if (!$this->writeConfig($config)) {
            return $fail(['form' => 'The database is ready, but config/local.php could not be written. Make the folders config and storage writable for the web server user, then submit again (nothing is duplicated).']);
        }
        @file_put_contents($this->root . '/storage/installed.lock', gmdate('c') . "\n");
        @unlink($keyFile);

        $view = new View($this->root . '/resources/views', $this->root . '/public', ['path' => '/setup', 'siteUrl' => '', 'flash' => null, 'user' => null, 'errors' => [], 'old' => []]);
        $view->share('v', $view);
        return Response::html($view->render('setup/done', ['email' => strtolower($v->value('admin_email'))]));
    }

    /** @param array<string, mixed> $config */
    private function writeConfig(array $config): bool
    {
        $file = $this->root . '/config/local.php';
        $tmp = $file . '.' . bin2hex(random_bytes(4)) . '.tmp';
        $php = "<?php\n\ndeclare(strict_types=1);\n\n// Written by the setup page. Contains the database password: keep it private.\nreturn " . var_export($config, true) . ";\n";
        if (@file_put_contents($tmp, $php, LOCK_EX) === false) {
            return false;
        }
        @chmod($tmp, 0600);
        return @rename($tmp, $file);
    }
}
