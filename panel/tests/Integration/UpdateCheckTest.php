<?php

declare(strict_types=1);

namespace NimVor\Tests\Integration;

use PHPUnit\Framework\TestCase;
use NimVor\Agent\AgentClient;
use NimVor\Support\Logger;
use NimVor\Services\UpdateCheckService;
use NimVor\Tests\Support\FakeTransport;

final class UpdateCheckTest extends TestCase
{
    private string $dir;
    private FakeTransport $transport;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/nimvor-update-' . bin2hex(random_bytes(4));
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
        self::assertSame('acme/nimvor', UpdateCheckService::githubRepo('https://github.com/acme/nimvor.git'));
        self::assertSame('acme/nimvor', UpdateCheckService::githubRepo('git@github.com:acme/nimvor.git'));
        self::assertSame('acme/NimVor', UpdateCheckService::githubRepo('https://github.com/acme/NimVor'));
        self::assertNull(UpdateCheckService::githubRepo('https://gitlab.com/acme/nimvor.git'));
        self::assertNull(UpdateCheckService::githubRepo('https://github.com/acme/nimvor/../../x'));
    }

    public function testNewerCommitIsAnUpdate(): void
    {
        $current = str_repeat('a', 40);
        $this->transport->succeed('nimvor.update_status', ['git' => true, 'commit' => $current, 'branch' => 'main', 'remote' => 'https://github.com/acme/nimvor.git', 'updatable' => true]);
        $asked = [];
        $svc = $this->service(function (string $url) use (&$asked): array {
            $asked[] = $url;
            return [200, str_repeat('b', 40) . "\n"];
        });
        self::assertTrue($svc->due());
        $res = $svc->check();
        self::assertTrue($res['available']);
        self::assertSame(['https://api.github.com/repos/acme/nimvor/commits/main'], $asked);
        self::assertTrue($svc->available());
        self::assertFalse($svc->due());
        // The same commit: up to date.
        $same = $this->service(fn (string $url): array => [200, $current]);
        self::assertSame('current', $same->check()['state']);
        self::assertFalse($same->available());
    }

    public function testPrivateRepositoryIsUnknownNotCurrent(): void
    {
        $this->transport->succeed('nimvor.update_status', ['git' => true, 'commit' => str_repeat('a', 40), 'branch' => 'main', 'remote' => 'https://github.com/acme/private.git']);
        $res = $this->service(fn (string $url): array => [404, '{"message":"Not Found"}'])->check();
        self::assertFalse($res['available']);
        self::assertSame('unknown', $res['state']);
        self::assertStringContainsString('private', $res['reason']);
    }
}
