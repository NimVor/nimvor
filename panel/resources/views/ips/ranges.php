<?php
/**
 * @var \WhaCP\View\View $this
 * @var list<array<string, mixed>> $ranges
 * @var list<array<string, mixed>> $allocated
 * @var list<array<string, mixed>> $websites
 */
use WhaCP\Security\Permissions as P;

$canManage = $this->can(P::SERVER_MANAGE);
$errors ??= [];
$old ??= [];
?>
<?= $this->partial('ips/_tabs', get_defined_vars()) ?>
<?php if ($canManage): ?><?php $this->start(); ?><button type="button" class="btn btn-sm btn-primary" data-bs-toggle="modal" data-bs-target="#range-add"><i class="bi bi-plus-lg me-1"></i>Add an IPv6 range</button><?php $this->end('actions'); ?><?php endif; ?>

<div class="wcp-infobar">
    <i class="bi bi-diagram-2" aria-hidden="true"></i>
    <span>A range your provider <em>routes</em> to this server (not the on-link prefix of its interface) becomes usable by websites: NimVor adds a local route for it on the loopback interface and keeps it at boot (<code>whacp-ipv6-ranges.service</code>). Ranges that overlap an interface's prefix or contain the gateway are refused.</span>
</div>

<div class="row g-3">
<?php foreach ($ranges as $g): ?>
    <div class="col-xl-6">
        <div class="card h-100">
            <div class="card-header d-flex align-items-center gap-2"><span class="fw-semibold font-monospace"><?= e((string) $g['prefix']) ?></span><span class="small wcp-muted ms-auto"><?= (int) $g['allocated'] ?> allocated · <?= (int) $g['used'] ?> in use</span></div>
            <div class="card-body small">
                <?php $mine = array_filter($allocated, static fn (array $a): bool => (int) $a['range_id'] === (int) $g['id']); ?>
                <?php if ($mine === []): ?><p class="wcp-muted">No addresses allocated yet.</p><?php else: ?>
                    <ul class="list-unstyled mb-2"><?php foreach ($mine as $a): ?><li><span class="font-monospace"><?= e((string) $a['address']) ?></span> <span class="wcp-muted">— <?= $a['sites'] === [] ? 'free' : e(implode(', ', array_column($a['sites'], 'domain'))) ?></span></li><?php endforeach; ?></ul>
                <?php endif; ?>
                <?php if ($canManage): ?>
                <form class="row g-2 align-items-end mb-2" method="post" action="<?= e($this->url('ips.ranges.allocate', ['id' => (int) $g['id']])) ?>" data-wcp-busy><?= $this->csrfField() ?>
                    <div class="col-sm-5"><label class="form-label small" for="al-a-<?= (int) $g['id'] ?>">Address (empty: next free)</label><input class="form-control form-control-sm font-monospace" id="al-a-<?= (int) $g['id'] ?>" name="address"></div>
                    <div class="col-sm-4"><label class="form-label small" for="al-s-<?= (int) $g['id'] ?>">Assign to website</label><select class="form-select form-select-sm" id="al-s-<?= (int) $g['id'] ?>" name="website_id"><option value="0">— later —</option><?php foreach ($websites as $w): ?><option value="<?= (int) $w['id'] ?>"><?= e((string) $w['domain']) ?></option><?php endforeach; ?></select></div>
                    <div class="col-sm-3"><div class="form-check"><input class="form-check-input" type="checkbox" name="dns" value="1" id="al-d-<?= (int) $g['id'] ?>" checked><label class="form-check-label small" for="al-d-<?= (int) $g['id'] ?>">Add AAAA</label></div></div>
                    <div class="col-12"><button class="btn btn-sm btn-outline-primary">Allocate</button></div>
                </form>
                <form class="d-flex gap-2 mb-2" method="post" action="<?= e($this->url('ips.ranges.update', ['id' => (int) $g['id']])) ?>"><?= $this->csrfField() ?><input class="form-control form-control-sm" name="note" value="<?= e((string) $g['note']) ?>" placeholder="Note" aria-label="Note" maxlength="300"><button class="btn btn-sm btn-outline-secondary">Save note</button></form>
                <form method="post" action="<?= e($this->url('ips.ranges.delete', ['id' => (int) $g['id']])) ?>" data-wcp-confirm="Remove the range <?= e((string) $g['prefix']) ?> and its free allocations? Its route is removed."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger"<?= (int) $g['used'] > 0 ? ' disabled title="Addresses of this range are in use"' : '' ?>>Remove range</button></form>
                <?php elseif ($g['note'] !== ''): ?><p class="mb-0"><?= e((string) $g['note']) ?></p><?php endif; ?>
            </div>
        </div>
    </div>
<?php endforeach; ?>
<?php if ($ranges === []): ?>
    <div class="col-12"><div class="wcp-panel p-4 text-center wcp-muted">No IPv6 range yet.<?= $canManage ? ' Use "Add an IPv6 range" at the top right.' : '' ?></div></div>
<?php endif; ?>
</div>
<?php if ($canManage): ?>
<?= $this->modalStart('range-add', 'Add an IPv6 range', isset($errors['prefix']), '') ?>
    <form method="post" action="<?= e($this->url('ips.ranges.store')) ?>" data-wcp-busy>
        <?= $this->csrfField() ?>
        <label class="form-label small" for="rg-p">Range</label>
        <input class="form-control form-control-sm font-monospace<?= isset($errors['prefix']) ? ' is-invalid' : '' ?>" id="rg-p" name="prefix" value="<?= e((string) ($old['prefix'] ?? '')) ?>" placeholder="2001:db8:100::/64" required>
        <?php if (isset($errors['prefix'])): ?><div class="invalid-feedback"><?= e($errors['prefix']) ?></div><?php endif; ?>
        <label class="form-label small mt-2" for="rg-n">Note</label><input class="form-control form-control-sm" id="rg-n" name="note" maxlength="300">
        <div class="form-text">A range your provider routes to this server. NimVor adds a local route for it and keeps it at boot.</div>
        <div class="d-flex justify-content-end gap-2 mt-3"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Add range</button></div>
    </form>
<?= $this->modalEnd() ?>
<?php endif; ?>
