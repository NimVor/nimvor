<?php
/**
 * The allow and deny list of a website or of the whole server.
 *
 * @var \NimVor\View\View $this
 * @var string $base page URL (actions are posted to base/add, /remove, /status and /import)
 * @var bool $canManage
 * @var list<array<string, string>> $rules
 * @var int $status
 * @var int $max
 * @var array<string, string> $kinds
 * @var array<string, string> $actions
 * @var array<int, string> $statuses
 * @var string $scope "this website" or "every website"
 */
?>
<p class="small text-body-secondary">Refuse or always serve visitors by <strong>address</strong> (one address, a network such as <code>10.0.0.0/8</code> or a range), by <strong>browser</strong> (the User-Agent contains the text) or by <strong>URL</strong> (the path starts with it). An <strong>allow</strong> entry always wins over a deny entry, so a trusted address or URL is never locked out. Certificate renewal (<code>/.well-known/acme-challenge/</code>) is never refused.</p>
<div class="row g-3">
    <div class="col-lg-7">
        <div class="card mb-3">
            <div class="card-header d-flex justify-content-between align-items-center">
                <span>List for <?= e($scope) ?> <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($rules) ?></span></span>
                <a class="btn btn-sm btn-outline-secondary" href="<?= e($base . '/export') ?>"><i class="bi bi-download" aria-hidden="true"></i> Export</a>
            </div>
            <?php if ($rules === []): ?>
                <div class="nim-empty-state"><i class="bi bi-slash-circle" aria-hidden="true"></i><div class="fw-medium">The list is empty</div><div class="small">Nothing is refused. Add an entry or import a list.</div></div>
            <?php else: ?>
                <div class="nim-dtable table-responsive">
                    <table class="table align-middle mb-0" id="al-rules">
                        <thead><tr><th>Type</th><th>Entry</th><th>Rule</th><th class="text-end"></th></tr></thead>
                        <tbody>
                        <?php foreach ($rules as $r): $allow = ($r['action'] ?? '') === 'allow'; ?>
                            <tr>
                                <td class="small"><?= e($kinds[$r['kind']] ?? $r['kind']) ?></td>
                                <td class="font-monospace small text-break"><?= e($r['value']) ?><?= ($r['note'] ?? '') !== '' ? '<div class="text-body-secondary font-sans-serif">' . e($r['note']) . '</div>' : '' ?></td>
                                <td><?= $allow ? '<span class="nim-state is-ok">Allow</span>' : '<span class="nim-state is-bad">Deny</span>' ?></td>
                                <td class="text-end">
                                    <?php if ($canManage): ?>
                                    <form class="d-inline" method="post" action="<?= e($base . '/remove') ?>" data-nim-busy>
                                        <?= $this->csrfField() ?><input type="hidden" name="kind" value="<?= e($r['kind']) ?>"><input type="hidden" name="value" value="<?= e($r['value']) ?>">
                                        <button class="btn btn-sm btn-outline-danger">Remove</button>
                                    </form>
                                    <?php endif; ?>
                                </td>
                            </tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
            <?php endif; ?>
        </div>
        <?php if ($canManage): ?>
        <div class="card mb-3">
            <div class="card-header">When a visitor is refused</div>
            <div class="card-body">
                <form class="d-flex flex-wrap align-items-end gap-2" method="post" action="<?= e($base . '/status') ?>" data-nim-busy>
                    <?= $this->csrfField() ?>
                    <div>
                        <label class="form-label small" for="al-status">Visitors get</label>
                        <select class="form-select form-select-sm" id="al-status" name="status">
                            <?php foreach ($statuses as $code => $label): ?><option value="<?= (int) $code ?>"<?= $status === $code ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                        </select>
                    </div>
                    <button class="btn btn-outline-primary btn-sm">Save</button>
                </form>
            </div>
        </div>
        <div class="card">
            <div class="card-header">Import a list</div>
            <div class="card-body">
                <form method="post" action="<?= e($base . '/import') ?>" data-nim-busy>
                    <?= $this->csrfField() ?>
                    <label class="form-label small" for="al-text">One entry per line: <code>deny ip 203.0.113.0/24</code>, <code>allow url /api/</code>, <code>deny ua BadBot  # note</code></label>
                    <textarea class="form-control form-control-sm font-monospace mb-2" id="al-text" name="text" rows="5" spellcheck="false" placeholder="deny ip 203.0.113.0/24&#10;allow ip 198.51.100.7&#10;deny ua SemrushBot&#10;deny url /xmlrpc.php"></textarea>
                    <div class="d-flex flex-wrap align-items-center gap-3">
                        <div class="form-check"><input class="form-check-input" type="radio" name="mode" id="al-merge" value="merge" checked><label class="form-check-label small" for="al-merge">Add to the list</label></div>
                        <div class="form-check"><input class="form-check-input" type="radio" name="mode" id="al-replace" value="replace"><label class="form-check-label small" for="al-replace">Replace the list</label></div>
                        <button class="btn btn-primary btn-sm ms-auto">Import</button>
                    </div>
                </form>
            </div>
        </div>
        <?php endif; ?>
    </div>
    <?php if ($canManage): ?>
    <div class="col-lg-5">
        <div class="card">
            <div class="card-header">Add an entry</div>
            <div class="card-body">
                <form method="post" action="<?= e($base . '/add') ?>" data-nim-busy autocomplete="off">
                    <?= $this->csrfField() ?>
                    <div class="row g-2 mb-2">
                        <div class="col-6">
                            <label class="form-label small" for="al-kind">Type</label>
                            <select class="form-select form-select-sm" id="al-kind" name="kind"><?php foreach ($kinds as $k => $label): ?><option value="<?= e($k) ?>"><?= e($label) ?></option><?php endforeach; ?></select>
                        </div>
                        <div class="col-6">
                            <label class="form-label small" for="al-action">Rule</label>
                            <select class="form-select form-select-sm" id="al-action" name="action"><?php foreach ($actions as $k => $label): ?><option value="<?= e($k) ?>"><?= e($label) ?></option><?php endforeach; ?></select>
                        </div>
                    </div>
                    <div class="mb-2">
                        <label class="form-label small" for="al-value">Address, browser text or URL</label>
                        <input class="form-control form-control-sm font-monospace" id="al-value" name="value" maxlength="200" required placeholder="203.0.113.0/24">
                        <div class="form-text">Address: <code>203.0.113.7</code>, <code>10.0.0.0/8</code>, <code>10.0.0.1-10.0.0.99</code>, IPv6 too. Browser: part of the User-Agent, any case. URL: starts with <code>/</code>.</div>
                    </div>
                    <div class="mb-3">
                        <label class="form-label small" for="al-note">Note (optional)</label>
                        <input class="form-control form-control-sm" id="al-note" name="note" maxlength="100">
                    </div>
                    <button class="btn btn-primary btn-sm">Save entry</button>
                    <span class="small text-body-secondary ms-2">Up to <?= (int) $max ?> entries.</span>
                </form>
            </div>
        </div>
    </div>
    <?php endif; ?>
</div>
<ul class="small text-body-secondary mt-3">
    <li>Where a network and an address inside it disagree, the more specific one decides: deny <code>10.0.0.0/8</code> and allow <code>10.0.0.5</code> keeps that one address in.</li>
    <li>Visitors are matched by their address. Behind a CDN or proxy every visitor has the proxy's address unless the real address is restored first.</li>
    <li>An allowed address is also never counted by this website's traffic limits.</li>
</ul>
