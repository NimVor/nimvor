<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Unit;

use NimVorSite\Support\Markdown;
use PHPUnit\Framework\TestCase;

final class MarkdownTest extends TestCase
{
    private function html(string $md, ?\Closure $resolve = null): string
    {
        return (new Markdown($resolve))->render($md)['html'];
    }

    public function testBlocksAndInlineMarkup(): void
    {
        $h = $this->html("# Title *here*\n\nHello **bold**, `a | b`, snake_case_word and [link](https://x.org/a_b?c=1&d=2).\n\n> quoted\n\n---\n");
        self::assertStringContainsString('<h1 id="title-here">Title <em>here</em></h1>', $h);
        self::assertStringContainsString('<strong>bold</strong>', $h);
        self::assertStringContainsString('<code>a | b</code>', $h);
        self::assertStringContainsString('snake_case_word', $h, 'underscores inside words are not emphasis');
        self::assertStringContainsString('<a href="https://x.org/a_b?c=1&amp;d=2" rel="noopener nofollow" class="ext">link</a>', $h);
        self::assertStringContainsString('<blockquote>', $h);
        self::assertStringContainsString('<hr>', $h);
    }

    public function testRawHtmlAndDangerousLinksAreNeverOutput(): void
    {
        $h = $this->html("<script>alert(1)</script> <img src=x onerror=alert(1)>\n\n[bad](javascript:alert(1)) [also](data:text/html,x) [proto](//evil.test/x)\n\n![pic](https://x.test/i.png)\n");
        self::assertStringNotContainsString('<script', $h);
        self::assertStringNotContainsString('<img', $h);
        self::assertStringContainsString('&lt;script&gt;', $h);
        self::assertStringNotContainsString('javascript:', $h);
        self::assertStringNotContainsString('data:text', $h);
        self::assertStringNotContainsString('//evil.test', $h);
        self::assertStringContainsString('<a href="https://x.test/i.png"', $h, 'an image becomes a link');
    }

    public function testNestedListsTaskListsAndBlocksInsideItems(): void
    {
        $h = $this->html("- one\n  lazy\n- two\n\n  second paragraph\n\n  ```bash\n  echo hi\n  ```\n- [ ] todo\n- [x] done\n  - nested\n\n1. first\n2. second\n");
        self::assertStringContainsString('<li><p>two</p>', $h);
        self::assertStringContainsString('<p>second paragraph</p>', $h);
        self::assertStringContainsString('<pre class="codeblock"><code class="language-bash">echo hi</code></pre>', $h);
        self::assertStringContainsString('<input type="checkbox" disabled checked>', $h);
        self::assertStringContainsString('<ul>', $h);
        self::assertStringContainsString('<ol>', $h);
        self::assertSame(2, substr_count($h, '<ul>'), 'a nested list is a list of its own');
    }

    public function testTightListsHaveNoParagraphs(): void
    {
        $h = $this->html("- a\n- b\n");
        self::assertStringContainsString("<li>a</li>\n<li>b</li>", $h);
    }

    public function testTablesWithAlignmentAndEscapedPipes(): void
    {
        $h = $this->html("| A | B |\n|---|:-:|\n| `x\\|y` | **b** |\n");
        self::assertStringContainsString('<div class="table-wrap"><table>', $h);
        self::assertStringContainsString('<th class="al-c">B</th>', $h);
        self::assertStringContainsString('<td><code>x|y</code></td>', $h);
    }

    public function testHeadingIdsTocAndTitle(): void
    {
        $r = (new Markdown())->render("# Guide\n\n## Install it\n\n### Install it\n\n## Install it\n");
        self::assertSame('Guide', $r['title']);
        self::assertSame(['install-it', 'install-it-1', 'install-it-2'], array_column($r['toc'], 'id'), 'repeated headings get distinct ids, as on GitHub');
        self::assertSame([2, 3, 2], array_column($r['toc'], 'level'));
    }

    public function testLinksCanBeRewrittenOrDropped(): void
    {
        $h = $this->html("[a](../x/README.md#s) [b](keep.md) [c](https://ext.test)", static fn (string $t): string => $t === 'keep.md' ? '' : ($t === 'https://ext.test' ? $t : '/docs/x#s'));
        self::assertStringContainsString('<a href="/docs/x#s">a</a>', $h);
        self::assertStringNotContainsString('keep.md', str_replace('>b<', '', $h), 'a link the resolver drops is shown as plain text');
        self::assertStringContainsString('b', $h);
        self::assertStringContainsString('class="ext"', $h);
    }

    public function testTheProjectsOwnDocumentationRendersCleanly(): void
    {
        $root = dirname(__DIR__, 3);
        $files = array_merge(glob($root . '/docs/*.md') ?: [], glob($root . '/docs/*/*.md') ?: [], glob($root . '/*.md') ?: []);
        if ($files === []) {
            self::markTestSkipped('the repository documentation is not next to the site');
        }
        foreach ($files as $f) {
            $r = (new Markdown())->render((string) file_get_contents($f));
            $outside = preg_replace('#<code[^>]*>.*?</code>|<pre.*?</pre>#s', '', $r['html']) ?? '';
            self::assertStringNotContainsString("\x00", $r['html'], $f);
            self::assertStringNotContainsString('<script', $r['html'], $f);
            self::assertStringNotContainsString('](', $outside, $f . ' has a link that was not understood');
            self::assertStringNotContainsString('```', $outside, $f);
            self::assertNotSame('', $r['title'], $f . ' has no top heading');
        }
    }
}
