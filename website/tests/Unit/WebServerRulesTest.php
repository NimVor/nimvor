<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Unit;

use PHPUnit\Framework\TestCase;

/** The Apache rules are plain files; these checks keep them from being lost or weakened. */
final class WebServerRulesTest extends TestCase
{
    private string $root;

    protected function setUp(): void
    {
        $this->root = dirname(__DIR__, 2);
    }

    public function testThePublicFolderRoutesEverythingToTheFrontController(): void
    {
        $h = (string) file_get_contents($this->root . '/public/.htaccess');
        self::assertStringContainsString('RewriteCond %{REQUEST_FILENAME} !-f', $h);
        self::assertStringContainsString('RewriteRule ^ index.php [L]', $h);
        self::assertStringContainsString('Options -Indexes', $h);
        self::assertMatchesRegularExpression('/<FilesMatch "\\\\\.php\$">\s*<IfModule mod_authz_core\.c>\s*Require all denied/', $h, 'only index.php may run PHP');
    }

    public function testTheSiteRootFileBlocksThePrivateFoldersAndRoutesIntoPublic(): void
    {
        $h = (string) file_get_contents($this->root . '/.htaccess');
        self::assertSame(1, preg_match('/RewriteRule \^\(([a-z|]+)\)\(\/\|\$\) - \[R=404,L\]/', $h, $m), 'a rule blocks the private folders');
        $blocked = explode('|', $m[1]);
        foreach (['app', 'bin', 'config', 'database', 'deploy', 'resources', 'storage', 'tests', 'vendor'] as $dir) {
            self::assertContains($dir, $blocked, $dir);
        }
        self::assertStringContainsString('RewriteRule ^(.*)$ public/$1 [L]', $h);
    }

    public function testEveryPrivateFolderDeniesAccessItself(): void
    {
        foreach (['app', 'bin', 'config', 'database', 'deploy', 'resources', 'storage'] as $dir) {
            $h = (string) file_get_contents($this->root . '/' . $dir . '/.htaccess');
            self::assertStringContainsString('Require all denied', $h, $dir);
            self::assertStringContainsString('Deny from all', $h, $dir . ' (Apache 2.2 fallback)');
        }
    }

    public function testExampleConfigurationsExist(): void
    {
        self::assertStringContainsString('DocumentRoot /var/www/nimvor-site/public', (string) file_get_contents($this->root . '/deploy/apache.conf'));
        self::assertStringContainsString('root /var/www/nimvor-site/public', (string) file_get_contents($this->root . '/deploy/nginx.conf'));
    }
}
