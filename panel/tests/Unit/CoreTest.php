<?php

declare(strict_types=1);

namespace WhaCP\Tests\Unit;

use PHPUnit\Framework\TestCase;
use RuntimeException;
use WhaCP\Core\Config;
use WhaCP\Core\Container;
use WhaCP\Http\Request;

final class CoreTest extends TestCase
{
    public function testConfigMergesOverridesAndReadsDotPaths(): void
    {
        $dir = sys_get_temp_dir() . '/whacp-cfg-' . bin2hex(random_bytes(4));
        mkdir($dir);
        file_put_contents("$dir/a.php", "<?php return ['app' => ['name' => 'X', 'debug' => false], 'list' => [1, 2, 3]];");
        file_put_contents("$dir/b.php", "<?php return ['app' => ['debug' => true], 'list' => [9]];");
        $cfg = Config::load("$dir/a.php", "$dir/b.php");
        self::assertSame('X', $cfg->get('app.name'));
        self::assertTrue($cfg->bool('app.debug'));
        self::assertSame([9], $cfg->get('list'), 'lists are replaced, not merged');
        self::assertSame('d', $cfg->get('missing.key', 'd'));
        self::assertSame('X', Config::load("$dir/a.php", "$dir/none.php")->get('app.name'));
        array_map('unlink', glob("$dir/*.php") ?: []);
        rmdir($dir);
    }

    public function testContainerAutowiresAndShares(): void
    {
        $c = new Container();
        $a = $c->get(CoreFixtureB::class);
        self::assertInstanceOf(CoreFixtureA::class, $a->a);
        self::assertSame($a, $c->get(CoreFixtureB::class));
        self::assertSame($a->a, $c->get(CoreFixtureA::class));
    }

    public function testContainerDetectsCycles(): void
    {
        $this->expectException(RuntimeException::class);
        (new Container())->get(CoreFixtureCycle1::class);
    }

    public function testRequestIpIgnoresForwardedHeaderFromUntrustedPeer(): void
    {
        $req = new Request('GET', '/', [], [], ['REMOTE_ADDR' => '203.0.113.9'], ['x-forwarded-for' => '1.2.3.4']);
        self::assertSame('203.0.113.9', $req->ip());
        self::assertSame('203.0.113.9', $req->ip(['10.0.0.1']));
        $proxied = new Request('GET', '/', [], [], ['REMOTE_ADDR' => '10.0.0.1'], ['x-forwarded-for' => 'spoof, 198.51.100.7']);
        self::assertSame('198.51.100.7', $proxied->ip(['10.0.0.1']));
    }

    public function testRequestJsonInput(): void
    {
        $req = new Request('POST', '/x', [], [], [], ['content-type' => 'application/json'], '{"a":"1","b":[1]}');
        self::assertSame('1', $req->input('a'));
        self::assertSame('1', $req->string('a'));
        self::assertSame('', $req->string('b'));
    }

    public function testHelpers(): void
    {
        self::assertSame('&lt;script&gt;&quot;x&apos;', e('<script>"x\''));
        self::assertSame('', e(['array']));
        self::assertSame('1.5 KB', format_bytes(1536));
        self::assertSame('—', format_bytes(null));
        self::assertSame('2d 3h', format_duration(2 * 86400 + 3 * 3600 + 5));
        self::assertSame('5 min ago', time_ago(gmdate('c', 1000 - 300), 1000));
        self::assertSame('never', time_ago(null));
    }
}

final class CoreFixtureA
{
}

final class CoreFixtureB
{
    public function __construct(public readonly CoreFixtureA $a)
    {
    }
}

final class CoreFixtureCycle1
{
    public function __construct(public readonly CoreFixtureCycle2 $x)
    {
    }
}

final class CoreFixtureCycle2
{
    public function __construct(public readonly CoreFixtureCycle1 $x)
    {
    }
}
