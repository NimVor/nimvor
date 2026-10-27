<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $site */
/** @var list<array<string, mixed>> $subdomains */
/** @var bool $hasZone */
$id = (int) $site['id'];
$errors ??= [];
$old ??= [];
$open = $errors !== [];
$subError = $open ? ($errors['subdomain'] ?? $errors['domain'] ?? $errors['system_user'] ?? null) : null;
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'subdomains']) ?>
<p class="small text-body-secondary">A subdomain like <code>blog.<?= e($site['domain']) ?></code> is a website of its own (its own folder, PHP, SSL, databases…) that runs under this website's account <code><?= e($site['system_user']) ?></code>. Manage each one like any website; delete it from its own Advanced page.</p>

<div class="card" id="subdomains">
    <div class="card-header wcp-toolbar">
        <h2 class="wcp-toolbar-title"><i class="bi bi-diagram-3 me-1"></i>Subdomains <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($subdomains) ?></span></h2>
        <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#new-subdomain"><i class="bi bi-plus-lg me-1"></i>Create subdomain</button>
    </div>
    <?= $this->modalStart('new-subdomain', 'Create subdomain', $open) ?>
        <form class="p-3" method="post" action="<?= e($this->url('sites.subdomains.store', ['id' => $id])) ?>" data-wcp-busy>
            <?= $this->csrfField() ?><input type="hidden" name="type" value="subdomain">
            <div class="row g-2 align-items-end">
                <div class="col-md-6">
                    <label class="form-label small" for="sd-name">Subdomain</label>
                    <div class="input-group input-group-sm">
                        <input class="form-control<?= $subError !== null ? ' is-invalid' : '' ?>" id="sd-name" name="subdomain" value="<?= e($open ? (string) ($old['subdomain'] ?? '') : '') ?>" placeholder="blog" required autocomplete="off">
                        <span class="input-group-text">.<?= e($site['domain']) ?></span>
                    </div>
                </div>
                <div class="col-md-auto"><button class="btn btn-sm btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Create</button></div>
            </div>
            <?php if ($subError !== null): ?><div class="text-danger small mt-1"><?= e((string) $subError) ?></div><?php endif; ?>
            <?php if ($hasZone): ?>
                <div class="form-check small mt-2"><input type="hidden" name="dns" value="0"><input class="form-check-input" type="checkbox" name="dns" value="1" id="sd-dns" checked><label class="form-check-label" for="sd-dns">Add an A record for it to this domain's DNS zone</label></div>
            <?php else: ?>
                <div class="form-text">Point the subdomain to this server in your domain's DNS (an A record).</div>
            <?php endif; ?>
        </form>
    <?= $this->modalEnd() ?>
    <?php if ($subdomains === []): ?>
        <div class="wcp-empty-state"><i class="bi bi-diagram-3" aria-hidden="true"></i><div class="small">No subdomains yet. A subdomain like blog.<?= e($site['domain']) ?> can host a separate site.</div></div>
    <?php else: ?>
        <div class="table-responsive">
            <table class="table align-middle mb-0 wcp-table">
                <thead><tr><th>Domain</th><th>Folder</th><th>Status</th><th>HTTPS</th><th class="text-end">Actions</th></tr></thead>
                <tbody>
                <?php foreach ($subdomains as $c): ?>
                    <tr>
                        <td class="fw-semibold"><?= e($c['domain']) ?></td>
                        <td class="small"><code><?= e($c['document_root']) ?></code></td>
                        <td class="small"><?= $c['enabled'] ? '<span class="wcp-state is-ok">Active</span>' : '<span class="wcp-state is-off">Disabled</span>' ?></td>
                        <td class="small"><?= $c['https'] ? '<i class="bi bi-lock-fill text-success"></i> On' : '<span class="text-body-secondary">Off</span>' ?></td>
                        <td class="text-end text-nowrap">
                            <?php if ($c['enabled']): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e(($c['https'] ? 'https://' : 'http://') . $c['domain']) ?>/" target="_blank" rel="noopener noreferrer" title="Visit" aria-label="Visit <?= e($c['domain']) ?>"><i class="bi bi-box-arrow-up-right"></i></a><?php endif; ?>
                            <a class="btn btn-sm btn-outline-primary" href="<?= e($this->url('sites.show', ['id' => (int) $c['id']])) ?>">Manage</a>
                        </td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
</div>
