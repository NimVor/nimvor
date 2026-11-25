<?php

declare(strict_types=1);

namespace NimVor\Tests\Unit;

use PHPUnit\Framework\TestCase;

/**
 * The panel's Content-Security-Policy (style-src 'self') blocks style="…" attributes, in pages and in HTML a script
 * inserts, and the browser logs an error for each. The views below are drawn into hover cards, windows and the swap
 * page; they set widths with data-width (NimVor.applyWidths) and classes instead.
 */
final class NoInlineStylesTest extends TestCase
{
    public function testTheDashboardCardsAndWindowsUseNoInlineStyleAttributes(): void
    {
        $views = dirname(__DIR__, 2) . '/resources/views/';
        foreach (['dashboard/popover.php', 'dashboard/widgets/software.php', 'partials/changelog_card.php', 'partials/changelog_modal.php', 'partials/feedback_modal.php', 'server/swap.php'] as $f) {
            self::assertDoesNotMatchRegularExpression('/\sstyle\s*=\s*["\']/i', (string) file_get_contents($views . $f), $f . ' has an inline style attribute');
        }
    }
}
