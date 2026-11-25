<?php
/**
 * The change log card (the hover card on the version number): a search box, a filter by kind of change and a timeline
 * of releases, newest first. Search and filter work in the browser (public/assets/js/nimvor.js, data-cl-*).
 *
 * @var \NimVor\View\View $this
 * @var list<array{version: string, date: string, intro: string, groups: list<array{title: string, items: list<string>}>}> $releases
 */
// Plain text with **bold** and `code`, escaped first.
$fmt = static fn (string $t): string => preg_replace(['/\*\*(.+?)\*\*/', '/`([^`]+)`/'], ['<strong>$1</strong>', '<code>$1</code>'], e($t)) ?? '';
$icons = ['Added' => 'plus-lg', 'Changed' => 'arrow-up', 'Fixed' => 'wrench', 'Removed' => 'trash', 'Security' => 'shield-check', 'Deprecated' => 'exclamation-triangle'];
$kinds = [];
foreach ($releases as $r) {
    foreach ($r['groups'] as $g) {
        if ($g['items'] !== []) {
            $kinds[$g['title']] = true;
        }
    }
}
?>
<div class="nim-cl">
    <div class="nim-cl-head">Change Logs</div>
    <?php if ($releases === []): ?>
        <div class="small text-body-secondary">The change log is not installed on this server. It is published at <a href="https://github.com/NimVor/nimvor/blob/main/CHANGELOG.md" target="_blank" rel="noopener noreferrer">github.com/NimVor/nimvor</a>.</div>
    <?php else: ?>
    <div class="small text-body-secondary mb-2">What was added, changed and fixed in NimVor.</div>
    <div class="nim-cl-tools">
        <div class="nim-cl-search"><i class="bi bi-search" aria-hidden="true"></i><input type="search" class="form-control form-control-sm" placeholder="Search updates…" aria-label="Search updates" data-cl-search></div>
        <select class="form-select form-select-sm" aria-label="Kind of change" data-cl-filter>
            <option value="">All</option>
            <?php foreach (array_keys($kinds) as $k): ?><option value="<?= e((string) $k) ?>"><?= e((string) $k) ?></option><?php endforeach; ?>
        </select>
    </div>
    <ol class="nim-cl-timeline">
        <?php foreach ($releases as $i => $r): ?>
            <li class="nim-cl-release" data-cl-release>
                <span class="nim-cl-dot" aria-hidden="true"></span>
                <div class="nim-cl-card">
                    <div class="nim-cl-version">v<?= e($r['version']) ?><?php if ($i === 0): ?> <span class="badge nim-cl-new">New Version</span><?php endif; ?></div>
                    <?php if ($r['date'] !== ''): ?><div class="small text-body-secondary"><i class="bi bi-calendar3 me-1" aria-hidden="true"></i><?= e($r['date']) ?></div><?php endif; ?>
                    <?php if ($r['intro'] !== ''): ?><div class="small mt-2" data-cl-text><?= $fmt($r['intro']) ?></div><?php endif; ?>
                    <?php foreach ($r['groups'] as $g): if ($g['items'] === []) { continue; } ?>
                        <div class="nim-cl-group" data-cl-group="<?= e($g['title']) ?>">
                            <div class="nim-cl-group-title"><span class="nim-cl-icon"><i class="bi bi-<?= e($icons[$g['title']] ?? 'dot') ?>" aria-hidden="true"></i></span><?= e($g['title']) ?></div>
                            <ul class="nim-cl-items"><?php foreach ($g['items'] as $it): ?><li data-cl-text><?= $fmt($it) ?></li><?php endforeach; ?></ul>
                        </div>
                    <?php endforeach; ?>
                </div>
            </li>
        <?php endforeach; ?>
    </ol>
    <div class="nim-cl-none d-none small text-body-secondary" data-cl-none>Nothing matches.</div>
    <?php endif; ?>
</div>
