<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Tests\Support\SiteTestCase;

final class SecurityTest extends SiteTestCase
{
    public function testSecurityHeadersAreSent(): void
    {
        $h = $this->get('/')->headers;
        self::assertStringContainsString("script-src 'self'", $h['Content-Security-Policy']);
        self::assertStringContainsString("style-src 'self'", $h['Content-Security-Policy']);
        self::assertStringNotContainsString('unsafe-inline', $h['Content-Security-Policy']);
        self::assertStringContainsString("frame-ancestors 'none'", $h['Content-Security-Policy']);
        self::assertSame('nosniff', $h['X-Content-Type-Options']);
        self::assertSame('DENY', $h['X-Frame-Options']);
        self::assertArrayNotHasKey('Strict-Transport-Security', $h, 'only over HTTPS');
        self::assertArrayHasKey('Strict-Transport-Security', $this->get('/', ['HTTPS' => 'on'])->headers);
    }

    public function testNoViewUsesInlineScriptsOrStyles(): void
    {
        $dir = dirname(__DIR__, 2) . '/resources/views';
        $it = new \RecursiveIteratorIterator(new \RecursiveDirectoryIterator($dir, \FilesystemIterator::SKIP_DOTS));
        $checked = 0;
        foreach ($it as $f) {
            if ($f->getExtension() !== 'php') {
                continue;
            }
            $src = (string) file_get_contents($f->getPathname());
            self::assertDoesNotMatchRegularExpression('/<script(?![^>]*\ssrc=)/i', $src, $f->getFilename() . ' has an inline script');
            self::assertDoesNotMatchRegularExpression('/\sstyle\s*=/i', $src, $f->getFilename() . ' has an inline style');
            self::assertDoesNotMatchRegularExpression('/<style[\s>]/i', $src, $f->getFilename() . ' has a style element');
            self::assertDoesNotMatchRegularExpression('/\son[a-z]+\s*=\s*["\']/i', $src, $f->getFilename() . ' has an inline event handler');
            $checked++;
        }
        self::assertGreaterThan(30, $checked);
    }

    public function testEveryPostNeedsACsrfToken(): void
    {
        foreach (['/login', '/register', '/logout', '/support/contact', '/resources/build-services'] as $path) {
            self::assertSame(419, $this->request('POST', $path, ['email' => 'a@b.test'])->status, $path);
        }
        $this->get('/support/contact'); // a session now exists
        self::assertSame(419, $this->request('POST', '/support/contact', ['_token' => str_repeat('0', 64)])->status);
    }

    public function testOutputIsEscaped(): void
    {
        $this->app->posts->save(null, 'xss', '<script>alert(1)</script>', '<img src=x onerror=alert(1)>', "<b>bold</b> [x](javascript:alert(1)) [ok](https://example.test/a)\n\nSecond paragraph, long enough.", 'published', null);
        $page = $this->get('/resources/blog/xss')->body;
        self::assertStringNotContainsString('<script>alert(1)', $page);
        self::assertStringNotContainsString('<img src=x', $page);
        self::assertStringNotContainsString('<b>bold</b>', $page);
        self::assertStringNotContainsString('href="javascript:', $page);
        self::assertStringContainsString('href="https://example.test/a"', $page);
    }

    public function testAdminAndAccountPagesAreNeverCachedOrIndexed(): void
    {
        $this->loginAs('admin');
        $res = $this->get('/admin');
        self::assertSame('private, no-store', $res->headers['Cache-Control']);
        self::assertStringContainsString('noindex', $res->body);
        self::assertStringContainsString('Disallow: /admin', $this->get('/robots.txt')->body);
    }

    public function testTheSessionCookieIsLockedDown(): void
    {
        $res = $this->get('/login', ['HTTPS' => 'on']);
        $cookie = $res->cookies()[0];
        self::assertStringContainsString('HttpOnly', $cookie);
        self::assertStringContainsString('SameSite=Lax', $cookie);
        self::assertStringContainsString('Secure', $cookie);
        self::assertMatchesRegularExpression('/^nimvor_sid=[a-f0-9]{64};/', $cookie);
    }

    public function testAForgedSessionIdGetsNothing(): void
    {
        $this->cookies['nimvor_sid'] = str_repeat('a', 64);
        self::assertSame(303, $this->get('/account')->status, 'redirects to the login page');
        $this->cookies['nimvor_sid'] = "../../etc/passwd";
        self::assertSame(303, $this->get('/account')->status);
    }
}
