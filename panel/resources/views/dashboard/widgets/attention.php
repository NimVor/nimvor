<?php
/**
 * Needs attention: websites with something to fix, certificates close to expiry.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$needs = $d['needs'] ?? null;
$expiring = (int) ($d['expiring'] ?? 0);
$attention = $needs !== null ? array_sum($needs['counts']) : 0;
?>
    <?php if ($attention === 0 && $expiring === 0): ?>
        <div class="text-success"><i class="bi bi-check-circle-fill me-1" aria-hidden="true"></i>Nothing needs attention.</div>
    <?php else: ?>
        <div class="d-flex flex-wrap gap-2">
            <?php foreach (\NimVor\Services\SitesHealthService::CATEGORIES as $key => [$label]): $n = (int) ($needs['counts'][$key] ?? 0); if ($n === 0) { continue; } ?>
                <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.healthcheck')) ?>?category=<?= e($key) ?>"><?= e($label) ?> <span class="badge text-bg-warning"><?= $n ?></span></a>
            <?php endforeach; ?>
            <?php if ($expiring > 0): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('ssl')) ?>">Certificates expiring <span class="badge text-bg-warning"><?= $expiring ?></span></a><?php endif; ?>
        </div>
        <?php if ($needs !== null): ?><div class="small text-body-secondary mt-2"><?= (int) ($needs['total'] - $needs['healthy']) ?> of <?= (int) $needs['total'] ?> websites have something to look at.</div><?php endif; ?>
    <?php endif; ?>
