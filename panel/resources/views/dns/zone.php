<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $zone
 * @var list<array<string, mixed>> $records
 * @var int|null $served
 * @var array<string, mixed>|null $site
 */
use NimVor\Services\DnsService;

$id = (int) $zone['id'];
$errors ??= [];
$old ??= [];
$site ??= null;
$zoneName = (string) $zone['name'];
$sort ??= '';
$warnings ??= [];
$import ??= null;
// After a validation error the record form opens again with what was typed.
$reopen = $errors !== [] && isset($old['type']);
$recJson = static fn (array $r): string => (string) json_encode([
    'id' => (int) $r['id'], 'type' => (string) $r['type'], 'name' => (string) $r['name'], 'value' => (string) $r['value'],
    'ttl' => (int) $r['ttl'], 'priority' => (int) $r['priority'], 'weight' => (int) $r['weight'], 'port' => (int) $r['port'],
]);
$fv = static fn (string $k, mixed $d = '') => (string) ($old[$k] ?? $d);
$bad = static fn (string $k): string => isset($errors[$k]) ? ' is-invalid' : '';
$err = static fn (string $k): string => isset($errors[$k]) ? '<div class="invalid-feedback">' . e((string) $errors[$k]) . '</div>' : '';
[$caaTag, $caaVal] = array_pad(explode(' ', $fv('value'), 2), 2, '');
$sortLink = fn (string $k): string => $this->url('dns.zone', ['id' => $id], $k === $sort ? [] : ['sort' => $k]) . '#records';
?>
<?php if ($site !== null): ?>
    <?= $this->partial('sites/header', ['site' => $site, 'tab' => 'dns']) ?>
<?php else: ?>
    <?php $this->start(); ?><span class="font-monospace"><?= e($zoneName) ?></span><?php $this->end('title'); ?>
    <?php $this->start(); ?>DNS zone<?php $this->end('subtitle'); ?>

<?php endif; ?>

<div class="d-flex flex-wrap align-items-center gap-2 mb-3 small">
    <span class="text-body-secondary">Zone <code><?= e($zoneName) ?></code></span>
    <?php if ($served === null): ?><span class="nim-state is-error">Not served by this server</span>
    <?php elseif ($served !== (int) $zone['serial']): ?><span class="nim-state is-warn">Update pending (serving serial <?= (int) $served ?>)</span>
    <?php else: ?><span class="nim-state is-ok">Live</span><?php endif; ?>
    <span class="text-body-secondary">· serial <?= (int) $zone['serial'] ?></span>
</div>

<?php if ($warnings !== []): ?>
<div class="alert alert-warning small" role="status">
    <div class="fw-semibold mb-1"><i class="bi bi-exclamation-triangle me-1"></i>Check these</div>
    <ul class="mb-0 ps-3"><?php foreach ($warnings as $w): ?><li><?= e($w) ?></li><?php endforeach; ?></ul>
</div>
<?php endif; ?>
<?php if (is_array($import) && $import !== []): ?>
<details class="alert alert-secondary small" open>
    <summary class="fw-semibold">Lines not imported (<?= count($import) ?>)</summary>
    <ul class="mb-0 mt-1 ps-3"><?php foreach ($import as $sk): ?><li><?= (int) $sk['line'] > 0 ? 'Line ' . (int) $sk['line'] . ': ' : '' ?><code><?= e(mb_strimwidth((string) $sk['text'], 0, 120, '…')) ?></code> — <?= e((string) $sk['reason']) ?></li><?php endforeach; ?></ul>
</details>
<?php endif; ?>

<div class="card mb-3" id="records" data-nim-dt>
    <div class="card-header nim-toolbar">
        <h2 class="nim-toolbar-title"><i class="bi bi-list-ul me-1"></i>DNS records <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($records) ?></span></h2>
        <div class="d-flex flex-wrap gap-2 ms-auto">
            <select class="form-select form-select-sm w-auto" aria-label="Record type" data-nim-dt-select="type"><option value="">All types</option><?php foreach (DnsService::TYPES as $t): ?><option><?= e($t) ?></option><?php endforeach; ?><option value="off">Turned off</option></select>
            <div class="nim-search"><input class="form-control form-control-sm" type="search" placeholder="Search records" aria-label="Search records" data-nim-dt-search><i class="bi bi-search" aria-hidden="true"></i></div>
            <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('dns.export', ['id' => $id])) ?>" title="Download the zone file (BIND format)"><i class="bi bi-download me-1"></i>Export</a>
            <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#dns-import"><i class="bi bi-upload me-1"></i>Import</button>
            <button class="btn btn-sm btn-primary" type="button" data-nim-dns-new><i class="bi bi-plus-lg me-1"></i>Add record</button>
        </div>
    </div>
    <?php if ($records === []): ?>
        <div class="nim-empty-state"><i class="bi bi-signpost" aria-hidden="true"></i><div class="fw-medium">No records yet</div><div class="small">Add an A record so your domain points to this server.</div></div>
    <?php else: ?>
    <form id="dns-bulk" method="post" action="<?= e($this->url('dns.bulk', ['id' => $id])) ?>"><?= $this->csrfField() ?></form>
    <div class="nim-actionbar px-3 pt-2">
        <button class="btn btn-sm btn-outline-secondary" form="dns-bulk" name="op" value="enable">Turn on</button>
        <button class="btn btn-sm btn-outline-secondary" form="dns-bulk" name="op" value="disable">Turn off</button>
        <button class="btn btn-sm btn-outline-danger" form="dns-bulk" name="op" value="delete" data-nim-confirm-submit="Delete the selected records? The zone is reloaded without them.">Delete</button>
        <span class="small text-body-secondary">for the selected records</span>
    </div>
    <div class="nim-dtable table-responsive">
        <table class="table mb-0 align-middle" id="dns-table">
            <thead><tr>
                <th class="nim-w-5"><span class="visually-hidden">Select</span></th>
                <?php foreach (['type' => 'Type', 'name' => 'Name', 'value' => 'Value', 'ttl' => 'TTL'] as $k => $l): ?>
                    <th class="<?= $k === 'ttl' ? 'text-end' : '' ?>"<?= $sort === $k ? ' aria-sort="ascending"' : '' ?>><a class="link-body-emphasis text-decoration-none" href="<?= e($sortLink($k)) ?>"><?= e($l) ?><?= $sort === $k ? ' <i class="bi bi-caret-up-fill" aria-hidden="true"></i>' : '' ?></a></th>
                <?php endforeach; ?>
                <th class="text-end">Actions</th>
            </tr></thead>
            <tbody>
            <?php foreach ($records as $r): $off = !empty($r['disabled']); ?>
                <tr data-nim-row<?= $off ? ' class="text-body-secondary"' : '' ?> data-dt-type="<?= e((string) $r['type'] . ($off ? ' off' : '')) ?>">
                    <td><input class="form-check-input" type="checkbox" form="dns-bulk" name="ids[]" value="<?= (int) $r['id'] ?>" aria-label="Select <?= e($r['type'] . ' ' . $r['name']) ?>"></td>
                    <td><span class="badge text-bg-light border"><?= e((string) $r['type']) ?></span><?= $off ? ' <span class="badge text-bg-light border fw-normal">off</span>' : '' ?></td>
                    <td class="font-monospace text-break"><?= e((string) $r['name']) ?></td>
                    <td class="font-monospace small text-break"><?= in_array($r['type'], ['MX', 'SRV', 'CAA'], true) ? e((string) $r['priority']) . ' ' : '' ?><?= $r['type'] === 'SRV' ? e($r['weight'] . ' ' . $r['port']) . ' ' : '' ?><?= e((string) $r['value']) ?></td>
                    <td class="text-end small"><?= (int) $r['ttl'] > 0 ? (int) $r['ttl'] : '<span class="text-body-secondary" title="Zone default">' . (int) $zone['ttl'] . '</span>' ?></td>
                    <td class="text-end text-nowrap">
                        <button class="btn btn-sm btn-outline-secondary" type="button" data-nim-dns-edit="<?= e($recJson($r)) ?>" data-nim-dns-action="<?= e($this->url('dns.records.update', ['id' => $id, 'record' => (int) $r['id']])) ?>" title="Edit" aria-label="Edit <?= e($r['type'] . ' ' . $r['name']) ?>"><i class="bi bi-pencil"></i></button>
                        <form class="d-inline" method="post" action="<?= e($this->url('dns.bulk', ['id' => $id])) ?>" data-nim-busy><?= $this->csrfField() ?><input type="hidden" name="ids[]" value="<?= (int) $r['id'] ?>"><input type="hidden" name="op" value="<?= $off ? 'enable' : 'disable' ?>"><button class="btn btn-sm btn-outline-secondary" title="<?= $off ? 'Turn on' : 'Turn off' ?>" aria-label="<?= $off ? 'Turn on' : 'Turn off' ?> <?= e($r['type'] . ' ' . $r['name']) ?>"><i class="bi bi-<?= $off ? 'play' : 'pause' ?>"></i></button></form>
                        <form class="d-inline" method="post" action="<?= e($this->url('dns.records.delete', ['id' => $id, 'record' => (int) $r['id']])) ?>" data-nim-confirm="Delete the <?= e($r['type'] . ' record ' . $r['name']) ?>?"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" title="Delete" aria-label="Delete <?= e($r['type'] . ' ' . $r['name']) ?>"><i class="bi bi-trash"></i></button></form>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
    <div class="nim-pager px-3 pb-2" data-nim-dt-pager></div>
    <?php endif; ?>
</div>

<div class="modal fade" id="dns-record" tabindex="-1" aria-labelledby="dns-record-title" aria-hidden="true"<?= $reopen ? ' data-nim-dns-reopen="' . e($this->url(isset($old['_record']) && (int) $old['_record'] > 0 ? 'dns.records.update' : 'dns.records.store', isset($old['_record']) && (int) $old['_record'] > 0 ? ['id' => $id, 'record' => (int) $old['_record']] : ['id' => $id])) . '"' : '' ?>>
    <div class="modal-dialog modal-lg">
        <form class="modal-content" method="post" action="<?= e($this->url('dns.records.store', ['id' => $id])) ?>" data-nim-busy data-nim-dns-form data-store="<?= e($this->url('dns.records.store', ['id' => $id])) ?>">
            <?= $this->csrfField() ?><input type="hidden" name="_record" value="<?= e($fv('_record', '0')) ?>">
            <div class="modal-header"><h2 class="modal-title fs-5" id="dns-record-title">DNS record</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body">
                <div class="nim-shortcuts mb-3" aria-label="Common records" data-nim-dns-shortcuts>
                    <span class="small text-body-secondary me-1 align-self-center">Common:</span>
                    <button type="button" class="btn btn-sm btn-outline-secondary" data-nim-dns-preset='{"type":"A","name":"@"}'>A (domain → IP)</button>
                    <button type="button" class="btn btn-sm btn-outline-secondary" data-nim-dns-preset='{"type":"CNAME","name":"www","value":"@"}'>CNAME www</button>
                    <button type="button" class="btn btn-sm btn-outline-secondary" data-nim-dns-preset='{"type":"MX","name":"@","priority":10}'>MX (mail)</button>
                    <button type="button" class="btn btn-sm btn-outline-secondary" data-nim-dns-preset='{"type":"TXT","name":"@","value":"v=spf1 mx ~all"}'>TXT (SPF)</button>
                    <button type="button" class="btn btn-sm btn-outline-secondary" data-nim-dns-preset='{"type":"CAA","name":"@","value":"issue letsencrypt.org"}'>CAA (Let's Encrypt)</button>
                </div>
                <div class="row g-2">
                    <div class="col-sm-3"><label class="form-label small" for="rec-type">Type</label>
                        <select class="form-select form-select-sm<?= $bad('type') ?>" id="rec-type" name="type" data-nim-toggle-kind><?php foreach (DnsService::TYPES as $t): ?><option<?= $fv('type', 'A') === $t ? ' selected' : '' ?>><?= e($t) ?></option><?php endforeach; ?></select><?= $err('type') ?></div>
                    <div class="col-sm-6"><label class="form-label small" for="rec-name">Name</label><input class="form-control form-control-sm font-monospace<?= $bad('name') ?>" id="rec-name" name="name" value="<?= e($fv('name', '@')) ?>" placeholder="@, www, mail…"><?= $err('name') ?><div class="form-text">Relative to <code><?= e($zoneName) ?></code>; <code>@</code> is the domain itself.</div></div>
                    <div class="col-sm-3"><label class="form-label small" for="rec-ttl">TTL (seconds)</label><input class="form-control form-control-sm<?= $bad('ttl') ?>" id="rec-ttl" type="number" min="0" max="604800" name="ttl" value="<?= e($fv('ttl', 0)) ?>"><?= $err('ttl') ?><div class="form-text">0 = zone default (<?= (int) $zone['ttl'] ?>)</div></div>
                </div>
                <div class="row g-2 mt-1" data-nim-kind="SRV">
                    <div class="col-sm-3"><label class="form-label small" for="rec-srvs">Service</label><input class="form-control form-control-sm font-monospace" id="rec-srvs" name="srv_service" value="<?= e($fv('srv_service')) ?>" placeholder="sip"></div>
                    <div class="col-sm-3"><label class="form-label small" for="rec-srvp">Protocol</label><select class="form-select form-select-sm" id="rec-srvp" name="srv_protocol"><option value="">(in the name)</option><?php foreach (['tcp', 'udp', 'tls'] as $p): ?><option<?= $fv('srv_protocol') === $p ? ' selected' : '' ?>><?= $p ?></option><?php endforeach; ?></select></div>
                    <div class="col-sm-6 form-text align-self-end">With a service and protocol the name becomes <code>_service._protocol</code> (plus the name, unless it is @).</div>
                </div>
                <div class="row g-2 mt-1">
                    <div class="col-sm-3" data-nim-kind="MX SRV"><label class="form-label small" for="rec-prio">Priority</label><input class="form-control form-control-sm<?= $bad('priority') ?>" id="rec-prio" type="number" min="0" max="65535" name="priority" value="<?= e($fv('priority', 10)) ?>"><?= $err('priority') ?></div>
                    <div class="col-sm-3" data-nim-kind="SRV"><label class="form-label small" for="rec-weight">Weight</label><input class="form-control form-control-sm<?= $bad('weight') ?>" id="rec-weight" type="number" min="0" max="65535" name="weight" value="<?= e($fv('weight', 0)) ?>"><?= $err('weight') ?></div>
                    <div class="col-sm-3" data-nim-kind="SRV"><label class="form-label small" for="rec-port">Port</label><input class="form-control form-control-sm<?= $bad('port') ?>" id="rec-port" type="number" min="0" max="65535" name="port" value="<?= e($fv('port', 0)) ?>"><?= $err('port') ?></div>
                    <div class="col-12" data-nim-kind="A AAAA CNAME NS PTR MX SRV">
                        <label class="form-label small" for="rec-value" data-nim-dns-value-label>Value</label>
                        <input class="form-control form-control-sm font-monospace<?= $bad('value') ?>" id="rec-value" name="value" value="<?= e($fv('value')) ?>"><?= $err('value') ?>
                        <div class="form-text" data-nim-dns-hint></div>
                    </div>
                    <div class="col-12" data-nim-kind="TXT"><label class="form-label small" for="rec-txt">Text</label><textarea class="form-control form-control-sm font-monospace<?= $bad('value') ?>" id="rec-txt" name="value" rows="3" maxlength="4000"><?= e($fv('value')) ?></textarea><?= $err('value') ?><div class="form-text">Without surrounding quotes; long values are split into 255-character strings automatically.</div></div>
                    <div class="col-sm-3" data-nim-kind="CAA"><label class="form-label small" for="rec-caaf">Flags</label><select class="form-select form-select-sm<?= $bad('caa_flags') ?>" id="rec-caaf" name="caa_flags"><option value="0">0</option><option value="128"<?= $fv('caa_flags', $fv('priority')) === '128' ? ' selected' : '' ?>>128 (critical)</option></select><?= $err('caa_flags') ?></div>
                    <div class="col-sm-4" data-nim-kind="CAA"><label class="form-label small" for="rec-caat">Tag</label><select class="form-select form-select-sm<?= $bad('caa_tag') ?>" id="rec-caat" name="caa_tag"><?php foreach (DnsService::CAA_TAGS as $k => $l): ?><option value="<?= e($k) ?>"<?= $fv('caa_tag', $caaTag) === $k ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?></select><?= $err('caa_tag') ?></div>
                    <div class="col-sm-5" data-nim-kind="CAA"><label class="form-label small" for="rec-caav">Value</label><input class="form-control form-control-sm font-monospace<?= $bad('caa_value') ?>" id="rec-caav" name="caa_value" value="<?= e($fv('caa_value', $caaVal)) ?>" placeholder="letsencrypt.org"><?= $err('caa_value') ?></div>
                </div>
                <div class="form-text mt-2">Every change is checked and loaded at once; if the DNS server refuses it, nothing changes.</div>
            </div>
            <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save record</button></div>
        </form>
    </div>
</div>

<div class="modal fade" id="dns-import" tabindex="-1" aria-labelledby="dns-import-title" aria-hidden="true">
    <div class="modal-dialog modal-lg">
        <form class="modal-content" method="post" action="<?= e($this->url('dns.import', ['id' => $id])) ?>" enctype="multipart/form-data" data-nim-busy>
            <?= $this->csrfField() ?>
            <div class="modal-header"><h2 class="modal-title fs-5" id="dns-import-title">Import records</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
            <div class="modal-body">
                <p class="small text-body-secondary">A zone file in BIND format, as most DNS hosts export it. Records that are not in the zone yet are added; the SOA, the name servers at @ and unsupported lines are left out and listed.</p>
                <label class="form-label small" for="imp-file">Zone file</label>
                <input class="form-control form-control-sm mb-2" type="file" id="imp-file" name="zonefile_upload" accept=".zone,.txt,.db,text/plain">
                <label class="form-label small" for="imp-text">or paste it</label>
                <textarea class="form-control form-control-sm font-monospace" id="imp-text" name="zonefile" rows="8" placeholder="www  3600  IN  A  192.0.2.10"></textarea>
            </div>
            <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Import</button></div>
        </form>
    </div>
</div>

<?php if ($this->serverScope()): /* Zone-wide settings and deletion are for administrators; a hosting customer manages only the records above. */ ?>
<div class="row g-3">
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-sliders me-1"></i>Zone settings</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('dns.settings', ['id' => $id])) ?>" data-nim-busy>
                    <?= $this->csrfField() ?>
                    <?= $this->partial('partials/field', ['name' => 'ttl', 'label' => 'Default TTL (seconds)', 'type' => 'number', 'value' => (string) ($old['ttl'] ?? $zone['ttl'])]) ?>
                    <?= $this->partial('partials/field', ['name' => 'secondaries', 'label' => 'Secondary servers (IP addresses)', 'value' => (string) ($old['secondaries'] ?? str_replace(',', ', ', (string) $zone['secondaries'])), 'help' => 'They may transfer the zone and are notified of changes. Empty: transfers refused.']) ?>
                    <dl class="nim-dl small"><dt>SOA</dt><dd><code><?= e($zone['primary_ns'] . '. ' . $zone['hostmaster']) ?></code></dd></dl>
                    <button class="btn btn-sm btn-primary">Save</button>
                </form>
                <hr>
                <form method="post" action="<?= e($this->url('dns.sync', ['id' => $id])) ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-repeat me-1"></i>Load the zone again</button> <span class="small text-body-secondary">e.g. after reinstalling the DNS server</span></form>
            </div>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card nim-danger-card">
            <div class="card-header"><i class="bi bi-exclamation-octagon me-1"></i>Danger zone</div>
            <div class="card-body">
                <p class="small text-body-secondary">Deleting the zone removes all records above. This server stops answering for <code><?= e($zoneName) ?></code>.</p>
                <form method="post" action="<?= e($this->url('dns.delete', ['id' => $id])) ?>" data-nim-confirm="Delete the zone <?= e($zoneName) ?>? This server stops answering for it." data-nim-confirm-word="<?= e($zoneName) ?>">
                    <?= $this->csrfField() ?><input type="hidden" name="confirm" value="<?= e($zoneName) ?>">
                    <button class="btn btn-sm btn-outline-danger"><i class="bi bi-trash me-1"></i>Delete zone</button>
                </form>
            </div>
        </div>
    </div>
</div>
<?php endif; ?>
