<?php

/** @var array<string, mixed> $r */
/** @var int $id */
$cls = static fn (string $s): string => ['done' => 'mr-ok', 'pass' => 'mr-ok', 'failed' => 'mr-bad', 'fail' => 'mr-bad', 'warn' => 'mr-warn', 'warning' => 'mr-warn', 'critical' => 'mr-bad'][$s] ?? '';
$src = (array) ($r['source'] ?? []);
$plan = (array) ($r['plan'] ?? []);
?><!doctype html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="robots" content="noindex">
    <title>Migration report <?= (int) $id ?></title>
    <link rel="stylesheet" href="<?= e($this->asset('assets/css/migration-report.css')) ?>">
</head>
<body>
<div class="mr-wrap">
    <p class="mr-noprint"><button type="button" data-nim-print>Print or save as PDF</button> · <a href="/migrations/<?= (int) $id ?>">Back to the migration</a></p>
    <h1>Migration report #<?= (int) $id ?></h1>
    <p>Status: <strong><?= e((string) $r['status']) ?></strong> · from <strong><?= e((string) ($src['user'] ?? '')) ?>@<?= e((string) ($src['host'] ?? '')) ?></strong> (<?= e((string) ($src['panel'] ?? '')) ?>) · started <?= e((string) ($r['created_at'] ?? '')) ?><?= !empty($r['finished_at']) ? ' · finished ' . e((string) $r['finished_at']) : '' ?> · generated <?= e((string) $r['generated_at']) ?></p>

    <h2>What was planned</h2>
    <ul>
        <li><?= count((array) ($plan['files'] ?? [])) ?> website(s), <?= count((array) ($plan['databases'] ?? [])) ?> database(s), <?= count((array) ($plan['mail'] ?? [])) ?> mailbox(es)</li>
        <?php foreach ((array) ($plan['files'] ?? []) as $f): ?><li>Website <?= e((string) $f['domain']) ?>: <?= e((string) $f['source_root']) ?> → <?= e((string) $f['dest_root']) ?></li><?php endforeach; ?>
        <?php foreach ((array) ($plan['databases'] ?? []) as $d): ?><li>Database <?= e((string) $d['source']) ?> → <?= e((string) $d['dest']) ?></li><?php endforeach; ?>
        <?php foreach ((array) ($plan['mail'] ?? []) as $x): ?><li>Mailbox <?= e((string) $x['address']) ?></li><?php endforeach; ?>
    </ul>

    <h2>Results</h2>
    <table><thead><tr><th>Task</th><th>Status</th><th>Files</th><th>Differences</th><th>Detail</th></tr></thead><tbody>
    <?php foreach ((array) ($r['tasks'] ?? []) as $t): ?>
        <tr><td><?= e((string) (($t['label'] ?? '') !== '' ? $t['label'] : ($t['id'] ?? ''))) ?></td><td class="<?= e($cls((string) ($t['status'] ?? ''))) ?>"><?= e((string) ($t['status'] ?? '')) ?></td><td><?= (int) ($t['files'] ?? 0) ?></td><td><?= (int) ($t['differences'] ?? 0) ?></td><td><?= e((string) (($t['error'] ?? '') !== '' ? $t['error'] : ($t['detail'] ?? ''))) ?></td></tr>
    <?php endforeach; ?>
    </tbody></table>

    <?php if (!empty($r['assessment'])): ?>
    <h2>Compatibility check</h2>
    <table><tbody>
    <?php foreach ((array) $r['assessment'] as $f): ?>
        <tr><td class="<?= e($cls((string) ($f['severity'] ?? ''))) ?>"><?= e((string) ($f['severity'] ?? '')) ?></td><td><?= e((string) ($f['message'] ?? '')) ?> <?= !empty($f['fix']) ? '— ' . e((string) $f['fix']) : '' ?></td></tr>
    <?php endforeach; ?>
    </tbody></table>
    <?php endif; ?>

    <?php if (!empty($r['validation'])): $v = (array) $r['validation']; ?>
    <h2>Validation (<?= e((string) ($v['at'] ?? '')) ?>)</h2>
    <p><span class="mr-ok"><?= (int) ($v['passed'] ?? 0) ?> passed</span> · <span class="mr-warn"><?= (int) ($v['warned'] ?? 0) ?> to look at</span> · <span class="mr-bad"><?= (int) ($v['failed'] ?? 0) ?> failed</span></p>
    <table><tbody>
    <?php foreach ((array) ($v['checks'] ?? []) as $c): ?>
        <tr><td><?= e((string) $c['item']) ?></td><td><?= e((string) $c['name']) ?></td><td class="<?= e($cls((string) $c['status'])) ?>"><?= e((string) $c['status']) ?></td><td><?= e((string) $c['detail']) ?></td></tr>
    <?php endforeach; ?>
    </tbody></table>
    <p><strong>Not checked:</strong> <?= e(implode('; ', array_map('strval', (array) ($v['limits'] ?? [])))) ?></p>
    <?php endif; ?>

    <?php if (!empty($r['rollback'])): ?><h2>Rollback</h2><pre><?= e((string) json_encode($r['rollback'], JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES)) ?></pre><?php endif; ?>

    <h2>Not moved by this version</h2>
    <ul><?php foreach ((array) ($r['not_migrated'] ?? []) as $n): ?><li><?= e((string) $n) ?></li><?php endforeach; ?></ul>

    <h2>Recommendations</h2>
    <ul><?php foreach ((array) ($r['recommendations'] ?? []) as $n): ?><li><?= e((string) $n) ?></li><?php endforeach; ?></ul>
    <p><?= e((string) ($r['validation_note'] ?? '')) ?></p>
</div>
<script src="<?= e($this->asset('assets/js/migrations.js')) ?>" defer></script>
</body>
</html>
