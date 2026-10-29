<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Unit;

use NimVorSite\Services\DocsCatalog;
use PHPUnit\Framework\TestCase;

final class DocsCatalogTest extends TestCase
{
    public function testWhichFilesArePublishedAndWhere(): void
    {
        self::assertSame('index', DocsCatalog::entry('docs/README.md')['slug']);
        self::assertSame('installation', DocsCatalog::entry('docs/installation/README.md')['slug']);
        self::assertSame('security/installer-review', DocsCatalog::entry('docs/security/installer-review.md')['slug']);
        self::assertSame('migration/whacp-to-nimvor', DocsCatalog::entry('docs/migration/WHACP-TO-NIMVOR.md')['slug']);
        self::assertSame('security-policy', DocsCatalog::entry('SECURITY.md')['slug']);
        self::assertSame('contributing', DocsCatalog::entry('CONTRIBUTING.md')['slug']);
        foreach (['docs/release/1.0.0-ga-report.md', 'docs/development/app-installer-plan.md', 'README.md', 'AGENTS.md', 'panel/README.md', 'docs/../etc.md', 'docs/a b.md'] as $hidden) {
            self::assertNull(DocsCatalog::entry($hidden), $hidden);
        }
        self::assertSame(['slug' => 'extra/new-page', 'group' => 'More', 'order' => 900, 'label' => 'New page'], DocsCatalog::entry('docs/extra/new-page.md'));
    }

    public function testAddresses(): void
    {
        self::assertSame('/docs', DocsCatalog::url('index'));
        self::assertSame('/docs/agent', DocsCatalog::url('agent'));
    }

    public function testLinksInTheDocumentationAreRewritten(): void
    {
        $slugs = ['docs/README.md' => 'index', 'docs/installation/README.md' => 'installation', 'docs/security/README.md' => 'security', 'SECURITY.md' => 'security-policy'];
        $r = static fn (string $from, string $t): string => DocsCatalog::resolve($from, $t, $slugs, 'https://github.com/NimVor/nimvor', 'main');
        self::assertSame('/docs/installation', $r('docs/README.md', 'installation/README.md'));
        self::assertSame('/docs/security#threats', $r('docs/installation/README.md', '../security/README.md#threats'));
        self::assertSame('/docs/installation', $r('docs/installation/README.md', './README.md'));
        self::assertSame('/docs/security', $r('docs/README.md', 'security/'));
        self::assertSame('/docs/security-policy', $r('docs/security/README.md', '../../SECURITY.md'));
        self::assertSame('#local', $r('docs/README.md', '#local'));
        self::assertSame('https://example.org/x', $r('docs/README.md', 'https://example.org/x'));
        self::assertSame('https://github.com/NimVor/nimvor/blob/main/installer/install.sh', $r('docs/installation/README.md', '../../installer/install.sh'));
        self::assertSame('https://github.com/NimVor/nimvor/tree/main/installer', $r('docs/installation/README.md', '../../installer/'));
        self::assertSame('', $r('docs/README.md', '//evil.test/x'));
        $escaped = $r('docs/README.md', '../../../../etc/passwd');
        self::assertStringStartsWith('https://github.com/NimVor/nimvor/', $escaped, 'climbing out of the repository stays inside it');
        self::assertStringNotContainsString('..', $escaped);
    }
}
