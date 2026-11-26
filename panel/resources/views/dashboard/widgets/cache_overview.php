<?php
/**
 * Redis and Memcached.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 * @var string $only all (the default), redis or memcached
 */
$only ??= 'all';
$n = static fn (?int $v): string => $v === null ? 'N/A' : number_format($v);
$pct = static fn (?float $v): string => $v === null ? 'N/A' : number_format($v, 1) . '%';
$mem = static fn (?int $used, ?int $max): string => $used === null ? 'N/A' : format_bytes($used) . ($max !== null ? ' / ' . format_bytes($max) : ' (no limit)');
?>
<?php foreach (['redis' => 'Redis', 'memcached' => 'Memcached'] as $key => $label): if ($d[$key] === null || ($only !== 'all' && $only !== $key)) { continue; } $c = $d[$key]; ?>
    <div class="mb-3">
        <div class="fw-semibold mb-1"><?= e($label) ?>
            <?php if ($key === 'redis' && $c['version'] !== ''): ?><span class="fw-normal text-body-secondary"><?= e($c['version']) ?></span><?php endif; ?>
            <?php if ($key === 'memcached' && (!$c['active'] || !$c['answering'])): ?><span class="fw-normal text-danger"><?= $c['active'] ? 'not answering' : 'not running' ?></span><?php endif; ?>
        </div>
        <?php if ($key === 'memcached' && !$c['answering']): ?>
            <div class="text-body-secondary">No statistics: Memcached did not answer.</div>
        <?php else: ?>
            <table class="table table-sm mb-0"><tbody>
                <tr><td>Memory</td><td class="text-end"><?= e($mem($c['used'], $c['max'])) ?></td></tr>
                <tr><td>Hit ratio</td><td class="text-end"><?= e($pct($c['hit'])) ?></td></tr>
                <tr><td>Clients</td><td class="text-end"><?= e($n($c['clients'])) ?></td></tr>
                <tr><td>Evicted since start</td><td class="text-end <?= ($c['evicted'] ?? 0) > 0 ? 'text-warning' : '' ?>"><?= e($n($c['evicted'])) ?></td></tr>
                <tr><td><?= $key === 'redis' ? 'Keys' : 'Items' ?></td><td class="text-end"><?= e($n($key === 'redis' ? $c['keys'] : $c['items'])) ?></td></tr>
            </tbody></table>
        <?php endif; ?>
    </div>
<?php endforeach; ?>
