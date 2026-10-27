<?php

declare(strict_types=1);

namespace WhaCP\Tests\Integration;

use PHPUnit\Framework\TestCase;
use WhaCP\Agent\AgentClient;
use WhaCP\Support\Logger;
use WhaCP\Services\UpdateCheckService;
use WhaCP\Tests\Support\FakeTransport;

final class UpdateCheckTest extends TestCase
{
    private string $dir;
    private FakeTransport $transport;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/whacp-update-' . bin2hex(random_bytes(4));
        mkdir($this->dir);
        file_put_contents($this->dir . '/token', str_repeat('a', 64));
        $this->transport = new FakeTransport();
    }

    protected function tearDown(): void
    {
        array_map('unlink', glob($this->dir . '/*') ?: []);
        @rmdir($this->dir . '/cache');
        @rmdir($this->dir);
    }

    private function service(callable $fetch): UpdateCheckService
    {
        $agent = new AgentClient($this->transport, $this->dir . '/token', new Logger('/dev/null'));
        return new UpdateCheckService($agent, $this->dir . '/check.json', \Closure::fromCallable($fetch));
    }

    public function testGithubRemotes(): void
    {
        self::assertSame('acme/whacp', UpdateCheckService::githubRepo('https://github.com/acme/whacp.git'));
        self::assertSame('acme/whacp', UpdateCheckService::githubRepo('git@github.com:acme/whacp.git'));
        self::assertSame('acme/WhaCP', UpdateCheckService::githubRepo('https://github.com/acme/WhaCP'));
        self::assertNull(UpdateCheckService::githubRepo('https://gitlab.com/acme/whacp.git'));
        self::assertNull(UpdateCheckService::githubRepo('https://github.com/acme/whacp/../../x'));
    }

    public function testNewerCommitIsAnUpdate(): void
    {
        $current = str_repeat('a', 40);
        $this->transport->succeed('whacp.update_status', ['git' => true, 'commit' => $current, 'branch' => 'main', 'remote' => 'https://github.com/acme/whacp.git', 'updatable' => true]);
        $asked = [];
        $svc = $this->service(function (string $url) use (&$asked): array {
            $asked[] = $url;
            return [200, str_repeat('b', 40) . "\n"];
        });
        self::assertTrue($svc->due());
        $res = $svc->check();
        self::assertTrue($res['available']);
        self::assertSame(['https://api.github.com/repos/acme/whacp/commits/main'], $asked);
        self::assertTrue($svc->available());
        self::assertFalse($svc->due());
        // The same commit: up to date.
        $same = $this->service(fn (string $url): array => [200, $current]);
        self::assertSame('current', $same->check()['state']);
        self::assertFalse($same->available());
    }

    public function testPrivateRepositoryIsUnknownNotCurrent(): void
    {
        $this->transport->succeed('whacp.update_status', ['git' => true, 'commit' => str_repeat('a', 40), 'branch' => 'main', 'remote' => 'https://github.com/acme/private.git']);
        $res = $this->service(fn (string $url): array => [404, '{"message":"Not Found"}'])->check();
        self::assertFalse($res['available']);
        self::assertSame('unknown', $res['state']);
        self::assertStringContainsString('private', $res['reason']);
    }
}
