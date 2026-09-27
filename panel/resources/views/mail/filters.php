<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $domain
 * @var array<string, mixed> $mailbox
 * @var array{filters: list<array<string, mixed>>, responder: array<string, mixed>}|null $data
 */
use WhaCP\Services\MailFilterService as F;

$id = (int) $domain['id'];
$mb = (int) $mailbox['id'];
$addr = (string) $mailbox['address'];
$modal = function (?int $n, ?array $f) use ($id, $mb): string {
    $key = $n === null ? 'new' : (string) $n;
    $rules = array_pad($f['rules'] ?? [], 4, ['part' => 'subject', 'match' => 'contains', 'value' => '']);
    $acts = array_pad($f['actions'] ?? [], 3, ['type' => '', 'value' => '']);
    ob_start(); ?>
    <div class="modal fade" id="filter-<?= e($key) ?>" tabindex="-1" aria-labelledby="filter-title-<?= e($key) ?>" aria-hidden="true">
        <div class="modal-dialog modal-lg modal-dialog-scrollable">
            <form class="modal-content" method="post" action="<?= e($this->url('mail.filters.save', ['id' => $id, 'mailbox' => $mb])) ?>" data-wcp-busy>
                <?= $this->csrfField() ?>
                <?php if ($n !== null): ?><input type="hidden" name="index" value="<?= $n ?>"><?php endif; ?>
                <input type="hidden" name="enabled" value="<?= $f === null || !empty($f['enabled']) ? '1' : '0' ?>">
                <div class="modal-header"><h2 class="modal-title fs-5" id="filter-title-<?= e($key) ?>"><?= $f === null ? 'New filter' : 'Edit filter' ?></h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
                <div class="modal-body">
                    <div class="row g-2 mb-3">
                        <div class="col-md-7"><label class="form-label" for="fn-<?= e($key) ?>">Filter name</label><input class="form-control" id="fn-<?= e($key) ?>" name="name" required maxlength="100" value="<?= e((string) ($f['name'] ?? '')) ?>"></div>
                        <div class="col-md-5"><label class="form-label" for="fa-<?= e($key) ?>">Apply when</label>
                            <select class="form-select" id="fa-<?= e($key) ?>" name="any"><option value="0">all conditions match</option><option value="1"<?= !empty($f['any']) ? ' selected' : '' ?>>any condition matches</option></select></div>
                    </div>
                    <div class="fw-medium mb-1">Conditions</div>
                    <?php foreach ($rules as $i => $r): ?>
                        <div class="row g-2 mb-2">
                            <div class="col-4 col-md-3"><select class="form-select form-select-sm" name="rules[<?= $i ?>][part]" aria-label="Part"><?php foreach (F::PARTS as $k => $l): ?><option value="<?= e($k) ?>"<?= $r['part'] === $k ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?></select></div>
                            <div class="col-8 col-md-3"><select class="form-select form-select-sm" name="rules[<?= $i ?>][match]" aria-label="Comparison"><?php foreach (F::MATCHES as $k => $l): ?><option value="<?= e($k) ?>"<?= $r['match'] === $k ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?></select></div>
                            <div class="col-md-6"><input class="form-control form-control-sm" name="rules[<?= $i ?>][value]" maxlength="200" value="<?= e((string) $r['value']) ?>" placeholder="<?= $i === 0 ? 'e.g. invoice' : 'optional' ?>" aria-label="Value"></div>
                        </div>
                    <?php endforeach; ?>
                    <div class="fw-medium mt-3 mb-1">Actions</div>
                    <?php foreach ($acts as $i => $a): ?>
                        <div class="row g-2 mb-2">
                            <div class="col-md-5"><select class="form-select form-select-sm" name="actions[<?= $i ?>][type]" aria-label="Action"><option value=""><?= $i === 0 ? 'Choose an action' : '—' ?></option><?php foreach (F::ACTIONS as $k => $l): ?><option value="<?= e($k) ?>"<?= $a['type'] === $k ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?></select></div>
                            <div class="col-md-7"><input class="form-control form-control-sm" name="actions[<?= $i ?>][value]" maxlength="200" value="<?= e((string) ($a['value'] ?? '')) ?>" placeholder="folder name or address, when needed" aria-label="Folder or address"></div>
                        </div>
                    <?php endforeach; ?>
                    <div class="form-text">Folders are created when needed. Filters run in order after the spam filter; “Stop processing” skips the filters below.</div>
                </div>
                <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save filter</button></div>
            </form>
        </div>
    </div>
    <?php return (string) ob_get_clean();
};
?>
<?php $this->start(); ?>Filters for <span class="font-monospace"><?= e($addr) ?></span><?php $this->end('title'); ?>
<?php $this->start(); ?>Email filters and autoresponder<?php $this->end('subtitle'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('mail.domain', ['id' => $id])) ?>"><i class="bi bi-arrow-left"></i> <?= e((string) $domain['domain']) ?></a><?php $this->end('actions'); ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php return; endif; ?>
<?php $r = $data['responder']; ?>

<div class="row g-3">
    <div class="col-xl-7">
        <div class="card" id="filters">
            <div class="card-header wcp-toolbar">
                <h2 class="wcp-toolbar-title"><i class="bi bi-funnel me-1"></i>Email filters <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($data['filters']) ?></span></h2>
                <button class="btn btn-sm btn-outline-primary" type="button" data-bs-toggle="modal" data-bs-target="#filter-new"><i class="bi bi-plus-lg me-1"></i>New filter</button>
            </div>
            <?php if ($data['filters'] === []): ?>
                <div class="wcp-empty-state"><i class="bi bi-funnel" aria-hidden="true"></i><div class="fw-medium">No filters</div><div class="small">Sort, forward or delete incoming mail by sender, recipient, subject or content.</div></div>
            <?php else: ?>
                <div class="wcp-dtable table-responsive">
                    <table class="table align-middle mb-0">
                        <thead><tr><th>Filter</th><th>When</th><th>Then</th><th></th></tr></thead>
                        <tbody>
                        <?php foreach ($data['filters'] as $n => $f): ?>
                            <tr<?= empty($f['enabled']) ? ' class="text-body-secondary"' : '' ?>>
                                <td class="fw-semibold"><?= e($f['name']) ?><?= empty($f['enabled']) ? ' <span class="badge text-bg-light border fw-normal">off</span>' : '' ?></td>
                                <td class="small"><?= e(implode(!empty($f['any']) ? ' or ' : ' and ', array_map(static fn (array $x): string => (F::PARTS[$x['part']] ?? $x['part']) . ' ' . (F::MATCHES[$x['match']] ?? $x['match']) . ' “' . $x['value'] . '”', $f['rules']))) ?></td>
                                <td class="small"><?= e(implode(', ', array_map(static fn (array $x): string => (F::ACTIONS[$x['type']] ?? $x['type']) . (($x['value'] ?? '') !== '' ? ' ' . $x['value'] : ''), $f['actions']))) ?></td>
                                <td class="text-end text-nowrap">
                                    <form class="d-inline" method="post" action="<?= e($this->url('mail.filters.toggle', ['id' => $id, 'mailbox' => $mb, 'n' => $n])) ?>" data-wcp-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary" title="<?= empty($f['enabled']) ? 'Enable' : 'Disable' ?>" aria-label="<?= empty($f['enabled']) ? 'Enable' : 'Disable' ?> <?= e($f['name']) ?>"><i class="bi bi-<?= empty($f['enabled']) ? 'play' : 'pause' ?>"></i></button></form>
                                    <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#filter-<?= $n ?>" aria-label="Edit <?= e($f['name']) ?>"><i class="bi bi-pencil"></i></button>
                                    <form class="d-inline" method="post" action="<?= e($this->url('mail.filters.delete', ['id' => $id, 'mailbox' => $mb, 'n' => $n])) ?>" data-wcp-confirm="Delete the filter “<?= e($f['name']) ?>”?"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" aria-label="Delete <?= e($f['name']) ?>"><i class="bi bi-trash"></i></button></form>
                                </td>
                            </tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
            <?php endif; ?>
        </div>
    </div>
    <div class="col-xl-5">
        <div class="card" id="autoresponder">
            <div class="card-header d-flex align-items-center"><i class="bi bi-reply me-1"></i>Autoresponder<span class="ms-auto"><?= !empty($r['enabled']) ? '<span class="wcp-state is-ok">On</span>' : '<span class="wcp-state is-off">Off</span>' ?></span></div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('mail.responder', ['id' => $id, 'mailbox' => $mb])) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <div class="form-check form-switch mb-3"><input type="hidden" name="enabled" value="0"><input class="form-check-input" type="checkbox" role="switch" id="ar-on" name="enabled" value="1"<?= !empty($r['enabled']) ? ' checked' : '' ?>><label class="form-check-label" for="ar-on">Reply automatically to incoming mail</label></div>
                    <label class="form-label" for="ar-subject">Subject</label>
                    <input class="form-control mb-2" id="ar-subject" name="subject" maxlength="200" value="<?= e((string) $r['subject']) ?>" placeholder="Out of office">
                    <label class="form-label" for="ar-body">Message</label>
                    <textarea class="form-control mb-2" id="ar-body" name="body" rows="5" maxlength="4000"><?= e((string) $r['body']) ?></textarea>
                    <div class="row g-2 mb-3">
                        <div class="col-4"><label class="form-label small" for="ar-days">Once per sender every (days)</label><input class="form-control form-control-sm" id="ar-days" type="number" min="1" max="30" name="days" value="<?= (int) ($r['days'] ?: 1) ?>"></div>
                        <div class="col-4"><label class="form-label small" for="ar-start">From (optional)</label><input class="form-control form-control-sm" id="ar-start" type="date" name="start" value="<?= e((string) ($r['start'] ?? '')) ?>"></div>
                        <div class="col-4"><label class="form-label small" for="ar-end">Until (optional)</label><input class="form-control form-control-sm" id="ar-end" type="date" name="end" value="<?= e((string) ($r['end'] ?? '')) ?>"></div>
                    </div>
                    <button class="btn btn-sm btn-primary">Save autoresponder</button>
                    <div class="form-text">Mailing lists and automatic messages are not answered.</div>
                </form>
            </div>
        </div>
    </div>
</div>
<?= $modal(null, null) ?>
<?php foreach ($data['filters'] as $n => $f): ?><?= $modal($n, $f) ?><?php endforeach; ?>
