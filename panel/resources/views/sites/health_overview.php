<?php
/**
 * Websites → Site health: every website with its problems, worst first.
 *
 * @var \NimVor\View\View $this
 * @var array{sites: list<array<string, mixed>>, counts: array<string, int>, total: int, healthy: int, mail_host: string} $result
 * @var string $filter
 * @var string $checkedAt
 */
use NimVor\Services\SitesHealthService;

$icon = ['bad' => 'bi-x-circle-fill text-danger', 'warn' => 'bi-exclamation-triangle-fill text-warning', 'info' => 'bi-info-circle text-body-secondary', 'ok' => 'bi-check-circle-fill text-success'];
$problems = array_sum($result['counts']);
?>
<?php $this->start(); ?>Site health<?php $this->end('title'); ?>
<?php $this->start(); ?>What needs attention across your websites<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
    <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.healthcheck')) ?>"><i class="bi bi-arrow-clockwise me-1" aria-hidden="true"></i>Check again</a>
<?php $this->end('actions'); ?>

<div class="nim-panel mb-3">
    <div class="nim-statusbar flex-wrap">
        <i class="bi <?= $problems === 0 ? 'bi-check-circle-fill text-success' : 'bi-exclamation-triangle-fill text-warning' ?> fs-4" aria-hidden="true"></i>
        <span class="fw-semibold"><?= $problems === 0 ? 'Every website looks healthy' : e((string) ($result['total'] - $result['healthy'])) . ' of ' . e((string) $result['total']) . ' websites need attention' ?></span>
        <span class="small nim-muted ms-auto">Checked <?= e($checkedAt) ?> UTC</span>
    </div>
    <div class="d-flex flex-wrap gap-2 px-3 pb-3">
        <a class="btn btn-sm <?= $filter === '' ? 'btn-primary' : 'btn-outline-secondary' ?>" href="<?= e($this->url('sites.healthcheck')) ?>">All</a>
        <?php foreach (SitesHealthService::CATEGORIES as $key => [$label]): $n = (int) ($result['counts'][$key] ?? 0); ?>
            <a class="btn btn-sm <?= $filter === $key ? 'btn-primary' : 'btn-outline-secondary' ?>" href="<?= e($this->url('sites.healthcheck')) ?>?category=<?= e($key) ?>"><?= e($label) ?> <span class="badge text-bg-<?= $n > 0 ? 'warning' : 'light' ?>"><?= $n ?></span></a>
        <?php endforeach; ?>
    </div>
</div>

<div class="nim-panel">
    <div class="table-responsive">
        <table class="table align-middle mb-0">
            <thead><tr><th>Website</th><th>Problems</th></tr></thead>
            <tbody>
            <?php foreach ($result['sites'] as $row): $s = $row['site']; ?>
                <tr>
                    <td class="text-nowrap">
                        <i class="bi <?= e($icon[$row['worst']] ?? $icon['ok']) ?> me-1" aria-hidden="true"></i>
                        <a href="<?= e($this->url('sites.show', ['id' => (int) $s['id']])) ?>"><?= e((string) $s['domain']) ?></a>
                    </td>
                    <td class="small">
                        <?php if ($row['issues'] === []): ?><span class="nim-muted">All good</span><?php endif; ?>
                        <?php foreach ($row['issues'] as $i): if ($filter !== '' && $i['category'] !== $filter) { continue; }
                            $route = SitesHealthService::CATEGORIES[$i['category']][1]; ?>
                            <div class="d-flex align-items-start gap-2 py-1">
                                <i class="bi <?= e($icon[$i['level']] ?? $icon['ok']) ?> mt-1" aria-hidden="true"></i>
                                <div class="flex-grow-1"><span class="fw-medium"><?= e($i['title']) ?></span> <span class="nim-muted"><?= e($i['detail']) ?></span></div>
                                <?php if ($i['category'] === 'email'): ?>
                                    <?php if (!empty($i['fixable']) && $this->routeAllowed('sites.healthcheck.fix_email') && !empty($row['mail_domain_id'])): ?>
                                        <form method="post" action="<?= e($this->url('sites.healthcheck.fix_email')) ?>" class="d-inline"><?= $this->csrfField() ?>
                                            <input type="hidden" name="domain_id" value="<?= (int) $row['mail_domain_id'] ?>">
                                            <button class="btn btn-sm btn-outline-primary text-nowrap">Add records</button>
                                        </form>
                                    <?php elseif ($this->routeAllowed('mail')): ?>
                                        <a class="btn btn-sm btn-outline-secondary text-nowrap" href="<?= e($this->url('mail')) ?>">Email page</a>
                                    <?php endif; ?>
                                <?php elseif ($this->hasRoute($route) && $this->routeAllowed($route)): ?>
                                    <a class="btn btn-sm btn-outline-primary text-nowrap" href="<?= e($this->url($route, ['id' => (int) $s['id']])) ?>">Open</a>
                                <?php endif; ?>
                            </div>
                        <?php endforeach; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            <?php if ($result['sites'] === []): ?><tr><td colspan="2" class="nim-muted p-3">No websites to show.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
</div>
