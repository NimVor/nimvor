<?php

declare(strict_types=1);

namespace NimVorSite\Support;

/**
 * A small, strict Markdown renderer for the project's own documentation (the
 * subset GitHub's pages use there: ATX headings, paragraphs, nested lists and
 * task lists, fenced code, blockquotes, tables, rules, links, code spans,
 * emphasis). The site has no third-party code, so this stands alone.
 *
 * Safety: every character of the source is escaped; the only tags in the
 * output are the ones written here. Raw HTML in the source is shown as text.
 * A link is only kept when the resolver returns an http(s), mailto, relative
 * or anchor target; images become links (the Content-Security-Policy blocks
 * remote images, and the docs have none).
 */
final class Markdown
{
    /** Bump when the output changes, so stored pages are rendered again. */
    public const VERSION = 1;

    /** @var array<string, int> */
    private array $ids = [];
    /** @var list<array{level: int, id: string, text: string}> */
    private array $toc = [];
    private string $title = '';
    /** @var list<string> */
    private array $stash = [];

    /** @param (\Closure(string): string)|null $resolve maps a link target to the one to use ('' drops the link) */
    public function __construct(private readonly ?\Closure $resolve = null)
    {
    }

    /** @return array{html: string, toc: list<array{level: int, id: string, text: string}>, title: string} */
    public function render(string $markdown): array
    {
        $this->ids = [];
        $this->toc = [];
        $this->title = '';
        $lines = explode("\n", str_replace(["\r\n", "\r", "\t"], ["\n", "\n", '    '], $markdown));
        $html = $this->blocks($lines);
        return ['html' => $html, 'toc' => $this->toc, 'title' => $this->title];
    }

    public static function slugify(string $text): string
    {
        $s = mb_strtolower(trim($text), 'UTF-8');
        $s = preg_replace('/[^\p{L}\p{N}\s_-]/u', '', $s) ?? $s;
        return preg_replace('/\s/u', '-', $s) ?? $s;
    }

    /**
     * @param list<string> $lines
     */
    private function blocks(array $lines, bool $tight = false): string
    {
        $out = '';
        $i = 0;
        $n = count($lines);
        while ($i < $n) {
            $line = $lines[$i];
            if (trim($line) === '') {
                $i++;
                continue;
            }
            if (preg_match('/^( {0,3})(`{3,}|~{3,})[ ]*([^`\s]*)[^`]*$/', $line, $m) === 1) {
                [$block, $i] = $this->fenced($lines, $i, $m);
                $out .= $block;
                continue;
            }
            if (preg_match('/^ {0,3}(#{1,6})[ ]+(.*?)(?:[ ]+#+)?[ ]*$/', $line, $m) === 1 || preg_match('/^ {0,3}(#{1,6})()[ ]*$/', $line, $m) === 1) {
                $out .= $this->heading(strlen($m[1]), $m[2]);
                $i++;
                continue;
            }
            if (preg_match('/^ {0,3}([-*_])(?:[ ]*\1){2,}[ ]*$/', $line) === 1) {
                $out .= "<hr>\n";
                $i++;
                continue;
            }
            if (preg_match('/^ {0,3}>/', $line) === 1) {
                $quote = [];
                while ($i < $n && preg_match('/^ {0,3}>[ ]?(.*)$/', $lines[$i], $q) === 1) {
                    $quote[] = $q[1];
                    $i++;
                }
                $out .= "<blockquote>\n" . $this->blocks($quote) . "</blockquote>\n";
                continue;
            }
            if ($this->startsTable($lines, $i)) {
                [$block, $i] = $this->table($lines, $i);
                $out .= $block;
                continue;
            }
            if (preg_match('/^( {0,3})([-*+]|\d{1,9}[.)])(?:[ ]+|$)/', $line) === 1) {
                [$block, $i] = $this->list($lines, $i);
                $out .= $block;
                continue;
            }
            $para = [];
            while ($i < $n && trim($lines[$i]) !== '' && ($para === [] || !$this->interrupts($lines, $i))) {
                $para[] = ltrim($lines[$i]);
                $i++;
            }
            $text = $this->inline(implode("\n", $para));
            $out .= $tight ? $text . "\n" : '<p>' . $text . "</p>\n";
        }
        return $out;
    }

    /** @param list<string> $lines */
    private function interrupts(array $lines, int $i): bool
    {
        $l = $lines[$i];
        return preg_match('/^ {0,3}(`{3,}|~{3,})/', $l) === 1
            || preg_match('/^ {0,3}#{1,6}([ ]|$)/', $l) === 1
            || preg_match('/^ {0,3}([-*_])(?:[ ]*\1){2,}[ ]*$/', $l) === 1
            || preg_match('/^ {0,3}>/', $l) === 1
            || preg_match('/^ {0,3}([-*+]|1[.)])[ ]+\S/', $l) === 1
            || $this->startsTable($lines, $i);
    }

    /**
     * @param list<string> $lines
     * @param list<string> $m
     * @return array{string, int}
     */
    private function fenced(array $lines, int $i, array $m): array
    {
        $fence = $m[2];
        $indent = strlen($m[1]);
        $lang = preg_replace('/[^a-zA-Z0-9_+-]/', '', $m[3]) ?? '';
        $close = '/^ {0,3}' . preg_quote($fence[0], '/') . '{' . strlen($fence) . ',}[ ]*$/';
        $n = count($lines);
        $code = [];
        $i++;
        while ($i < $n && preg_match($close, $lines[$i]) !== 1) {
            $code[] = $indent > 0 ? (preg_replace('/^ {0,' . $indent . '}/', '', $lines[$i]) ?? $lines[$i]) : $lines[$i];
            $i++;
        }
        $cls = $lang !== '' ? ' class="language-' . e($lang) . '"' : '';
        return ['<pre class="codeblock"><code' . $cls . '>' . e(implode("\n", $code)) . "</code></pre>\n", $i + 1];
    }

    private function heading(int $level, string $raw): string
    {
        $plain = $this->plain($raw);
        $base = self::slugify($plain);
        $base = $base === '' ? 'section' : $base;
        $id = $base;
        if (isset($this->ids[$base])) {
            $id = $base . '-' . $this->ids[$base];
            $this->ids[$base]++;
        } else {
            $this->ids[$base] = 1;
        }
        if ($level === 1 && $this->title === '') {
            $this->title = $plain;
        }
        if ($level >= 2 && $level <= 3) {
            $this->toc[] = ['level' => $level, 'id' => $id, 'text' => $plain];
        }
        return '<h' . $level . ' id="' . e($id) . '">' . $this->inline($raw) . ($level >= 2 ? ' <a class="anchor" href="#' . e($id) . '" aria-label="Link to this section">#</a>' : '') . '</h' . $level . ">\n";
    }

    /** The heading's text without markup, for its id, the table of contents and the title. */
    private function plain(string $raw): string
    {
        $s = preg_replace('/!?\[([^\]]*)\]\([^)]*\)/', '$1', $raw) ?? $raw;
        $s = preg_replace('/[`*_~]/', '', $s) ?? $s;
        return trim($s);
    }

    /** @param list<string> $lines */
    private function startsTable(array $lines, int $i): bool
    {
        if (!isset($lines[$i + 1]) || !str_contains($lines[$i], '|') || !str_contains($lines[$i + 1], '|')) {
            return false;
        }
        $sep = $this->cells($lines[$i + 1]);
        if (count($sep) !== count($this->cells($lines[$i]))) {
            return false;
        }
        foreach ($sep as $c) {
            if (preg_match('/^:?-+:?$/', $c) !== 1) {
                return false;
            }
        }
        return true;
    }

    /** @return list<string> */
    private function cells(string $line): array
    {
        $line = trim($line);
        $line = preg_replace('/^\|/', '', $line) ?? $line;
        $line = preg_replace('/(?<!\\\\)\|$/', '', $line) ?? $line;
        $cells = preg_split('/(?<!\\\\)\|/', $line) ?: [];
        return array_map(static fn (string $c): string => trim(str_replace('\\|', '|', $c)), $cells);
    }

    /**
     * @param list<string> $lines
     * @return array{string, int}
     */
    private function table(array $lines, int $i): array
    {
        $head = $this->cells($lines[$i]);
        $align = array_map(static function (string $c): string {
            $l = str_starts_with($c, ':');
            $r = str_ends_with($c, ':');
            return $l && $r ? ' class="al-c"' : ($r ? ' class="al-r"' : '');
        }, $this->cells($lines[$i + 1]));
        $i += 2;
        $n = count($lines);
        $html = '<div class="table-wrap"><table><thead><tr>';
        foreach ($head as $k => $c) {
            $html .= '<th' . ($align[$k] ?? '') . '>' . $this->inline($c) . '</th>';
        }
        $html .= '</tr></thead><tbody>';
        while ($i < $n && trim($lines[$i]) !== '' && str_contains($lines[$i], '|')) {
            $cells = $this->cells($lines[$i]);
            $html .= '<tr>';
            foreach ($head as $k => $_) {
                $html .= '<td' . ($align[$k] ?? '') . '>' . $this->inline($cells[$k] ?? '') . '</td>';
            }
            $html .= '</tr>';
            $i++;
        }
        return [$html . "</tbody></table></div>\n", $i];
    }

    /**
     * @param list<string> $lines
     * @return array{string, int}
     */
    private function list(array $lines, int $i): array
    {
        $n = count($lines);
        preg_match('/^( {0,3})([-*+]|\d{1,9}[.)])/', $lines[$i], $first);
        $ordered = ctype_digit($first[2][0]);
        $kind = $ordered ? substr($first[2], -1) : $first[2];
        $start = $ordered ? (int) $first[2] : 1;
        $items = [];
        $loose = false;
        while ($i < $n) {
            if (preg_match('/^( {0,3})([-*+]|\d{1,9}[.)])( +|$)(.*)$/', $lines[$i], $m) !== 1) {
                break;
            }
            if (ctype_digit($m[2][0]) !== $ordered || ($ordered ? substr($m[2], -1) : $m[2]) !== $kind) {
                break;
            }
            $spaces = strlen($m[3]);
            if ($m[4] === '' || $spaces > 4) {
                $spaces = 1;
                $m[4] = $m[4] === '' ? '' : str_repeat(' ', strlen($m[3]) - 1) . $m[4];
            }
            $offset = strlen($m[1]) + strlen($m[2]) + $spaces;
            $item = [$m[4]];
            $j = $i + 1;
            while ($j < $n) {
                $l = $lines[$j];
                if (trim($l) === '') {
                    $k = $j;
                    while ($k < $n && trim($lines[$k]) === '') {
                        $k++;
                    }
                    if ($k < $n && $this->indent($lines[$k]) >= $offset) {
                        for (; $j < $k; $j++) {
                            $item[] = '';
                        }
                        continue;
                    }
                    break;
                }
                if ($this->indent($l) >= $offset) {
                    $item[] = substr($l, $offset);
                    $j++;
                    continue;
                }
                $prev = end($item);
                if ($prev !== '' && $prev !== false && $this->indent($l) < 4 && !$this->interrupts($lines, $j) && preg_match('/^ {0,3}([-*+]|\d{1,9}[.)])[ ]/', $l) !== 1) {
                    $item[] = ltrim($l);
                    $j++;
                    continue;
                }
                break;
            }
            // Blank lines inside an item make the list loose.
            while ($item !== [] && end($item) === '') {
                array_pop($item);
            }
            if (in_array('', $item, true)) {
                $loose = true;
            }
            $items[] = $item;
            $i = $j;
            $k = $i;
            while ($k < $n && trim($lines[$k]) === '') {
                $k++;
            }
            if ($k > $i && $k < $n && preg_match('/^( {0,3})([-*+]|\d{1,9}[.)])( +|$)/', $lines[$k], $nx) === 1
                && ctype_digit($nx[2][0]) === $ordered && ($ordered ? substr($nx[2], -1) : $nx[2]) === $kind) {
                $loose = true;
                $i = $k;
            }
        }
        $tag = $ordered ? 'ol' : 'ul';
        $html = '<' . $tag . ($ordered && $start !== 1 ? ' start="' . $start . '"' : '') . ">\n";
        foreach ($items as $item) {
            $task = '';
            if (preg_match('/^\[([ xX])\][ ]+/', $item[0], $t) === 1) {
                $task = '<input type="checkbox" disabled' . ($t[1] === ' ' ? '' : ' checked') . '> ';
                $item[0] = substr($item[0], strlen($t[0]));
            }
            $html .= '<li>' . $task . trim($this->blocks($item, !$loose)) . "</li>\n";
        }
        return [$html . '</' . $tag . ">\n", $i];
    }

    private function indent(string $line): int
    {
        return strlen($line) - strlen(ltrim($line, ' '));
    }

    /** Turns inline Markdown into HTML. */
    private function inline(string $s): string
    {
        $this->stash = [];
        $s = $this->protect($s);
        $s = e($s);
        $s = preg_replace('/~~(?=\S)(.+?)(?<=\S)~~/s', '<del>$1</del>', $s) ?? $s;
        $s = preg_replace('/\*\*(?=\S)(.+?)(?<=\S)\*\*/s', '<strong>$1</strong>', $s) ?? $s;
        $s = preg_replace('/(?<![\w])__(?=\S)(.+?)(?<=\S)__(?![\w])/s', '<strong>$1</strong>', $s) ?? $s;
        $s = preg_replace('/(?<![*\w])\*(?=[^\s*])(.+?)(?<=[^\s*])\*(?![*\w])/s', '<em>$1</em>', $s) ?? $s;
        $s = preg_replace('/(?<![\w])_(?=[^\s_])(.+?)(?<=[^\s_])_(?![\w])/s', '<em>$1</em>', $s) ?? $s;
        $s = preg_replace('/(?: {2,}|\\\\)\n/', "<br>\n", $s) ?? $s;
        for ($guard = 0; $guard < 6 && str_contains($s, "\x00"); $guard++) {
            $s = preg_replace_callback('/\x00(\d+)\x01/', fn (array $m): string => $this->stash[(int) $m[1]] ?? '', $s) ?? $s;
        }
        return trim($s);
    }

    private function hold(string $html): string
    {
        $this->stash[] = $html;
        return "\x00" . (count($this->stash) - 1) . "\x01";
    }

    /** Takes code spans, escapes, links and URLs out of the text (their HTML is put back last). */
    private function protect(string $s): string
    {
        $s = preg_replace_callback('/(?<!\\\\)(`+)(?!`)(.+?)(?<!`)\1(?!`)/s', function (array $m): string {
            $c = $m[2];
            if (strlen($c) > 2 && $c[0] === ' ' && substr($c, -1) === ' ') {
                $c = substr($c, 1, -1);
            }
            return $this->hold('<code>' . e(str_replace("\n", ' ', $c)) . '</code>');
        }, $s) ?? $s;
        $s = preg_replace_callback('/\\\\([\\\\`*_{}\[\]()#+\-.!|~<>])/', fn (array $m): string => $this->hold(e($m[1])), $s) ?? $s;
        $s = preg_replace_callback('/<((?:https?:\/\/|mailto:)[^\s<>]+)>/', fn (array $m): string => $this->link($m[1], e($m[1])), $s) ?? $s;
        $s = preg_replace_callback('/(!?)\[((?:[^\[\]]|\[[^\[\]]*\])*)\]\((<[^>]*>|(?:[^\s()]|\([^\s()]*\))*)(?:\s+"[^"]*")?\)/', function (array $m): string {
            $url = trim($m[3], '<>');
            $text = $m[2] === '' ? $url : $this->inlineNested($m[2]);
            return $this->link($url, $m[1] === '!' ? '[' . $text . ']' : $text);
        }, $s) ?? $s;
        return preg_replace_callback('/(?<![\w"=\/@.])https?:\/\/[^\s<>]*[^\s<>.,;:!?)\]\'"*]/', fn (array $m): string => $this->link($m[0], e($m[0])), $s) ?? $s;
    }

    /** Link text may hold code, emphasis and escapes: render it on its own, keeping the outer stash. */
    private function inlineNested(string $text): string
    {
        $saved = $this->stash;
        $html = $this->inline($text);
        $this->stash = $saved;
        return $html;
    }

    private function link(string $target, string $labelHtml): string
    {
        $href = $this->resolve === null ? $target : ($this->resolve)($target);
        if (!self::safeHref($href)) {
            return $this->hold($labelHtml);
        }
        $external = preg_match('#^https?://#i', $href) === 1;
        return $this->hold('<a href="' . e($href) . '"' . ($external ? ' rel="noopener nofollow" class="ext"' : '') . '>' . $labelHtml . '</a>');
    }

    public static function safeHref(string $href): bool
    {
        if ($href === '' || preg_match('/[\x00-\x1f\s]/', $href) === 1) {
            return false;
        }
        if (preg_match('#^[a-z][a-z0-9+.-]*:#i', $href) === 1) {
            return preg_match('#^(https?|mailto):#i', $href) === 1;
        }
        return !str_starts_with($href, '//');
    }
}
