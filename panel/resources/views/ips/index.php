<?php
/**
 * @var \WhaCP\View\View $this
 * @var list<array<string, mixed>> $pool
 * @var list<array<string, mixed>> $unpooled
 * @var array<string, mixed>|null $server
 * @var array<string, mixed>|null $rebuild
 */
use WhaCP\Security\Permissions as P;

$canManage = $this->can(P::SERVER_MANAGE);
$errors ??= [];
$old ??= [];
?>
<?= $this->partial('ips/_tabs', get_defined_vars()) ?>
<?php if ($canManage): ?><?php $this->start(); ?><button type="button" class="btn btn-sm btn-primary" data-bs-toggle="modal" data-bs-target="#ip-add"><i class="bi bi-plus-lg me-1"></i>Add an address</button><?php $this->end('actions'); ?><?php endif; ?>

<div class="wcp-infobar">
    <i class="bi bi-info-circle" aria-hidden="true"></i>
    <span>Websites listen on every address unless bound to one from this pool. WhaCP does not configure network interfaces: add an IPv4 address to the server first (your provider's panel or the system's network configuration), then to the pool. Routed IPv6 ranges are made usable under <a href="<?= e($this->url('ips.ranges')) ?>">IPv6 ranges</a>.</span>
</div>

<?php if (is_array($rebuild) && $rebuild['missing'] !== []): ?>
    <div class="alert alert-warning small">No longer on the server (kept in the pool; websites bound to them do not answer on them): <span class="font-monospace"><?= e(implode(', ', $rebuild['missing'])) ?></span></div>
<?php endif; ?>

<div class="wcp-panel" data-wcp-dt>
    <div class="wcp-actionbar">
        <div class="fw-semibold"><i class="bi bi-ethernet me-1"></i>Pool <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($pool) ?></span></div>
        <div class="wcp-actionbar-end">
            <select class="form-select form-select-sm w-auto" aria-label="Family" data-wcp-dt-select="family"><option value="">IPv4 and IPv6</option><option value="4">IPv4</option><option value="6">IPv6</option></select>
            <select class="form-select form-select-sm w-auto" aria-label="State" data-wcp-dt-select="state"><option value="">All</option><option value="free">Free</option><option value="used">In use</option><option value="reserved">Reserved</option><option value="dedicated">Dedicated</option><option value="missing">Not on the server</option></select>
            <div class="wcp-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search addresses" data-wcp-dt-search><i class="bi bi-search" aria-hidden="true"></i></div>
            <?php if ($canManage): ?><form method="post" action="<?= e($this->url('ips.rebuild')) ?>" data-wcp-busy data-wcp-confirm="Rebuild the pool from the server's interfaces and the websites' addresses? Nothing is removed."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-repeat me-1"></i>Rebuild pool</button></form><?php endif; ?>
        </div>
    </div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Address</th><th>Type</th><th>Assignment</th><th>Websites</th><th>Status</th><th class="text-end">Operate</th></tr></thead>
            <tbody>
            <?php foreach ($pool as $a): $used = $a['sites'] !== [];
                $state = trim(($used ? 'used' : 'free') . ((int) $a['reserved'] ? ' reserved' : '') . ($a['kind'] === 'dedicated' ? ' dedicated' : '') . ($a['present'] === false ? ' missing' : '')); ?>
                <tr data-wcp-row data-dt-family="<?= (int) $a['family'] ?>" data-dt-state="<?= e($state) ?>">
                    <td class="fw-semibold font-monospace text-break"><?= e((string) $a['address']) ?></td>
                    <td class="small">IPv<?= (int) $a['family'] ?><?= $a['range_id'] !== null ? ' <span class="wcp-muted">· from a range</span>' : '' ?></td>
                    <td class="small"><?= (int) $a['reserved'] ? '<span class="wcp-state is-warn">Reserved</span>' . ($a['reason'] !== '' ? ' <span class="wcp-muted">' . e((string) $a['reason']) . '</span>' : '') : e(ucfirst((string) $a['kind'])) ?></td>
                    <td class="small"><?php if (!$used): ?><span class="wcp-muted">—</span><?php else: foreach ($a['sites'] as $s): ?><div><a href="<?= e($this->url('sites.advanced', ['id' => (int) $s['id']])) ?>#ip"><?= e((string) $s['domain']) ?></a><?= $s['owner'] ? ' <span class="wcp-muted">(' . e((string) $s['owner']) . ')</span>' : '' ?></div><?php endforeach; endif; ?></td>
                    <td class="small"><?= $a['present'] === null ? '<span class="wcp-muted">unknown</span>' : ($a['present'] ? '<span class="wcp-state is-ok">On the server</span>' : '<span class="wcp-state is-error">Not on the server</span>') ?></td>
                    <td class="text-end text-nowrap">
                        <?php if ($canManage): ?>
                            <button type="button" class="btn btn-sm btn-outline-secondary" data-bs-toggle="modal" data-bs-target="#ip-edit-<?= (int) $a['id'] ?>">Edit</button>
                            <?php if (!$used): ?><form class="d-inline" method="post" action="<?= e($this->url('ips.delete', ['id' => (int) $a['id']])) ?>" data-wcp-confirm="Remove <?= e((string) $a['address']) ?> from the pool? The server keeps the address."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger">Remove</button></form><?php endif; ?>
                        <?php endif; ?>
                    </td>
                </tr>
                <?php if ($canManage): ?>
                <?php ob_start(); ?><?= $this->modalStart('ip-edit-' . (int) $a['id'], 'Edit ' . $a['address']) ?>
                    <form class="row g-2 align-items-end" method="post" action="<?= e($this->url('ips.update', ['id' => (int) $a['id']])) ?>" data-wcp-busy><?= $this->csrfField() ?>
                        <div class="col-sm-3"><label class="form-label small" for="ipk-<?= (int) $a['id'] ?>">Assignment</label><select class="form-select form-select-sm" id="ipk-<?= (int) $a['id'] ?>" name="kind"><option value="shared">Shared (several websites)</option><option value="dedicated"<?= $a['kind'] === 'dedicated' ? ' selected' : '' ?>>Dedicated (one website)</option></select></div>
                        <div class="col-sm-2"><div class="form-check"><input class="form-check-input" type="checkbox" name="reserved" value="1" id="ipr-<?= (int) $a['id'] ?>"<?= (int) $a['reserved'] ? ' checked' : '' ?>><label class="form-check-label small" for="ipr-<?= (int) $a['id'] ?>">Reserved</label></div></div>
                        <div class="col-sm-5"><label class="form-label small" for="ipn-<?= (int) $a['id'] ?>">Reason / comment</label><input class="form-control form-control-sm" id="ipn-<?= (int) $a['id'] ?>" name="reason" maxlength="300" value="<?= e((string) $a['reason']) ?>" placeholder="e.g. mail server, kept for a customer"></div>
                        <div class="col-sm-2 text-end"><button class="btn btn-sm btn-primary">Save</button></div>
                    </form>
                <?= $this->modalEnd() ?><?php $editWindows[] = ob_get_clean(); ?>
                <?php endif; ?>
            <?php endforeach; ?>
            <tr class="<?= $pool === [] ? '' : 'd-none' ?>" data-wcp-dt-empty><td colspan="6" class="text-center py-4 wcp-muted">The pool is empty. Add the server's addresses (Add an address), or rebuild the pool.</td></tr>
            </tbody>
        </table>
    </div>
    <div class="wcp-pager" data-wcp-dt-pager></div>
</div>
<?= implode('', $editWindows ?? []) ?>

<div class="row g-3 mt-0">
    <div class="col-12">
        <div class="card">
            <div class="card-header"><i class="bi bi-hdd-network me-1"></i>Addresses on the server, not in the pool</div>
            <div class="card-body small">
                <?php if ($server === null): ?><span class="wcp-muted">Unknown (the agent did not answer).</span>
                <?php elseif ($unpooled === []): ?><span class="wcp-muted">None: every address of the server is in the pool.</span>
                <?php else: foreach ($unpooled as $u): ?>
                    <div class="d-flex align-items-center gap-2 mb-1"><span class="font-monospace"><?= e($u['address']) ?>/<?= (int) $u['prefix_len'] ?></span><span class="wcp-muted"><?= e($u['interface']) ?></span>
                        <?php if ($canManage): ?><form class="ms-auto" method="post" action="<?= e($this->url('ips.store')) ?>" data-wcp-busy><?= $this->csrfField() ?><input type="hidden" name="address" value="<?= e($u['address']) ?>"><input type="hidden" name="kind" value="shared"><button class="btn btn-sm btn-outline-primary">Add to pool</button></form><?php endif; ?></div>
                <?php endforeach; endif; ?>
                <?php if (!empty($server['gateways'])): ?><div class="mt-2 wcp-muted">Gateways: <span class="font-monospace"><?= e(implode(', ', (array) $server['gateways'])) ?></span></div><?php endif; ?>
            </div>
        </div>
    </div>
</div>
<?php if ($canManage): ?>
<?= $this->modalStart('ip-add', 'Add an address', isset($errors['address']), '') ?>
    <form method="post" action="<?= e($this->url('ips.store')) ?>" data-wcp-busy>
        <?= $this->csrfField() ?>
        <div class="row g-2">
            <div class="col-sm-7"><label class="form-label small" for="ip-new">Address</label><input class="form-control form-control-sm font-monospace<?= isset($errors['address']) ? ' is-invalid' : '' ?>" id="ip-new" name="address" value="<?= e((string) ($old['address'] ?? '')) ?>" placeholder="203.0.113.20 or 2001:db8::20" required><?php if (isset($errors['address'])): ?><div class="invalid-feedback"><?= e($errors['address']) ?></div><?php endif; ?></div>
            <div class="col-sm-5"><label class="form-label small" for="ip-kind">Assignment</label><select class="form-select form-select-sm" id="ip-kind" name="kind"><option value="shared">Shared</option><option value="dedicated">Dedicated</option></select></div>
            <div class="col-12"><label class="form-label small" for="ip-reason">Comment</label><input class="form-control form-control-sm" id="ip-reason" name="reason" maxlength="300"></div>
        </div>
        <div class="form-text">The address must already be configured on this server; it is checked.</div>
        <div class="d-flex justify-content-end gap-2 mt-3"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Add</button></div>
    </form>
<?= $this->modalEnd() ?>
<?php endif; ?>
