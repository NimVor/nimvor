<?php

declare(strict_types=1);

namespace NimVor\Tests\Unit;

use NimVor\Services\ChangelogService;
use PHPUnit\Framework\TestCase;

final class ChangelogServiceTest extends TestCase
{
    public function testItReadsTheNewestReleasesAndSkipsUnreleased(): void
    {
        $f = tempnam(sys_get_temp_dir(), 'cl');
        file_put_contents($f, "# Changelog\n\n## [Unreleased]\n\n### Added\n- Not out yet.\n\n## [2.0.0] — 2026-02-01\n\nBig release.\n\n### Added\n- **New** thing\n  that wraps.\n### Fixed\n- A bug\n\n## [1.0.0] — 2026-01-01\n\n### Added\n- First\n\n## [0.9.0] — 2025-12-01\n\n### Added\n- Old\n");
        $r = (new ChangelogService($f))->releases(2);
        unlink($f);
        self::assertSame(['2.0.0', '1.0.0'], array_column($r, 'version'));
        self::assertSame('2026-02-01', $r[0]['date']);
        self::assertSame('Big release.', $r[0]['intro']);
        self::assertSame(['Added', 'Fixed'], array_column($r[0]['groups'], 'title'));
        self::assertSame(['**New** thing that wraps.'], $r[0]['groups'][0]['items']);
    }

    public function testAMissingFileGivesNothing(): void
    {
        self::assertSame([], (new ChangelogService('/nonexistent/CHANGELOG.md'))->releases());
    }
}
