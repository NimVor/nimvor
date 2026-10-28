<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Core\Request;
use NimVorSite\Core\Response;
use NimVorSite\Setup\Setup;
use PHPUnit\Framework\TestCase;

/** The first-run page, in a temporary copy of the site's folders so the real config is never touched. */
final class SetupTest extends TestCase
{
    private string $root;
    private ?string $savedDbPass;
    /** @var array<string, string> */
    private array $cookies = [];

    protected function setUp(): void
    {
        $this->savedDbPass = getenv('DB_PASS') === false ? null : (string) getenv('DB_PASS');
        putenv('DB_PASS'); // an environment-configured site is "installed"; this test is about the other kind
        $src = dirname(__DIR__, 2);
        $this->root = sys_get_temp_dir() . '/nimsite-setup-' . bin2hex(random_bytes(4));
        mkdir($this->root . '/config', 0777, true);
        mkdir($this->root . '/storage', 0777, true);
        mkdir($this->root . '/public', 0777, true);
        copy($src . '/config/config.php', $this->root . '/config/config.php');
        $this->copyDir($src . '/database', $this->root . '/database');
        $this->copyDir($src . '/resources', $this->root . '/resources');
        $this->cookies = [];
        $db = new \NimVorSite\Core\Db($this->dbConfig());
        $db->run('SET FOREIGN_KEY_CHECKS = 0');
        foreach (['visits', 'sponsors', 'build_requests', 'contact_messages', 'posts', 'users', 'migrations'] as $t) {
            $db->run("DROP TABLE IF EXISTS {$t}");
        }
        $db->run('SET FOREIGN_KEY_CHECKS = 1');
    }

    protected function tearDown(): void
    {
        if ($this->savedDbPass !== null) {
            putenv('DB_PASS=' . $this->savedDbPass);
        }
        $this->rmDir($this->root);
    }

    /** @return array{host: string, port: int, name: string, user: string, pass: string} */
    private function dbConfig(): array
    {
        $env = static fn (string $k, string $d): string => getenv($k) !== false && getenv($k) !== '' ? (string) getenv($k) : $d;
        return ['host' => $env('DB_HOST', '127.0.0.1'), 'port' => 3306, 'name' => $env('DB_NAME', 'nimvor_site_test'), 'user' => $env('DB_USER', 'site'), 'pass' => $this->savedDbPass ?? 'site-test-pw'];
    }

    private function copyDir(string $from, string $to): void
    {
        mkdir($to, 0777, true);
        foreach (scandir($from) ?: [] as $f) {
            if ($f === '.' || $f === '..') {
                continue;
            }
            is_dir("$from/$f") ? $this->copyDir("$from/$f", "$to/$f") : copy("$from/$f", "$to/$f");
        }
    }

    private function rmDir(string $d): void
    {
        foreach (scandir($d) ?: [] as $f) {
            if ($f !== '.' && $f !== '..') {
                is_dir("$d/$f") ? $this->rmDir("$d/$f") : @unlink("$d/$f");
            }
        }
        @rmdir($d);
    }

    /** @param array<string, string> $post */
    private function call(string $method, string $path, array $post = []): Response
    {
        $res = (new Setup($this->root))->handle(new Request($method, $path, [], $post, $this->cookies, ['REMOTE_ADDR' => '203.0.113.9']));
        foreach ($res->cookies() as $c) {
            [$pair] = explode(';', $c, 2);
            [$n, $v] = explode('=', $pair, 2);
            $this->cookies[$n] = $v;
        }
        return $res;
    }

    /** @return array<string, string> */
    private function form(array $over = []): array
    {
        $db = $this->dbConfig();
        return $over + [
            '_token' => $this->cookies[Setup::COOKIE] ?? '', 'setup_key' => 'a-secret-setup-key-123', 'db_host' => $db['host'], 'db_port' => '3306', 'db_name' => $db['name'], 'db_user' => $db['user'], 'db_pass' => $db['pass'],
            'cache_driver' => 'file', 'cache_host' => '', 'cache_port' => '', 'site_url' => 'https://nimvor.test', 'admin_name' => 'First Admin', 'admin_email' => 'first@example.test',
            'admin_password' => 'a-long-admin-password-1', 'admin_password_confirm' => 'a-long-admin-password-1',
        ];
    }

    private function key(string $value = 'a-secret-setup-key-123'): void
    {
        file_put_contents($this->root . '/storage/setup.key', $value . "\n");
    }

    public function testAnUnconfiguredSiteOnlyShowsTheSetupPage(): void
    {
        self::assertSame('/setup', $this->call('GET', '/')->headers['Location']);
        $res = $this->call('GET', '/setup');
        self::assertSame(200, $res->status);
        self::assertStringContainsString('Set up the NimVor website', $res->body);
        self::assertStringContainsString('not found yet', $res->body, 'it says the key file is missing');
        self::assertSame('no-store', $res->headers['Cache-Control']);
        self::assertStringContainsString('HttpOnly', $res->cookies()[0]);
    }

    public function testNothingWorksWithoutTheKeyFileOrWithAWrongKey(): void
    {
        $this->call('GET', '/setup');
        $res = $this->call('POST', '/setup', $this->form());
        self::assertSame(422, $res->status);
        self::assertStringContainsString('Create the file', $res->body);
        $this->key('some-other-secret-value');
        $bad = $this->call('POST', '/setup', $this->form());
        self::assertStringContainsString('That setup key is not right.', $bad->body);
        self::assertFileDoesNotExist($this->root . '/config/local.php');
        self::assertFileDoesNotExist($this->root . '/storage/installed.lock');
        $this->key('short');
        self::assertStringContainsString('Create the file', $this->call('POST', '/setup', $this->form())->body, 'a key under 16 characters is no key');
    }

    public function testAPostNeedsTheFormsToken(): void
    {
        $this->call('GET', '/setup');
        $this->key();
        self::assertSame(419, $this->call('POST', '/setup', $this->form(['_token' => str_repeat('0', 64)]))->status);
        self::assertFileDoesNotExist($this->root . '/config/local.php');
    }

    public function testSetupCreatesTheTablesTheAdministratorAndLocksItself(): void
    {
        $this->call('GET', '/setup');
        $this->key();
        $res = $this->call('POST', '/setup', $this->form());
        self::assertSame(200, $res->status, strip_tags($res->body));
        self::assertStringContainsString('The site is set up', $res->body);

        $db = new \NimVorSite\Core\Db($this->dbConfig());
        $admin = $db->one('SELECT * FROM users WHERE email = ?', ['first@example.test']);
        self::assertSame('admin', $admin['role']);
        self::assertStringStartsWith('$argon2id$', $admin['password_hash']);
        self::assertSame(1, (int) $db->scalar('SELECT COUNT(*) FROM posts'), 'the first blog post was added');

        $local = $this->root . '/config/local.php';
        self::assertFileExists($local);
        self::assertSame('0600', substr(sprintf('%o', fileperms($local)), -4), 'the file with the password is private');
        clearstatcache(true); // the earlier is_file() on this path was cached within this one process
        $cfg = (static fn (string $f): array => require $f)($this->root . '/config/config.php');
        self::assertSame([$this->dbConfig()['name'], 'file', 'https://nimvor.test'], [$cfg['db']['name'], $cfg['cache']['driver'], $cfg['url']], 'the normal config loads what setup wrote');
        self::assertFileExists($this->root . '/storage/installed.lock');
        self::assertFileDoesNotExist($this->root . '/storage/setup.key', 'the key is deleted');

        $written = (static fn (string $f): array => require $f)($local);
        self::assertSame($this->dbConfig()['pass'], $written['db']['pass'], 'the database password typed in the form was saved');
        self::assertSame(404, $this->call('GET', '/setup')->status, 'the page is gone');
        self::assertSame(404, $this->call('POST', '/setup', $this->form())->status);
    }

    public function testABadDatabasePasswordIsReportedWithoutEchoingIt(): void
    {
        $this->call('GET', '/setup');
        $this->key();
        $res = $this->call('POST', '/setup', $this->form(['db_pass' => 'definitely-wrong-password']));
        self::assertSame(422, $res->status);
        self::assertStringContainsString('refused the connection', $res->body);
        self::assertStringNotContainsString('definitely-wrong-password', $res->body);
        self::assertStringNotContainsString('a-secret-setup-key-123', $res->body);
        self::assertStringNotContainsString('a-long-admin-password-1', $res->body);
        self::assertFileDoesNotExist($this->root . '/config/local.php');
        self::assertFileExists($this->root . '/storage/setup.key', 'the key stays so you can try again');
    }

    public function testTheFormIsValidated(): void
    {
        $this->call('GET', '/setup');
        $this->key();
        $res = $this->call('POST', '/setup', $this->form(['admin_email' => 'nope', 'admin_password' => 'short', 'admin_password_confirm' => 'short', 'site_url' => 'javascript:alert(1)', 'cache_driver' => 'bogus']));
        self::assertSame(422, $res->status);
        foreach (['valid email', 'at least 12', 'address of the site', 'Choose a cache'] as $msg) {
            self::assertStringContainsString($msg, $res->body, $msg);
        }
    }

    public function testASiteConfiguredByTheEnvironmentNeverShowsSetup(): void
    {
        putenv('DB_PASS=anything');
        self::assertTrue(Setup::isInstalled($this->root));
        self::assertSame(404, $this->call('GET', '/setup')->status);
        putenv('DB_PASS');
        file_put_contents($this->root . '/storage/installed.lock', 'x');
        self::assertSame(404, $this->call('GET', '/setup')->status);
    }

    public function testRerunningAfterAConfigWriteFailureDuplicatesNothing(): void
    {
        $this->call('GET', '/setup');
        $this->key();
        mkdir($this->root . '/config/local.php'); // a directory in the way: the file cannot be written
        $first = $this->call('POST', '/setup', $this->form());
        self::assertStringContainsString('could not be written', $first->body);
        rmdir($this->root . '/config/local.php');
        $second = $this->call('POST', '/setup', $this->form());
        self::assertStringContainsString('The site is set up', $second->body);
        $db = new \NimVorSite\Core\Db($this->dbConfig());
        self::assertSame(1, (int) $db->scalar('SELECT COUNT(*) FROM users'));
        self::assertSame(1, (int) $db->scalar('SELECT COUNT(*) FROM posts'));
    }
}
