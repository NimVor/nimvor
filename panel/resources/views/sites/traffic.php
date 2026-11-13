<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var string $sub
 * @var list<array<string, mixed>> $limits
 * @var int $limitStatus
 * @var int $max
 * @var array<int, string> $statuses
 */
$id = (int) $site['id'];
$tabs = ['limits' => 'Traffic limits'];
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'traffic']) ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php return; endif; ?>

<?php if (count($tabs) > 1): ?>
<ul class="nav nav-tabs mb-3">
    <?php foreach ($tabs as $key => $label): ?>
        <li class="nav-item"><a class="nav-link<?= $sub === $key ? ' active' : '' ?>" href="<?= e($this->url('sites.traffic', ['id' => $id], ['tab' => $key])) ?>"><?= e($label) ?></a></li>
    <?php endforeach; ?>
</ul>
<?php endif; ?>

<p class="small text-body-secondary">Traffic limits keep one visitor from using up a website: above the rate you set, Nginx answers with an error instead of loading the page. They suit login pages, search and APIs. Certificate renewal (<code>/.well-known/acme-challenge/</code>) is never limited.</p>

<div class="row g-3">
    <div class="col-lg-7">
        <div class="card mb-3">
            <div class="card-header">Limits of <?= e($site['domain']) ?> <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($limits) ?></span></div>
            <?php if ($limits === []): ?>
                <div class="nim-empty-state"><i class="bi bi-speedometer2" aria-hidden="true"></i><div class="fw-medium">No traffic limits</div><div class="small">Add a limit to slow down visitors that ask for too much.</div></div>
            <?php else: ?>
                <div class="nim-dtable table-responsive">
                    <table class="table align-middle mb-0" id="st-limits">
                        <thead><tr><th>Rule</th><th>Where</th><th>Limit</th><th>State</th><th class="text-end"></th></tr></thead>
                        <tbody>
                        <?php foreach ($limits as $l): $on = !empty($l['enabled']); ?>
                            <tr<?= $on ? '' : ' class="text-body-secondary"' ?>>
                                <td class="font-monospace small"><?= e((string) $l['name']) ?></td>
                                <td class="font-monospace small text-break"><?= e($site['domain'] . $l['path']) ?><?= ($l['match'] ?? '') === 'exact' ? ' <span class="badge text-bg-light border">exact</span>' : '' ?><?= ($l['path'] ?? '') === '/' ? ' <span class="badge text-bg-secondary">whole website</span>' : '' ?></td>
                                <td class="small"><?= (int) $l['rate'] ?> per <?= ($l['unit'] ?? 's') === 'm' ? 'minute' : 'second' ?><?= (int) ($l['burst'] ?? 0) > 0 ? ', burst ' . (int) $l['burst'] : '' ?><br><span class="text-body-secondary"><?= !empty($l['per_ua']) ? 'per address and browser' : 'per address' ?></span></td>
                                <td><?= $on ? '<span class="nim-state is-ok">On</span>' : '<span class="nim-state is-off">Off</span>' ?></td>
                                <td class="text-end text-nowrap">
                                    <form class="d-inline" method="post" action="<?= e($this->url('sites.traffic.limit.toggle', ['id' => $id])) ?>" data-nim-busy>
                                        <?= $this->csrfField() ?><input type="hidden" name="name" value="<?= e((string) $l['name']) ?>"><input type="hidden" name="enabled" value="<?= $on ? '0' : '1' ?>">
                                        <button class="btn btn-sm btn-outline-secondary"><?= $on ? 'Turn off' : 'Turn on' ?></button>
                                    </form>
                                    <form class="d-inline" method="post" action="<?= e($this->url('sites.traffic.limit.remove', ['id' => $id])) ?>" data-nim-busy data-nim-confirm="Remove the limit <?= e((string) $l['name']) ?>?">
                                        <?= $this->csrfField() ?><input type="hidden" name="name" value="<?= e((string) $l['name']) ?>">
                                        <button class="btn btn-sm btn-outline-danger">Remove</button>
                                    </form>
                                </td>
                            </tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
            <?php endif; ?>
        </div>
        <div class="card">
            <div class="card-header">When a limit is exceeded</div>
            <div class="card-body">
                <form class="d-flex flex-wrap align-items-end gap-2" method="post" action="<?= e($this->url('sites.traffic.status', ['id' => $id])) ?>" data-nim-busy>
                    <?= $this->csrfField() ?>
                    <div>
                        <label class="form-label small" for="st-status">Visitors get</label>
                        <select class="form-select form-select-sm" id="st-status" name="status">
                            <?php foreach ($statuses as $code => $label): ?><option value="<?= (int) $code ?>"<?= $limitStatus === $code ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                        </select>
                    </div>
                    <button class="btn btn-outline-primary btn-sm">Save</button>
                </form>
                <div class="form-text mt-2">429 or 503 are the usual choices; search engines understand both as "come back later".</div>
            </div>
        </div>
    </div>
    <div class="col-lg-5">
        <div class="card">
            <div class="card-header">Add or change a limit</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('sites.traffic.limit', ['id' => $id])) ?>" data-nim-busy autocomplete="off">
                    <?= $this->csrfField() ?>
                    <div class="mb-2">
                        <label class="form-label small" for="st-name">Rule name</label>
                        <input class="form-control form-control-sm font-monospace" id="st-name" name="name" maxlength="40" required placeholder="login">
                        <div class="form-text">A name that already exists is changed.</div>
                    </div>
                    <div class="row g-2 mb-2">
                        <div class="col-5">
                            <label class="form-label small" for="st-match">Applies to</label>
                            <select class="form-select form-select-sm" id="st-match" name="match"><option value="prefix">A folder and below</option><option value="exact">One exact address</option></select>
                        </div>
                        <div class="col-7">
                            <label class="form-label small" for="st-path">Address</label>
                            <input class="form-control form-control-sm font-monospace" id="st-path" name="path" value="/" maxlength="200" required>
                        </div>
                    </div>
                    <div class="row g-2 mb-2">
                        <div class="col-6">
                            <label class="form-label small" for="st-rate">Allow</label>
                            <input class="form-control form-control-sm" id="st-rate" name="rate" type="number" min="1" max="60000" value="10" required>
                        </div>
                        <div class="col-6">
                            <label class="form-label small" for="st-unit">Requests per</label>
                            <select class="form-select form-select-sm" id="st-unit" name="unit"><option value="s">second</option><option value="m">minute</option></select>
                        </div>
                    </div>
                    <div class="mb-2">
                        <label class="form-label small" for="st-burst">Burst</label>
                        <input class="form-control form-control-sm" id="st-burst" name="burst" type="number" min="0" max="1000" value="0">
                        <div class="form-text">Extra requests allowed at once before the limit answers (a page and its files load together).</div>
                    </div>
                    <div class="form-check mb-3">
                        <input class="form-check-input" type="checkbox" id="st-ua" name="per_ua" value="1">
                        <label class="form-check-label small" for="st-ua">Count an address and its browser together</label>
                    </div>
                    <button class="btn btn-primary btn-sm"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save limit</button>
                    <span class="small text-body-secondary ms-2">Up to <?= (int) $max ?> limits.</span>
                </form>
            </div>
        </div>
    </div>
</div>
<ul class="small text-body-secondary mt-3">
    <li>Visitors are counted by their address. Behind a CDN or proxy (Cloudflare, for example) every visitor has the proxy's address unless the real address is restored first, so a limit would hit all of them together.</li>
    <li>Use limits for login pages, search and APIs. A limit on the whole website also counts images and scripts; allow a generous rate and burst.</li>
    <li>Several limits can apply to one request; the first to trip answers. Each limit counts on its own.</li>
</ul>
