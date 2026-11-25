<?php

declare(strict_types=1);

namespace NimVor\Tests\Unit;

use PHPUnit\Framework\TestCase;

/** The look of every window (modal): header colours, the close icon and the rule under the header; and the sidebar's colour. */
final class ModalStyleTest extends TestCase
{
    public function testModalHeadersUseTheAgreedColours(): void
    {
        $css = (string) file_get_contents(dirname(__DIR__, 2) . '/public/assets/css/nimvor.css');
        self::assertMatchesRegularExpression('/\.modal-header\s*\{[^}]*background-color:\s*#f6f8f8;[^}]*color:\s*#555;[^}]*border-bottom:\s*1px solid #ccc;/i', $css);
        self::assertMatchesRegularExpression('/\.modal-header \.modal-title\s*\{[^}]*color:\s*#555;/i', $css);
        self::assertMatchesRegularExpression("/\\.modal-header \\.btn-close\\s*\\{[^}]*fill='%23ff0000'[^}]*opacity:\\s*1;[^}]*filter:\\s*none;/is", $css, 'the close icon is red and not dimmed or inverted in the dark theme');
    }

    public function testTheSidebarIsTealWithAGreyEdge(): void
    {
        $css = (string) file_get_contents(dirname(__DIR__, 2) . '/public/assets/css/nimvor.css');
        self::assertMatchesRegularExpression('/\.nim-sidebar\s*\{\s*background:\s*#115f5a;\s*border-right:\s*1px solid #ccc;/i', $css);
    }
}
