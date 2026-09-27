<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $zone
 * @var list<array<string, mixed>> $records
 * @var int|null $served
 * @var int $edit
 * @var array<string, mixed>|null $site
 */
use WhaCP\Services\DnsService;

$id = (int) $zone['id'];
$errors ??= [];
$old ??= [];
$site ??= null;
$zoneName = (string) $zone['name'];
$openAdd = $errors !== [] && $edit === 0;
$form = function (?array $r, string $action, string $label, string $idp) use ($old, $errors): string {
    $v = static fn (string $k, mixed $d = '') => (string) ($old[$k] ?? ($r[$k] ?? $d));
    ob_start(); ?>
    <form method="post" action="<?= e($action) ?>" class="row g-2 align-items-end wcp-dns-form" data-wcp-busy>
        <?= $this->csrfField() ?>
        <div class="col-6 col-md-2"><label class="form-label small" for="<?= e($idp) ?>-type">Type</label>
            <select class="form-select form-select-sm" id="<?= e($idp) ?>-type" name="type" data-wcp-toggle-kind>
                <?php foreach (DnsService::TYPES as $t): ?><option<?= $v('type', 'A') === $t ? ' selected' : '' ?>><?= $t ?></option><?php endforeach; ?>
            </select></div>
        <div class="col-6 col-md-2"><label class="form-label small" for="<?= e($idp) ?>-name">Name</label><input class="form-control form-control-sm font-monospace<?= isset($errors['name']) ? ' is-invalid' : '' ?>" id="<?= e($idp) ?>-name" name="name" value="<?= e($v('name', '@')) ?>" required></div>
        <div class="col-md-4"><label class="form-label small" for="<?= e($idp) ?>-value">Value</label><input class="form-control form-control-sm font-monospace<?= isset($errors['value']) ? ' is-invalid' : '' ?>" id="<?= e($idp) ?>-value" name="value" value="<?= e($v('value')) ?>" required></div>
        <div class="col-4 col-md-1" data-wcp-kind="MX SRV CAA"><label class="form-label small" for="<?= e($idp) ?>-prio" title="Priority (CAA: flags)">Priority</label><input class="form-control form-control-sm" id="<?= e($idp) ?>-prio" type="number" min="0" max="65535" name="priority" value="<?= e($v('priority', 10)) ?>"></div>
        <div class="col-4 col-md-1" data-wcp-kind="SRV"><label class="form-label small" for="<?= e($idp) ?>-weight">Weight</label><input class="form-control form-control-sm" id="<?= e($idp) ?>-weight" type="number" min="0" max="65535" name="weight" value="<?= e($v('weight', 0)) ?>"></div>
        <div class="col-4 col-md-1" data-wcp-kind="SRV"><label class="form-label small" for="<?= e($idp) ?>-port">Port</label><input class="form-control form-control-sm" id="<?= e($idp) ?>-port" type="number" min="0" max="65535" name="port" value="<?= e($v('port', 0)) ?>"></div>
        <div class="col-4 col-md-1"><label class="form-label small" for="<?= e($idp) ?>-ttl">TTL</label><input class="form-control form-control-sm" id="<?= e($idp) ?>-ttl" type="number" min="0" name="ttl" value="<?= e($v('ttl', 0)) ?>" title="0 = zone default"></div>
        <div class="col-auto"><button class="btn btn-sm btn-primary" type="submit"><?= e($label) ?></button></div>
    </form>
    <?php return (string) ob_get_clean();
};
?>
<?php if ($site !== null): ?>
    <?= $this->partial('sites/header', ['site' => $site, 'tab' => 'dns']) ?>
<?php else: ?>
    <?php $this->start(); ?><span class="font-monospace"><?= e($zoneName) ?></span><?php $this->end('title'); ?>
    <?php $this->start(); ?>DNS zone<?php $this->end('subtitle'); ?>
    <?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('dns')) ?>"><i class="bi bi-arrow-left"></i> DNS</a><?php $this->end('actions'); ?>
<?php endif; ?>

<div class="d-flex flex-wrap align-items-center gap-2 mb-3 small">
    <span class="text-body-secondary">Zone <code><?= e($zoneName) ?></code></span>
    <?php if ($served === null): ?><span class="wcp-state is-error">Not served by this server</span>
    <?php elseif ($served !== (int) $zone['serial']): ?><span class="wcp-state is-warn">Update pending (serving serial <?= (int) $served ?>)</span>
    <?php else: ?><span class="wcp-state is-ok">Live</span><?php endif; ?>
    <span class="text-body-secondary">· serial <?= (int) $zone['serial'] ?></span>
</div>

<div class="card mb-3" id="records">
    <div class="card-header wcp-toolbar">
        <h2 class="wcp-toolbar-title"><i class="bi bi-list-ul me-1"></i>DNS records <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($records) ?></span></h2>
        <?php if (count($records) > 6): ?><input class="form-control form-control-sm wcp-filter" type="search" placeholder="Filter records" aria-label="Filter records" data-wcp-filter="#dns-table"><?php endif; ?>
        <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="collapse" data-bs-target="#add-record" aria-expanded="<?= $openAdd ? 'true' : 'false' ?>" aria-controls="add-record"><i class="bi bi-plus-lg me-1"></i>Add record</button>
    </div>
    <div class="collapse wcp-form-panel<?= $openAdd ? ' show' : '' ?>" id="add-record" data-wcp-form-panel>
        <div class="p-3">
            <div class="wcp-shortcuts mb-3" aria-label="Common records">
                <span class="small text-body-secondary me-1 align-self-center">Common:</span>
                <button type="button" class="btn btn-sm btn-outline-secondary" data-wcp-fill='{"#add-type":"A","#add-name":"@"}'>A (domain → IP)</button>
                <button type="button" class="btn btn-sm btn-outline-secondary" data-wcp-fill='{"#add-type":"CNAME","#add-name":"www","#add-value":"@"}'>CNAME www</button>
                <button type="button" class="btn btn-sm btn-outline-secondary" data-wcp-fill='{"#add-type":"MX","#add-name":"@","#add-prio":"10"}'>MX (mail)</button>
                <button type="button" class="btn btn-sm btn-outline-secondary" data-wcp-fill='{"#add-type":"TXT","#add-name":"@","#add-value":"v=spf1 mx ~all"}'>TXT (SPF)</button>
                <button type="button" class="btn btn-sm btn-outline-secondary" data-wcp-fill='{"#add-type":"CAA","#add-name":"@","#add-prio":"0","#add-value":"issue letsencrypt.org"}'>CAA</button>
            </div>
            <?= $form(null, $this->url('dns.records.store', ['id' => $id]), 'Add record', 'add') ?>
            <div class="form-text">Names are relative to <code><?= e($zoneName) ?></code> (<code>@</code> is the domain itself). Host names ending in a dot are absolute. Every change is checked and loaded immediately; if the DNS server refuses it, nothing is changed.</div>
        </div>
    </div>
    <?php if ($records === []): ?>
        <div class="wcp-empty-state"><i class="bi bi-signpost" aria-hidden="true"></i><div class="fw-medium">No records yet</div><div class="small">Add an A record so your domain points to this server.</div></div>
    <?php else: ?>
    <div class="table-responsive">
        <table class="table mb-0 wcp-table align-middle" id="dns-table">
            <thead><tr><th>Type</th><th>Name</th><th>Value</th><th class="text-end">TTL</th><th class="text-end">Actions</th></tr></thead>
            <tbody>
            <?php foreach ($records as $r): ?>
                <?php if ($edit === (int) $r['id']): ?>
                    <tr><td colspan="5" class="wcp-form-panel"><?= $form($r, $this->url('dns.records.update', ['id' => $id, 'record' => (int) $r['id']]), 'Save', 'edit') ?>
                        <a class="small" href="<?= e($this->url('dns.zone', ['id' => $id])) ?>">Cancel</a></td></tr>
                <?php else: ?>
                <tr>
                    <td><span class="badge text-bg-light border"><?= e((string) $r['type']) ?></span></td>
                    <td class="font-monospace"><?= e((string) $r['name']) ?></td>
                    <td class="font-monospace small text-break"><?= in_array($r['type'], ['MX', 'SRV', 'CAA'], true) ? e((string) $r['priority']) . ' ' : '' ?><?= $r['type'] === 'SRV' ? e($r['weight'] . ' ' . $r['port']) . ' ' : '' ?><?= e((string) $r['value']) ?></td>
                    <td class="text-end small"><?= (int) $r['ttl'] > 0 ? (int) $r['ttl'] : '<span class="text-body-secondary" title="Zone default">' . (int) $zone['ttl'] . '</span>' ?></td>
                    <td class="text-end text-nowrap">
                        <a class="btn btn-sm btn-outline-secondary" href="?edit=<?= (int) $r['id'] ?>" title="Edit" aria-label="Edit <?= e($r['type'] . ' ' . $r['name']) ?>"><i class="bi bi-pencil"></i></a>
                        <form class="d-inline" method="post" action="<?= e($this->url('dns.records.delete', ['id' => $id, 'record' => (int) $r['id']])) ?>" data-wcp-confirm="Delete the <?= e($r['type'] . ' record ' . $r['name']) ?>?"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" title="Delete" aria-label="Delete <?= e($r['type'] . ' ' . $r['name']) ?>"><i class="bi bi-trash"></i></button></form>
                    </td>
                </tr>
                <?php endif; ?>
            <?php endforeach; ?>
            <tr class="d-none" data-wcp-filter-empty><td colspan="5" class="text-body-secondary small">No record matches the filter.</td></tr>
            </tbody>
        </table>
    </div>
    <?php endif; ?>
</div>

<div class="row g-3">
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-sliders me-1"></i>Zone settings</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('dns.settings', ['id' => $id])) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <?= $this->partial('partials/field', ['name' => 'ttl', 'label' => 'Default TTL (seconds)', 'type' => 'number', 'value' => (string) ($old['ttl'] ?? $zone['ttl'])]) ?>
                    <?= $this->partial('partials/field', ['name' => 'secondaries', 'label' => 'Secondary servers (IP addresses)', 'value' => (string) ($old['secondaries'] ?? str_replace(',', ', ', (string) $zone['secondaries'])), 'help' => 'They may transfer the zone and are notified of changes. Empty: transfers refused.']) ?>
                    <dl class="wcp-dl small"><dt>SOA</dt><dd><code><?= e($zone['primary_ns'] . '. ' . $zone['hostmaster']) ?></code></dd></dl>
                    <button class="btn btn-sm btn-primary">Save</button>
                </form>
                <hr>
                <form method="post" action="<?= e($this->url('dns.sync', ['id' => $id])) ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-repeat me-1"></i>Load the zone again</button> <span class="small text-body-secondary">e.g. after reinstalling the DNS server</span></form>
            </div>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card wcp-danger-card">
            <div class="card-header"><i class="bi bi-exclamation-octagon me-1"></i>Danger zone</div>
            <div class="card-body">
                <p class="small text-body-secondary">Deleting the zone removes all records above. This server stops answering for <code><?= e($zoneName) ?></code>.</p>
                <form method="post" action="<?= e($this->url('dns.delete', ['id' => $id])) ?>" data-wcp-confirm="Delete the zone <?= e($zoneName) ?>? This server stops answering for it." data-wcp-confirm-word="<?= e($zoneName) ?>">
                    <?= $this->csrfField() ?><input type="hidden" name="confirm" value="<?= e($zoneName) ?>">
                    <button class="btn btn-sm btn-outline-danger"><i class="bi bi-trash me-1"></i>Delete zone</button>
                </form>
            </div>
        </div>
    </div>
</div>
