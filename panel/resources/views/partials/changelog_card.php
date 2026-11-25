<?php
/**
 * The change log card.
 *
 * @var \NimVor\View\View $this
 * @var list<array{version: string, date: string, intro: string, groups: list<array{title: string, items: list<string>}>}> $releases
 */
// Plain text with **bold** and `code`, escaped first.
$fmt = static fn (string $t): string => preg_replace(['/\*\*(.+?)\*\*/', '/`([^`]+)`/'], ['<strong>$1</strong>', '<code>$1</code>'], e($t)) ?? '';
?>
<div class="nim-pop-title">Change log</div>
<?php if ($releases === []): ?>
    <div class="small text-body-secondary">The change log is not installed on this server. It is published at <a href="https://github.com/NimVor/nimvor/blob/main/CHANGELOG.md" target="_blank" rel="noopener noreferrer">github.com/NimVor/nimvor</a>.</div>
<?php endif; ?>
<?php foreach ($releases as $r): ?>
    <div class="nim-pop-h"><?= e($r['version']) ?><?= $r['date'] !== '' ? ' · ' . e($r['date']) : '' ?></div>
    <?php if ($r['intro'] !== ''): ?><div class="small mb-1"><?= $fmt($r['intro']) ?></div><?php endif; ?>
    <?php foreach ($r['groups'] as $g): if ($g['items'] === []) { continue; } ?>
        <div class="small fw-semibold mt-1"><?= e($g['title']) ?></div>
        <ul class="nim-pop-list"><?php foreach (array_slice($g['items'], 0, 8) as $i): ?><li><?= $fmt($i) ?></li><?php endforeach; ?></ul>
    <?php endforeach; ?>
<?php endforeach; ?>
