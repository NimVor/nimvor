<?php

declare(strict_types=1);

namespace NimVorSite\Support;

/**
 * Turns an author's plain text into safe HTML. Everything is escaped first; the
 * only markup is "## " and "### " headings, "- " lists, `code`, **bold** and
 * [text](https://…) links.
 */
final class Text
{
    public static function html(string $text): string
    {
        $blocks = preg_split('/\R{2,}/', trim(str_replace("\r\n", "\n", $text))) ?: [];
        $out = '';
        foreach ($blocks as $block) {
            $block = trim($block);
            if ($block === '') {
                continue;
            }
            if (str_starts_with($block, '### ')) {
                $out .= '<h3>' . self::inline(substr($block, 4)) . '</h3>';
            } elseif (str_starts_with($block, '## ')) {
                $out .= '<h2>' . self::inline(substr($block, 3)) . '</h2>';
            } elseif (preg_match('/^- /', $block) === 1) {
                $items = array_filter(array_map(static fn (string $l): string => preg_replace('/^- /', '', trim($l)) ?? '', explode("\n", $block)), 'strlen');
                $out .= '<ul>' . implode('', array_map(static fn (string $i): string => '<li>' . self::inline($i) . '</li>', $items)) . '</ul>';
            } elseif (str_starts_with($block, '```')) {
                $out .= '<pre><code>' . e(trim(preg_replace('/^```[a-z]*\n?|```$/m', '', $block) ?? '')) . '</code></pre>';
            } else {
                $out .= '<p>' . nl2br(self::inline($block), false) . '</p>';
            }
        }
        return $out;
    }

    private static function inline(string $s): string
    {
        $s = e($s);
        $s = preg_replace('/`([^`]+)`/', '<code>$1</code>', $s) ?? $s;
        $s = preg_replace('/\*\*([^*]+)\*\*/', '<strong>$1</strong>', $s) ?? $s;
        return preg_replace_callback('/\[([^\]]+)\]\((https:\/\/[^\s)"<>]+)\)/', static fn (array $m): string => '<a href="' . $m[2] . '" rel="noopener nofollow">' . $m[1] . '</a>', $s) ?? $s;
    }

    public static function slug(string $title): string
    {
        $s = strtolower(trim((string) preg_replace('/[^a-zA-Z0-9]+/', '-', iconv('UTF-8', 'ASCII//TRANSLIT//IGNORE', $title) ?: $title), '-'));
        return substr($s !== '' ? $s : 'post', 0, 100);
    }
}
