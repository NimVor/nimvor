<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $zone
 * @var list<array<string, mixed>> $records
 * @var int|null $served
 * @var int $edit
 */
use WhaCP\Services\DnsService;

$id = (int) $zone['id'];
$errors ??= [];
$old ??= [];
$hint = [
    'A' => 'IPv4 address', 'AAAA' => 'IPv6 address', 'CNAME' => 'host name (@ = the domain)', 'MX' => 'mail server host name',
    'TXT' => 'text', 'NS' => 'name server host name', 'SRV' => 'target host name', 'CAA' => 'issue letsencrypt.org',
];
$form = function (?array $r, string $action, string $label) use ($old, $errors): string {
    $v = static fn (string $k, mixed $d = '') => (string) ($old[$k] ?? ($r[$k] ?? $d));
    ob_start(); ?>
    <form method="post" action="<?= e($action) ?>" class="row g-2 align-items-end wcp-dns-form" data-wcp-busy>
        <?= $this->csrfField() ?>
        <div class="col-md-2"><label class="form-label small">Name</label><input class="form-control form-control-sm font-monospace<?= isset($errors['name']) ? ' is-invalid' : '' ?>" name="name" value="<?= e($v('name', '@')) ?>" required></div>
        <div class="col-md-2"><label class="form-label small">Type</label>
            <select class="form-select form-select-sm" name="type" data-wcp-toggle-kind>
                <?php foreach (DnsService::TYPES as $t): ?><option<?= $v('type', 'A') === $t ? ' selected' : '' ?>><?= $t ?></option><?php endforeach; ?>
            </select></div>
        <div class="col-md-4"><label class="form-label small">Value</label><input class="form-control form-control-sm font-monospace<?= isset($errors['value']) ? ' is-invalid' : '' ?>" name="value" value="<?= e($v('value')) ?>" required></div>
        <div class="col-md-1" data-wcp-kind="MX SRV CAA"><label class="form-label small" title="Priority (CAA: flags)">Prio.</label><input class="form-control form-control-sm" type="number" min="0" max="65535" name="priority" value="<?= e($v('priority', 10)) ?>"></div>
        <div class="col-md-1" data-wcp-kind="SRV"><label class="form-label small">Weight</label><input class="form-control form-control-sm" type="number" min="0" max="65535" name="weight" value="<?= e($v('weight', 0)) ?>"></div>
        <div class="col-md-1" data-wcp-kind="SRV"><label class="form-label small">Port</label><input class="form-control form-control-sm" type="number" min="0" max="65535" name="port" value="<?= e($v('port', 0)) ?>"></div>
        <div class="col-md-1"><label class="form-label small">TTL</label><input class="form-control form-control-sm" type="number" min="0" name="ttl" value="<?= e($v('ttl', 0)) ?>" title="0 = zone default"></div>
        <div class="col-auto"><button class="btn btn-sm btn-primary"><?= e($label) ?></button></div>
    </form>
    <?php return (string) ob_get_clean();
};
?>
<?php $this->start(); ?><span class="font-monospace"><?= e((string) $zone['name']) ?></span><?php $this->end('title'); ?>
<?php $this->start(); ?>DNS zone · serial <?= (int) $zone['serial'] ?>
<?php if ($served === null): ?> · <?= $this->statusBadge('error', 'not served') ?><?php elseif ($served !== (int) $zone['serial']): ?> · <?= $this->statusBadge('warning', 'served serial ' . $served) ?><?php else: ?> · <?= $this->statusBadge('active', 'served') ?><?php endif; ?>
<?php $this->end('subtitle'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('dns')) ?>"><i class="bi bi-arrow-left"></i> DNS</a><?php $this->end('actions'); ?>
<div class="card mb-3">
    <div class="card-header"><i class="bi bi-list-ul me-1"></i>Records</div>
    <div class="table-responsive">
        <table class="table mb-0 wcp-table align-middle">
            <thead><tr><th>Name</th><th>Type</th><th>Value</th><th class="text-end">TTL</th><th></th></tr></thead>
            <tbody>
            <?php foreach ($records as $r): ?>
                <?php if ($edit === (int) $r['id']): ?>
                    <tr><td colspan="5"><?= $form($r, $this->url('dns.records.update', ['id' => $id, 'record' => (int) $r['id']]), 'Save') ?></td></tr>
                <?php else: ?>
                <tr>
                    <td class="font-monospace"><?= e((string) $r['name']) ?></td>
                    <td><span class="badge text-bg-light border"><?= e((string) $r['type']) ?></span></td>
                    <td class="font-monospace small text-break"><?= in_array($r['type'], ['MX', 'SRV', 'CAA'], true) ? e((string) $r['priority']) . ' ' : '' ?><?= $r['type'] === 'SRV' ? e($r['weight'] . ' ' . $r['port']) . ' ' : '' ?><?= e((string) $r['value']) ?></td>
                    <td class="text-end small"><?= (int) $r['ttl'] > 0 ? (int) $r['ttl'] : '<span class="text-body-secondary">' . (int) $zone['ttl'] . '</span>' ?></td>
                    <td class="text-end text-nowrap">
                        <a class="btn btn-sm btn-outline-secondary" href="?edit=<?= (int) $r['id'] ?>" title="Edit"><i class="bi bi-pencil"></i></a>
                        <form class="d-inline" method="post" action="<?= e($this->url('dns.records.delete', ['id' => $id, 'record' => (int) $r['id']])) ?>" data-wcp-confirm="Delete the <?= e($r['type'] . ' record ' . $r['name']) ?>?"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" title="Delete"><i class="bi bi-trash"></i></button></form>
                    </td>
                </tr>
                <?php endif; ?>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
    <div class="card-body border-top">
        <h2 class="h6">Add a record</h2>
        <?= $form(null, $this->url('dns.records.store', ['id' => $id]), 'Add') ?>
        <div class="form-text">Names are relative to <code><?= e((string) $zone['name']) ?></code> (@ is the domain itself). Host names ending in a dot are absolute. Every change is validated and loaded immediately; if BIND refuses it, nothing is changed.</div>
    </div>
</div>
<div class="row g-3">
    <div class="col-lg-6">
        <div class="card">
            <div class="card-header"><i class="bi bi-sliders me-1"></i>Zone settings</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('dns.settings', ['id' => $id])) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <?= $this->partial('partials/field', ['name' => 'ttl', 'label' => 'Default TTL (seconds)', 'type' => 'number', 'value' => (string) ($old['ttl'] ?? $zone['ttl'])]) ?>
                    <?= $this->partial('partials/field', ['name' => 'secondaries', 'label' => 'Secondary servers (IP addresses)', 'value' => (string) ($old['secondaries'] ?? str_replace(',', ', ', (string) $zone['secondaries'])), 'help' => 'They may transfer the zone and are notified of changes. Empty: transfers refused.']) ?>
                    <dl class="wcp-dl small"><dt>SOA</dt><dd><code><?= e($zone['primary_ns'] . '. ' . $zone['hostmaster']) ?></code></dd></dl>
                    <button class="btn btn-sm btn-primary">Save</button>
                </form>
            </div>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card">
            <div class="card-header"><i class="bi bi-tools me-1"></i>Maintenance</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('dns.sync', ['id' => $id])) ?>" class="mb-3"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-repeat me-1"></i>Load the zone again</button> <span class="small text-body-secondary">e.g. after reinstalling the DNS server</span></form>
                <form method="post" action="<?= e($this->url('dns.delete', ['id' => $id])) ?>" data-wcp-confirm="Delete the zone <?= e((string) $zone['name']) ?>? This server stops answering for it." data-wcp-confirm-word="<?= e((string) $zone['name']) ?>">
                    <?= $this->csrfField() ?><input type="hidden" name="confirm" value="<?= e((string) $zone['name']) ?>">
                    <button class="btn btn-sm btn-outline-danger"><i class="bi bi-trash me-1"></i>Delete zone</button>
                </form>
            </div>
        </div>
    </div>
</div>
