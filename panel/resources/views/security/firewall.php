<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $fw */
$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE);
$rules = (array) ($fw['rules'] ?? []);
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($fw !== [] && empty($fw['installed'])): ?>
    <div class="alert alert-info">UFW is not installed. Install it from <a href="<?= e($this->url('packages')) ?>">Packages</a>.</div>
<?php elseif ($fw !== []): ?>
<div class="row g-3">
    <div class="col-xl-8">
        <div class="card">
            <div class="card-header d-flex align-items-center gap-2"><i class="bi bi-bricks"></i>UFW
                <?= !empty($fw['active']) ? $this->statusBadge('active', 'active') : $this->statusBadge('inactive', 'inactive') ?>
                <?php if (!empty($fw['default_incoming'])): ?><span class="small text-body-secondary">incoming: <?= e($fw['default_incoming']) ?> · outgoing: <?= e($fw['default_outgoing'] ?? '?') ?></span><?php endif; ?>
                <?php if ($canManage): ?>
                    <form class="ms-auto" method="post" action="<?= e($this->url('security.firewall.toggle')) ?>"<?= !empty($fw['active']) ? ' data-wcp-confirm="Disable the firewall? Every listening service becomes reachable."' : '' ?>>
                        <?= $this->csrfField() ?><input type="hidden" name="enabled" value="<?= !empty($fw['active']) ? '0' : '1' ?>">
                        <button class="btn btn-sm <?= !empty($fw['active']) ? 'btn-outline-danger' : 'btn-primary' ?>"<?= empty($fw['active']) && empty($fw['ssh_allowed']) ? ' disabled title="Allow SSH first"' : '' ?>><?= !empty($fw['active']) ? 'Disable' : 'Enable firewall' ?></button>
                    </form>
                <?php endif; ?>
            </div>
            <?php if (empty($fw['ssh_allowed'])): ?>
                <div class="alert alert-warning rounded-0 border-0 mb-0 small"><i class="bi bi-exclamation-triangle me-1"></i>No rule allows SSH on port <?= (int) ($fw['ssh_port'] ?? 22) ?>. Add one before enabling the firewall.</div>
            <?php endif; ?>
            <?php if ($rules === []): ?>
                <div class="card-body text-body-secondary">No rules.</div>
            <?php else: ?>
                <ul class="list-group list-group-flush">
                    <?php foreach ($rules as $r): ?>
                        <li class="list-group-item d-flex align-items-center gap-2">
                            <span class="badge <?= in_array($r['action'], ['allow', 'limit'], true) ? 'text-bg-success' : 'text-bg-danger' ?>"><?= e($r['action']) ?></span>
                            <code class="small text-break"><?= e($r['spec']) ?></code>
                            <?php if (!empty($r['ssh'])): ?><span class="badge text-bg-light border">SSH</span><?php endif; ?>
                            <?php if ($canManage): ?>
                                <form class="ms-auto" method="post" action="<?= e($this->url('security.firewall.delete')) ?>" data-wcp-confirm="Delete the rule “<?= e($r['spec']) ?>”?"><?= $this->csrfField() ?><input type="hidden" name="spec" value="<?= e($r['spec']) ?>"><button class="btn btn-sm btn-outline-danger" title="Delete"><i class="bi bi-trash"></i></button></form>
                            <?php endif; ?>
                        </li>
                    <?php endforeach; ?>
                </ul>
            <?php endif; ?>
        </div>
    </div>
    <?php if ($canManage): ?>
        <div class="col-xl-4">
            <div class="card">
                <div class="card-header"><i class="bi bi-plus-lg me-1"></i>Add a rule</div>
                <div class="card-body">
                    <form method="post" action="<?= e($this->url('security.firewall.add')) ?>" data-wcp-busy>
                        <?= $this->csrfField() ?>
                        <div class="row g-2 mb-2">
                            <div class="col-6"><label class="form-label small" for="r-action">Action</label>
                                <select class="form-select form-select-sm" id="r-action" name="action"><option value="allow">Allow</option><option value="limit">Allow, rate-limited</option><option value="deny">Deny</option><option value="reject">Reject</option></select></div>
                            <div class="col-6"><label class="form-label small" for="r-proto">Protocol</label>
                                <select class="form-select form-select-sm" id="r-proto" name="proto"><option value="tcp">TCP</option><option value="udp">UDP</option><option value="">Both</option></select></div>
                        </div>
                        <label class="form-label small" for="r-port">Port or range</label>
                        <input class="form-control form-control-sm mb-2" id="r-port" name="port" placeholder="443 or 8000:8100" required>
                        <label class="form-label small" for="r-from">From (optional)</label>
                        <input class="form-control form-control-sm mb-2" id="r-from" name="from" placeholder="Anywhere, or 203.0.113.4 / 10.0.0.0/8">
                        <label class="form-label small" for="r-comment">Comment (optional)</label>
                        <input class="form-control form-control-sm mb-3" id="r-comment" name="comment" maxlength="60" placeholder="Monitoring">
                        <button class="btn btn-sm btn-primary">Add rule</button>
                        <div class="form-text">Common ports: 22 SSH, 80 HTTP, 443 HTTPS. Changes are undone after 2 minutes unless you confirm them.</div>
                    </form>
                </div>
            </div>
        </div>
    <?php endif; ?>
</div>
<?php endif; ?>
