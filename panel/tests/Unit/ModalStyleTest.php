<?php

declare(strict_types=1);

namespace NimVor\Tests\Unit;

use PHPUnit\Framework\TestCase;

/** The look of every window (modal): header colours, the close icon and the rule under the header. */
final class ModalStyleTest extends TestCase
{
    public function testModalHeadersUseTheAgreedColours(): void
    {
        $css = (string) file_get_contents(dirname(__DIR__, 2) . '/public/assets/css/nimvor.css');
        self::assertMatchesRegularExpression('/\.modal-header\s*\{[^}]*background-color:\s*#0f766e;[^}]*color:\s*#ffffff;[^}]*border-bottom:\s*3px solid #134e4a;/i', $css);
        self::assertMatchesRegularExpression('/\.modal-header \.modal-title\s*\{[^}]*color:\s*#ffffff;/i', $css);
        self::assertMatchesRegularExpression("/\\.modal-header \\.btn-close\\s*\\{[^}]*fill='%23ff0000'[^}]*opacity:\\s*1;[^}]*filter:\\s*none;/is", $css, 'the close icon is red and not dimmed or inverted in the dark theme');
    }
}
