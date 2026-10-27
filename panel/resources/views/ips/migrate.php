<?php
/**
 * @var \WhaCP\View\View $this
 * @var list<array<string, mixed>> $pool
 * @var array<string, mixed>|null $plan
 * @var array<string, string> $errors
 * @var array<string, mixed> $q
 * @var list<array<string, mixed>>|null $results
 */
use WhaCP\Security\Permissions as P;

$canManage = $this->can(P::SERVER_MANAGE);
$opt = static fn (string $sel): string => implode('', array_map(static fn (array $a): string => '<option value="' . e((string) $a['address']) . '"' . ($sel === $a['address'] ? ' selected' : '') . '>' . e((string) $a['address']) . ' (' . count($a['sites']) . ' websites' . ((int) $a['reserved'] ? ', reserved' : '') . ')</option>', $pool));
?>
<?= $this->partial('ips/_tabs', get_defined_vars()) ?>

<?php if (is_array($results) && $results !== []): ?>
<div class="card mb-3"><div class="card-header">Results</div><ul class="list-group list-group-flush small">
    <?php foreach ($results as $r): ?><li class="list-group-item d-flex gap-2"><span class="wcp-state <?= $r['ok'] ? 'is-ok' : 'is-error' ?>"><?= $r['ok'] ? 'Done' : 'Failed' ?></span><span class="fw-semibold"><?= e((string) $r['item']) ?></span><span class="wcp-muted"><?= e((string) $r['message']) ?></span></li><?php endforeach; ?>
</ul></div>
<?php endif; ?>

<form class="card mb-3" method="get" action="<?= e($this->url('ips.migrate')) ?>">
    <div class="card-header"><span class="badge text-bg-light border me-1">1</span>Choose the addresses</div>
    <div class="card-body row g-2 align-items-end">
        <div class="col-md-4"><label class="form-label small" for="mg-s">Move from</label><select class="form-select form-select-sm<?= isset($errors['source']) ? ' is-invalid' : '' ?>" id="mg-s" name="source"><option value="">Choose…</option><?= $opt((string) $q['source']) ?></select><?php if (isset($errors['source'])): ?><div class="invalid-feedback"><?= e($errors['source']) ?></div><?php endif; ?></div>
        <div class="col-md-4"><label class="form-label small" for="mg-d">Move to</label><select class="form-select form-select-sm<?= isset($errors['dest']) ? ' is-invalid' : '' ?>" id="mg-d" name="dest"><option value="">Choose…</option><?= $opt((string) $q['dest']) ?></select><?php if (isset($errors['dest'])): ?><div class="invalid-feedback"><?= e($errors['dest']) ?></div><?php endif; ?></div>
        <div class="col-md-4"><div class="form-check"><input class="form-check-input" type="checkbox" name="all_zones" value="1" id="mg-z"<?= $q['all_zones'] ? ' checked' : '' ?>><label class="form-check-label small" for="mg-z">Also every DNS record that points to it (all zones, and the default IPv4 for new zones)</label></div></div>
        <div class="col-12"><button class="btn btn-sm btn-outline-primary">Show the changes</button></div>
    </div>
</form>

<?php if ($plan !== null): ?>
<div class="card">
    <div class="card-header"><span class="badge text-bg-light border me-1">2</span>Changes: <span class="font-monospace"><?= e((string) $plan['source']) ?></span> → <span class="font-monospace"><?= e((string) $plan['dest']) ?></span></div>
    <div class="card-body small">
        <p class="fw-semibold mb-1">Websites (<?= count($plan['sites']) ?>)</p>
        <?php if ($plan['sites'] === []): ?><p class="wcp-muted">No website is bound to <?= e((string) $plan['source']) ?>.</p><?php else: ?>
            <ul><?php foreach ($plan['sites'] as $s): ?><li><?= e((string) $s['domain']) ?><?= $s['owner'] ? ' <span class="wcp-muted">(' . e((string) $s['owner']) . ')</span>' : '' ?> — Nginx listens on the new address; its own <?= $plan['family'] === 4 ? 'A' : 'AAAA' ?> records follow</li><?php endforeach; ?></ul>
        <?php endif; ?>
        <?php if ($plan['all_zones']): ?>
            <p class="fw-semibold mb-1">DNS zones (<?= count($plan['zones']) ?>)</p>
            <?php if ($plan['zones'] === []): ?><p class="wcp-muted">No record points to it.</p><?php else: ?><ul><?php foreach ($plan['zones'] as $z): ?><li><?= e($z['name']) ?>: <?= (int) $z['records'] ?> record(s)</li><?php endforeach; ?></ul><?php endif; ?>
            <?php if ($plan['default_ipv4']): ?><p>The default IPv4 for new zones changes to <?= e((string) $plan['dest']) ?>.</p><?php endif; ?>
        <?php endif; ?>
        <p class="wcp-muted mb-0">Each website is moved on its own: its configuration is checked with <code>nginx -t</code> before the reload, and if its DNS update fails it goes back to the old address. Results are listed per website.</p>
    </div>
    <?php if ($canManage): ?>
    <form class="card-footer d-flex flex-wrap align-items-center gap-3" method="post" action="<?= e($this->url('ips.migrate.run')) ?>" data-wcp-busy>
        <?= $this->csrfField() ?><input type="hidden" name="source" value="<?= e((string) $plan['source']) ?>"><input type="hidden" name="dest" value="<?= e((string) $plan['dest']) ?>"><?php if ($plan['all_zones']): ?><input type="hidden" name="all_zones" value="1"><?php endif; ?>
        <div class="form-check mb-0"><input class="form-check-input" type="checkbox" name="confirm" value="1" id="mg-c" required><label class="form-check-label small" for="mg-c"><span class="badge text-bg-light border me-1">3</span>I checked the changes above</label></div>
        <button class="btn btn-primary btn-sm ms-auto"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Migrate</button>
    </form>
    <?php endif; ?>
</div>
<?php endif; ?>
