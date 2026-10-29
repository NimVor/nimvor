<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Tests\Support\SiteTestCase;

final class PagesTest extends SiteTestCase
{
    /** @return array<string, array{string, string}> */
    public static function pages(): array
    {
        return [
            'home' => ['/', 'in your hands'],
            'features' => ['/features', 'Websites and domains'],
            'resources' => ['/resources', 'Build services'],
            'documentation' => ['/resources/documentation', 'Administrator commands'],
            'install' => ['/get-started', 'Your command'],
            'blog' => ['/resources/blog', 'Blog'],
            'community' => ['/resources/community', 'Report a problem'],
            'build services' => ['/resources/build-services', 'Send request'],
            'sponsors' => ['/resources/sponsors', 'Become a sponsor'],
            'about' => ['/resources/about', 'The rules we follow'],
            'support' => ['/support', 'Security problem'],
            'contact' => ['/support/contact', 'Send message'],
            'privacy' => ['/support/privacy', 'What we store'],
            'login' => ['/login', 'Log in'],
            'register' => ['/register', 'Create an account'],
        ];
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('pages')]
    public function testEveryPublicPageRenders(string $path, string $needle): void
    {
        $res = $this->get($path);
        self::assertSame(200, $res->status, $path);
        self::assertStringContainsString($needle, $res->body, $path);
        self::assertStringContainsString('<title>', $res->body);
        self::assertStringContainsString('rel="canonical"', $res->body);
    }

    public function testTheNavigationHasEveryRequestedItem(): void
    {
        $html = $this->get('/')->body;
        foreach (['Home', 'Features', 'Resources', 'Documentation', 'Blog', 'Community &amp; source', 'Build services', 'Sponsors', 'About us', 'Support', 'Contact us', 'Privacy policy', 'Log in'] as $label) {
            self::assertStringContainsString($label, $html, $label);
        }
    }

    public function testUnknownPagesAndMethodsAreRefused(): void
    {
        self::assertSame(404, $this->get('/nope')->status);
        self::assertSame(404, $this->get('/resources/blog/does-not-exist')->status);
        self::assertSame(405, $this->request('PUT', '/features')->status);
    }

    public function testRobotsSitemapAndHealth(): void
    {
        self::assertStringContainsString('Disallow: /admin', $this->get('/robots.txt')->body);
        $map = $this->get('/sitemap.xml')->body;
        self::assertStringContainsString('<loc>https://nimvor.test/features</loc>', $map);
        self::assertStringNotContainsString('/admin', $map);
        $h = $this->get('/health');
        self::assertSame([200, "ok\n"], [$h->status, $h->body]);
        self::assertSame(301, $this->get('/sponsor')->status);
    }

    public function testPublicPagesAreCacheableAndSetNoCookie(): void
    {
        $res = $this->get('/features');
        self::assertSame('public, max-age=60', $res->headers['Cache-Control']);
        self::assertSame([], $res->cookies());
    }

    public function testPagesWithFormsAreNeverCached(): void
    {
        $res = $this->get('/support/contact');
        self::assertSame('private, no-store', $res->headers['Cache-Control']);
        self::assertCount(1, $res->cookies());
    }

    public function testVisitsAreCountedPerRouteWithoutIdentifiers(): void
    {
        $this->get('/features');
        $this->get('/features');
        $this->get('/resources/blog');
        $this->get('/nope');
        $rows = $this->app->db->all('SELECT route, hits FROM visits ORDER BY route');
        self::assertSame([['route' => '/features', 'hits' => 2], ['route' => '/resources/blog', 'hits' => 1]], array_map(static fn (array $r): array => ['route' => $r['route'], 'hits' => (int) $r['hits']], $rows));
        $cols = array_column($this->app->db->all('SHOW COLUMNS FROM visits'), 'Field');
        self::assertSame(['day', 'route', 'hits'], $cols, 'no address or identifier is stored');
    }

    public function testABlogPostShowsAndDraftsDoNot(): void
    {
        $this->app->posts->save(null, 'hello', 'Hello world', 'A summary', "First paragraph.\n\n## A heading\n\n- one\n- two", 'published', null);
        $this->app->posts->save(null, 'secret', 'Secret draft', 'x', 'Not public text here, long enough.', 'draft', null);
        self::assertStringContainsString('A heading', $this->get('/resources/blog/hello')->body);
        self::assertSame(404, $this->get('/resources/blog/secret')->status);
        self::assertStringNotContainsString('Secret draft', $this->get('/resources/blog')->body);
        self::assertStringNotContainsString('/resources/blog/secret', $this->get('/sitemap.xml')->body);
    }

    /** The install instructions name only options and folders the installer really has. */
    public function testInstallInstructionsMatchTheInstaller(): void
    {
        $installer = dirname(__DIR__, 3) . '/installer';
        if (!is_file($installer . '/install.sh')) {
            self::markTestSkipped('the installer is not next to the site');
        }
        $source = (string) file_get_contents($installer . '/install.sh') . (string) file_get_contents($installer . '/bootstrap.sh');
        $body = $this->get('/get-started')->body;
        self::assertStringContainsString('id="install"', $body);
        preg_match_all('/<code>(--[a-z-]+)/', $body, $m);
        self::assertGreaterThan(15, count(array_unique($m[1])), 'the options table is there');
        foreach (array_unique($m[1]) as $option) {
            self::assertStringContainsString($option, $source, $option . ' is not an option of the installer');
        }
        foreach (['--yes', '--resume', '--enable-writes', '--php=8.3,8.4', '--profile=full', '--hostname=', '--admin-user=', '--admin-email=', '--public-https'] as $needed) {
            self::assertStringContainsString($needed, $body, $needed . ' is missing from the install instructions');
        }
        foreach (['PREFIX=/opt/nimvor', 'CONF_DIR=/etc/nimvor', 'DATA_DIR=/var/lib/nimvor', 'LOG_DIR=/var/log/nimvor', 'BIN=/usr/local/bin/nimvor-agent', 'DIR=${NIMVOR_DIR:-/usr/local/src/nimvor}'] as $path) {
            self::assertStringContainsString($path, $source, $path);
        }
        foreach (['/opt/nimvor', '/etc/nimvor', '/var/lib/nimvor', '/var/log/nimvor', '/usr/local/bin/nimvor-agent', '/usr/local/src/nimvor'] as $dir) {
            self::assertStringContainsString('<code>' . $dir . '</code>', $body, $dir . ' is not on the page');
        }
    }

    public function testInstallPageHasItsOwnTitleAndTheCommandBuilder(): void
    {
        $res = $this->get('/get-started');
        self::assertSame(200, $res->status);
        $b = $res->body;
        self::assertStringContainsString('<title>Install NimVor: a free control panel for Ubuntu, AlmaLinux and Rocky Linux · NimVor</title>', $b);
        self::assertStringContainsString('<h1>Install NimVor</h1>', $b);
        self::assertStringContainsString('<meta name="description" content="Install NimVor in one command.', $b);
        self::assertStringContainsString('<link rel="canonical" href="https://nimvor.test/get-started">', $b);
        self::assertStringContainsString('id="install-builder"', $b);
        self::assertStringContainsString('id="install-static"', $b, 'without JavaScript the full command is still shown');
        self::assertMatchesRegularExpression('#<script src="/assets/js/install-builder\.js\?v=[a-f0-9]{8}" defer></script>#', $b);
        self::assertSame(0, preg_match('#<script(?![^>]*\bsrc=)#i', $b), 'no inline script: the Content-Security-Policy would block it');
        self::assertDoesNotMatchRegularExpression('# (on[a-z]+|style)="#i', (string) preg_replace('#<svg.*?</svg>#s', '', $b), 'no inline handlers or styles');
        foreach (['full', 'web', 'minimal', 'none'] as $profile) {
            self::assertStringContainsString('<option value="' . $profile . '"', $b);
        }
        foreach (['8.0', '8.1', '8.2', '8.3', '8.4'] as $php) {
            self::assertStringContainsString('name="php" value="' . $php . '"', $b);
        }
        foreach (['hostname', 'adminUser', 'adminEmail', 'publicHttps', 'httpsPort', 'port', 'yes', 'resume', 'dryRun', 'version', 'commit'] as $field) {
            self::assertStringContainsString('name="' . $field . '"', $b, $field);
        }
        self::assertStringContainsString('/get-started', $this->get('/')->body, 'the home page links to the install page');
        self::assertStringContainsString('/get-started', $this->get('/features')->body);
        $home = $this->get('/')->body;
        self::assertStringContainsString('<a href="https://whasys.com" rel="noopener">WhaSys</a>', $home, 'the footer credits WhaSys');
        self::assertStringContainsString('data-tagline', $home, 'the tagline is shown above the footer');
        self::assertStringNotContainsString('Not affiliated with any other control panel', $home);
        self::assertStringNotContainsStringIgnoringCase('whacp', $this->get('/about')->body, 'the old project name is not mentioned');
        $features = $this->get('/features')->body;
        foreach (['ModSecurity', 'ionCube', 'MongoDB', 'Cloudflare', 'Greylisting', 'WordPress Toolkit', 'Node.js and Python', 'Monitoring and health'] as $topic) {
            self::assertStringContainsString($topic, $features, $topic . ' is on the features page');
        }
        self::assertStringContainsString('<video class="tour-video"', $home, 'the home page shows the product tour');
        self::assertStringContainsString('media/nimvor-panel-tour.mp4', $home);
        self::assertFileExists(dirname(__DIR__, 2) . '/public/assets/media/nimvor-panel-tour.mp4');
        self::assertStringContainsString('/get-started', $this->get('/sitemap.xml')->body);
        $docs = $this->get('/resources/documentation')->body;
        self::assertStringContainsString('<h1>Documentation</h1>', $docs);
        self::assertStringContainsString('href="/get-started"', $docs);
        self::assertStringNotContainsString('install-builder.js', $docs, 'only the install page loads the builder');
    }
}
