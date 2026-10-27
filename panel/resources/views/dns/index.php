<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed>|null $status
 * @var list<array<string, mixed>> $zones
 * @var array<string, int> $served
 * @var list<array<string, mixed>> $websites
 * @var list<string> $nameservers
 * @var bool $admin
 * @var int $preselect
 */
use NimVor\Security\Permissions as P;

$errors ??= [];
$old ??= [];
$ready = !empty($status['configured']) && !empty($status['running']);
?>
<?php $this->start(); ?>DNS<?php $this->end('title'); ?>
<?php $this->start(); ?>Zones served by this server's DNS server (BIND)<?php $this->end('subtitle'); ?>
<?php if ($agentError !== null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError]) ?>
<?php elseif (!$ready): ?>
    <div class="card mb-3"><div class="card-body">
        <?php if (empty($status['installed'])): ?>
            <p>The DNS server (BIND) is not installed on this server.</p>
            <?php if ($this->can(P::PACKAGES_MANAGE)): ?><form method="post" action="<?= e($this->url('dns.install')) ?>"><?= $this->csrfField() ?><button class="btn btn-primary"><i class="bi bi-download me-1"></i>Install BIND</button></form><?php endif; ?>
        <?php elseif ($this->can(P::SERVER_MANAGE)): ?>
            <p><?= empty($status['configured']) ? 'BIND is installed but not set up for NimVor yet.' : 'BIND is set up but not running.' ?></p>
            <form method="post" action="<?= e($this->url('dns.setup')) ?>" data-nim-busy>
                <?= $this->csrfField() ?>
                <?php if (empty($status['configured'])): ?>
                    <div class="form-check mb-2"><input class="form-check-input" type="checkbox" name="confirm" value="1" id="dns-confirm" required><label class="form-check-label" for="dns-confirm">Add one <code>include</code> line for NimVor's zones to <code><?= e((string) $status['config_file']) ?></code> (a backup is kept)</label></div>
                <?php endif; ?>
                <button class="btn btn-primary"><i class="bi bi-play me-1"></i>Set up and start</button>
            </form>
        <?php else: ?>
            <p class="mb-0">The DNS server is not set up yet; ask the administrator.</p>
        <?php endif; ?>
    </div></div>
<?php endif; ?>
<?php if ($nameservers === []): ?>
    <div class="alert alert-info small"><?= $this->can(P::SETTINGS_MANAGE) ? 'Set the <strong>DNS name servers</strong> (and the default IPv4 for new zones) under <a href="' . e($this->url('settings')) . '">Settings</a> before creating zones.' : 'An administrator has to set the DNS name servers before zones can be created.' ?></div>
<?php endif; ?>
<div class="nim-panel">
    <div class="nim-actionbar">
        <div class="fw-semibold"><i class="bi bi-signpost me-1" aria-hidden="true"></i>Zones <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($zones) ?></span></div>
        <?php if ($ready && $nameservers !== []): ?><div class="nim-actionbar-end"><button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#new-zone"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>New zone</button></div><?php endif; ?>
    </div>
    <div class="nim-dtable">
<div class="table-responsive">
                <table class="table mb-0 nim-table align-middle">
                    <thead><tr><th>Zone</th><th>Website</th><th class="text-end">Records</th><th>Serial</th><th>Status</th></tr></thead>
                    <tbody>
                    <?php foreach ($zones as $z): $srv = $served[$z['name']] ?? null; ?>
                        <tr>
                            <td><a href="<?= e($this->url('dns.zone', ['id' => (int) $z['id']])) ?>" class="font-monospace"><?= e((string) $z['name']) ?></a></td>
                            <td><?= e((string) ($z['website'] ?? '—')) ?></td>
                            <td class="text-end"><?= (int) $z['records'] ?></td>
                            <td class="font-monospace small"><?= (int) $z['serial'] ?></td>
                            <td><?php if ($agentError !== null): ?>—<?php elseif ($srv === null): ?><?= $this->statusBadge('error', 'not served') ?><?php elseif ($srv !== (int) $z['serial']): ?><?= $this->statusBadge('warning', 'out of date') ?><?php else: ?><?= $this->statusBadge('active', 'served') ?><?php endif; ?></td>
                        </tr>
                    <?php endforeach; ?>
                    <?php if ($zones === []): ?><tr><td colspan="5" class="text-body-secondary">No zones yet.</td></tr><?php endif; ?>
                    </tbody>
                </table>
            </div>
    </div>
</div>
<?php if ($ready && $nameservers !== []): ?>
    <?= $this->modalStart('new-zone', 'New zone', isset($errors['website_id']) || isset($errors['name']), 'modal-md') ?>
                <form method="post" action="<?= e($this->url('dns.store')) ?>" data-nim-busy>
                    <?= $this->csrfField() ?>
                    <div class="mb-3">
                        <label class="form-label" for="dns-site">Website</label>
                        <select class="form-select<?= isset($errors['website_id']) ? ' is-invalid' : '' ?>" id="dns-site" name="website_id">
                            <?php if ($admin): ?><option value="">— none (enter the domain below) —</option><?php endif; ?>
                            <?php foreach ($websites as $w): ?><option value="<?= (int) $w['id'] ?>"<?= (int) ($old['website_id'] ?? $preselect) === (int) $w['id'] ? ' selected' : '' ?>><?= e((string) $w['domain']) ?></option><?php endforeach; ?>
                        </select>
                        <?php if (isset($errors['website_id'])): ?><div class="invalid-feedback"><?= e($errors['website_id']) ?></div><?php endif; ?>
                    </div>
                    <?php if ($admin): ?><?= $this->partial('partials/field', ['name' => 'name', 'label' => 'Domain (empty = the website\'s domain)', 'value' => (string) ($old['name'] ?? ''), 'attrs' => ['placeholder' => 'example.com']]) ?><?php endif; ?>
                    <div class="form-check mb-3"><input class="form-check-input" type="checkbox" name="template" value="1" id="dns-tpl" checked><label class="form-check-label" for="dns-tpl">Add the usual records (@ and www to this server, SPF, CAA for Let's Encrypt)</label></div>
                    <button class="btn btn-primary btn-sm">Create zone</button>
                    <div class="form-text">Name servers: <?= e(implode(', ', $nameservers)) ?>. After creating the zone, set these name servers for the domain at its registrar.</div>
                </form>
    <?= $this->modalEnd() ?>
<?php endif; ?>
