<?php

declare(strict_types=1);

namespace NimVor\Services;

/**
 * What changed, read from CHANGELOG.md (installed beside the panel's VERSION file): the newest releases with their
 * groups and lines, for the version popover in the top bar. Only the text is used; the caller escapes it.
 */
final class ChangelogService
{
    public function __construct(private readonly string $file)
    {
    }

    /**
     * @return list<array{version: string, date: string, intro: string, groups: list<array{title: string, items: list<string>}>}>
     */
    public function releases(int $limit = 3): array
    {
        $text = is_file($this->file) ? (string) file_get_contents($this->file) : '';
        if ($text === '') {
            return [];
        }
        $out = [];
        $cur = null;
        $group = null;
        foreach (preg_split('/\R/', $text) ?: [] as $line) {
            if (preg_match('/^##\s+\[([^\]]+)\](?:\s+(?:—|-)\s+(\S+))?/', $line, $m)) {
                if ($cur !== null && ($cur['groups'] !== [] || $cur['intro'] !== '')) {
                    $out[] = $cur;
                }
                if (count($out) >= $limit) {
                    $cur = null;
                    break;
                }
                // The unreleased section is not a version anyone runs.
                $cur = strcasecmp($m[1], 'Unreleased') === 0 ? null : ['version' => $m[1], 'date' => $m[2] ?? '', 'intro' => '', 'groups' => []];
                $group = null;
                continue;
            }
            if ($cur === null) {
                continue;
            }
            if (preg_match('/^###\s+(.+)$/', $line, $m)) {
                $cur['groups'][] = ['title' => trim($m[1]), 'items' => []];
                $group = count($cur['groups']) - 1;
            } elseif (preg_match('/^[-*]\s+(.+)$/', $line, $m) && $group !== null) {
                $cur['groups'][$group]['items'][] = trim($m[1]);
            } elseif (preg_match('/^\s{2,}\S/', $line) && $group !== null && $cur['groups'][$group]['items'] !== []) {
                $cur['groups'][$group]['items'][count($cur['groups'][$group]['items']) - 1] .= ' ' . trim($line);
            } elseif ($group === null && trim($line) !== '') {
                $cur['intro'] .= ($cur['intro'] === '' ? '' : ' ') . trim($line);
            }
        }
        if ($cur !== null && ($cur['groups'] !== [] || $cur['intro'] !== '')) {
            $out[] = $cur;
        }
        return array_slice($out, 0, $limit);
    }
}
