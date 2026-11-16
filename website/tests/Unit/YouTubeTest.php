<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Unit;

use NimVorSite\Support\YouTube;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;

final class YouTubeTest extends TestCase
{
    /** @return array<string, array{string, ?string}> */
    public static function links(): array
    {
        $id = 'dQw4w9WgXcQ';
        return [
            'watch' => ["https://www.youtube.com/watch?v={$id}", $id],
            'watch with extras' => ["https://youtube.com/watch?feature=share&v={$id}&t=30s", $id],
            'mobile' => ["https://m.youtube.com/watch?v={$id}", $id],
            'short link' => ["https://youtu.be/{$id}?si=abc", $id],
            'embed' => ["https://www.youtube.com/embed/{$id}", $id],
            'nocookie embed' => ["https://www.youtube-nocookie.com/embed/{$id}?rel=0", $id],
            'shorts' => ["https://www.youtube.com/shorts/{$id}", $id],
            'live' => ["https://www.youtube.com/live/{$id}", $id],
            'bare id' => [$id, $id],
            'padded' => ["  https://youtu.be/{$id}  ", $id],
            'http is refused' => ["http://www.youtube.com/watch?v={$id}", null],
            'other host' => ["https://evil.example/watch?v={$id}", null],
            'lookalike host' => ["https://youtube.com.evil.example/watch?v={$id}", null],
            'playlist only' => ['https://www.youtube.com/playlist?list=PLabc', null],
            'short id' => ['https://youtu.be/abc', null],
            'script' => ['javascript:alert(1)', null],
            'empty' => ['', null],
        ];
    }

    #[DataProvider('links')]
    public function testIdIsReadFromTheLinkOrRefused(string $input, ?string $expected): void
    {
        self::assertSame($expected, YouTube::id($input));
    }

    public function testEmbedUsesThePrivacyEnhancedHost(): void
    {
        self::assertSame('https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?rel=0', YouTube::embedUrl('dQw4w9WgXcQ'));
    }
}
