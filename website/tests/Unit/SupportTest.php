<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Unit;

use NimVorSite\Cache\FileCache;
use NimVorSite\Core\Response;
use NimVorSite\Support\Text;
use NimVorSite\Support\Validator;
use PHPUnit\Framework\TestCase;

final class SupportTest extends TestCase
{
    public function testTextRendersOnlyTheSafeSubset(): void
    {
        $html = Text::html("## Title\n\nA **bold** and `code` word.\n\n- one\n- two\n\n[site](https://example.test/x?a=1&b=2)");
        self::assertStringContainsString('<h2>Title</h2>', $html);
        self::assertStringContainsString('<strong>bold</strong>', $html);
        self::assertStringContainsString('<code>code</code>', $html);
        self::assertStringContainsString('<ul><li>one</li><li>two</li></ul>', $html);
        self::assertStringContainsString('<a href="https://example.test/x?a=1&amp;b=2" rel="noopener nofollow">site</a>', $html);
    }

    public function testTextNeverLinksNonHttpsOrInjectsMarkup(): void
    {
        $html = Text::html('<script>x</script> [a](http://example.test) [b](javascript:alert(1)) [c](data:text/html,x) [d](https://ok.test/"onmouseover="x)');
        self::assertStringNotContainsString('<script>', $html);
        self::assertStringNotContainsString('href="http://', $html);
        self::assertStringNotContainsString('href="javascript', $html);
        self::assertStringNotContainsString('href="data:', $html);
        self::assertStringNotContainsString('onmouseover="x', $html);
    }

    public function testSlug(): void
    {
        self::assertSame('hello-world-2026', Text::slug('Hello, World! 2026'));
        self::assertSame('post', Text::slug('!!!'));
        self::assertLessThanOrEqual(100, strlen(Text::slug(str_repeat('a', 300))));
    }

    public function testValidator(): void
    {
        $v = new Validator(['name' => '  Ada ', 'email' => 'ada@example.test', 'pw' => 'short', 'n' => ['array']]);
        $v->text('name', 1, 10, 'name')->email()->password('pw')->text('n', 1, 5, 'n');
        self::assertSame('Ada', $v->value('name'));
        self::assertSame(['pw', 'n'], array_keys($v->errors()));
        self::assertTrue($v->fails());
    }

    public function testARedirectNeverLeavesTheSite(): void
    {
        foreach (['https://evil.test', '//evil.test', 'javascript:alert(1)', "/a\nSet-Cookie: x"] as $to) {
            self::assertSame('/', Response::redirect($to)->headers['Location'], $to);
        }
        self::assertSame('/account?x=1', Response::redirect('/account?x=1')->headers['Location']);
    }

    public function testFileCacheStoresExpiresAndCounts(): void
    {
        $c = new FileCache(sys_get_temp_dir() . '/nimsite-' . bin2hex(random_bytes(4)), 'p:');
        $c->set('a', ['x' => 1], 60);
        self::assertSame(['x' => 1], $c->get('a'));
        $c->set('gone', 1, 1);
        sleep(2);
        self::assertNull($c->get('gone'));
        self::assertSame(1, $c->increment('n', 60));
        self::assertSame(2, $c->increment('n', 60));
        $c->delete('a');
        self::assertNull($c->get('a'));
    }
}
