<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $info */
use WhaCP\Services\RedisService;

$i = (array) ($info['info'] ?? []);
$server = (array) ($i['server'] ?? []);
$mem = (array) ($i['memory'] ?? []);
$clients = (array) ($i['clients'] ?? []);
$keyspace = (array) ($i['keyspace'] ?? []);
$stats = (array) ($i['stats'] ?? []);
$maxmem = (int) ($info['maxmemory'] ?? 0);
$policy = (string) ($info['maxmemory_policy'] ?? '');
$users = array_values(array_filter((array) ($info['acl_users'] ?? []), static fn ($u) => $u !== 'default'));
$hits = (int) ($stats['keyspace_hits'] ?? 0);
$misses = (int) ($stats['keyspace_misses'] ?? 0);
?>
<?php $this->start(); ?>Redis<?php $this->end('title'); ?>
<?php $this->start(); ?>In-memory cache and queue server<?php $this->end('subtitle'); ?>
<?php $this->start(); ?><?php if ($agentError === null): ?><a class="btn btn-sm btn-primary" href="<?= e($this->url('redis.browser')) ?>"><i class="bi bi-search me-1"></i>Browse keys</a><?php endif; ?><?php $this->end('actions'); ?>
<?= $this->partial('partials/secret', ['secret' => $secret]) ?>
<?php if ($agentError !== null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError]) ?>
    <p class="text-body-secondary small">Install Redis (<code>redis-server</code> on Ubuntu, <code>redis</code> on RHEL 9, <code>valkey</code> on RHEL 10)<?php if ($this->hasRoute('packages')): ?> from <a href="<?= e($this->url('packages')) ?>">Packages</a><?php endif; ?> to use it with your websites.</p>
<?php else: ?>
<ul class="nav nav-tabs wcp-tabs mb-3" role="tablist">
    <li class="nav-item" role="presentation"><button class="nav-link active" id="tab-overview" data-bs-toggle="tab" data-bs-target="#pane-overview" type="button" role="tab" aria-controls="pane-overview" aria-selected="true"><i class="bi bi-lightning me-1" aria-hidden="true"></i>Overview</button></li>
    <li class="nav-item" role="presentation"><button class="nav-link" id="tab-users" data-bs-toggle="tab" data-bs-target="#pane-users" type="button" role="tab" aria-controls="pane-users" aria-selected="false"><i class="bi bi-people me-1" aria-hidden="true"></i>Website users<?php if ($users !== []): ?> <span class="badge rounded-pill text-bg-light border ms-1"><?= count($users) ?></span><?php endif; ?></button></li>
    <li class="nav-item" role="presentation"><button class="nav-link" id="tab-security" data-bs-toggle="tab" data-bs-target="#pane-security" type="button" role="tab" aria-controls="pane-security" aria-selected="false"><i class="bi bi-key me-1" aria-hidden="true"></i>Security</button></li>
    <li class="nav-item" role="presentation"><button class="nav-link" id="tab-danger" data-bs-toggle="tab" data-bs-target="#pane-danger" type="button" role="tab" aria-controls="pane-danger" aria-selected="false"><i class="bi bi-exclamation-triangle me-1" aria-hidden="true"></i>Danger zone</button></li>
</ul>
<div class="tab-content">
    <section class="tab-pane fade show active" id="pane-overview" role="tabpanel" aria-labelledby="tab-overview" tabindex="0">
        <div class="card">
            <div class="card-header d-flex align-items-center"><i class="bi bi-lightning me-1"></i>Status
                <button class="btn btn-sm btn-outline-primary ms-auto" type="button" data-bs-toggle="modal" data-bs-target="#redis-config"><i class="bi bi-sliders me-1" aria-hidden="true"></i>Memory &amp; eviction</button>
            </div>
            <div class="card-body">
                <dl class="wcp-dl mb-0">
                    <dt>Version</dt><dd><?= e($server['redis_version'] ?? '') ?></dd>
                    <dt>Uptime</dt><dd><?= e(number_format(((int) ($server['uptime_in_seconds'] ?? 0)) / 86400, 1)) ?> days</dd>
                    <dt>Memory used</dt><dd><?= e($mem['used_memory_human'] ?? '') ?><?= $maxmem > 0 ? ' of ' . e(format_bytes($maxmem)) : ' (no limit)' ?></dd>
                    <dt>Eviction</dt><dd><code><?= e($policy) ?></code></dd>
                    <dt>Clients</dt><dd><?= (int) ($clients['connected_clients'] ?? 0) ?></dd>
                    <dt>Hit rate</dt><dd><?= $hits + $misses > 0 ? e(number_format(100 * $hits / ($hits + $misses), 1)) . ' %' : '—' ?></dd>
                    <dt>Keys</dt><dd class="small"><?php if ($keyspace === []): ?>none<?php else: foreach ($keyspace as $db => $v): ?><div><code><?= e($db) ?></code> <?= e($v) ?></div><?php endforeach; endif; ?></dd>
                </dl>
            </div>
        </div>
    </section>
    <section class="tab-pane fade" id="pane-users" role="tabpanel" aria-labelledby="tab-users" tabindex="0">
        <div class="card">
            <div class="card-header d-flex align-items-center flex-wrap gap-2"><i class="bi bi-people me-1"></i>Website users
                <?php if ($websites !== []): ?>
                <button class="btn btn-sm btn-primary ms-auto" type="button" data-bs-toggle="modal" data-bs-target="#redis-user"><i class="bi bi-person-plus me-1" aria-hidden="true"></i>Create / reset user</button>
                <?php endif; ?>
            </div>
            <div class="card-body">
                <p class="small text-body-secondary">A website user can only read and write keys that start with its prefix, and cannot run administrative commands. Use it to keep websites apart on a shared Redis.</p>
                <?php if ($users !== []): ?>
                    <input type="search" class="form-control form-control-sm mb-2" placeholder="Filter users…" aria-label="Filter users" data-wcp-filter="#redis-users-table">
                    <div class="table-responsive">
                        <table class="table table-sm align-middle mb-0" id="redis-users-table">
                            <thead><tr><th>User</th><th>Allowed keys</th><th class="text-end">Actions</th></tr></thead>
                            <tbody>
                                <?php foreach ($users as $u): ?>
                                    <tr>
                                        <td><code><?= e($u) ?></code></td>
                                        <td class="small text-body-secondary"><?= e($u) ?>:*</td>
                                        <td class="text-end">
                                            <form method="post" action="<?= e($this->url('redis.users.delete')) ?>" data-wcp-confirm="Remove Redis user <?= e($u) ?>? Applications using it lose access."><?= $this->csrfField() ?><input type="hidden" name="user" value="<?= e($u) ?>"><button class="btn btn-sm btn-outline-danger"><i class="bi bi-trash"></i></button></form>
                                        </td>
                                    </tr>
                                <?php endforeach; ?>
                                <tr data-wcp-filter-empty hidden><td colspan="3" class="text-center text-body-secondary small py-3">No users match.</td></tr>
                            </tbody>
                        </table>
                    </div>
                <?php else: ?>
                    <p class="text-body-secondary small mb-0">No website users yet.</p>
                <?php endif; ?>
            </div>
        </div>
    </section>
    <section class="tab-pane fade" id="pane-security" role="tabpanel" aria-labelledby="tab-security" tabindex="0">
        <div class="card">
            <div class="card-header"><i class="bi bi-key me-1"></i>Default-user password</div>
            <div class="card-body">
                <p class="small text-body-secondary">Redis only listens locally, but a password on the default user is recommended. Applications that connect without a user name must be updated when it changes.</p>
                <div class="d-flex flex-wrap gap-2">
                    <form method="post" action="<?= e($this->url('redis.password')) ?>" data-wcp-confirm="Set a new random password for the default Redis user? Applications that connect without a user name must be updated."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-primary">Generate new password</button></form>
                    <form method="post" action="<?= e($this->url('redis.password')) ?>" data-wcp-confirm="Remove the password of the default Redis user?"><?= $this->csrfField() ?><input type="hidden" name="clear" value="1"><button class="btn btn-sm btn-outline-secondary">Remove password</button></form>
                </div>
            </div>
        </div>
    </section>
    <section class="tab-pane fade" id="pane-danger" role="tabpanel" aria-labelledby="tab-danger" tabindex="0">
        <div class="card border-danger-subtle">
            <div class="card-header text-danger-emphasis"><i class="bi bi-eraser me-1"></i>Flush a database</div>
            <div class="card-body">
                <p class="small text-body-secondary">Permanently removes every key in the selected database. This cannot be undone.</p>
                <form class="d-flex gap-2 align-items-end flex-wrap" method="post" action="<?= e($this->url('redis.flush')) ?>">
                    <?= $this->csrfField() ?>
                    <div><label class="form-label small" for="r-db">Database</label><input class="form-control form-control-sm" id="r-db" type="number" name="db" min="0" max="255" value="0"></div>
                    <div class="flex-grow-1"><label class="form-label small" for="r-confirm">Type db&lt;number&gt; to confirm</label><input class="form-control form-control-sm" id="r-confirm" name="confirm" placeholder="db0" autocomplete="off"></div>
                    <button class="btn btn-sm btn-outline-danger">Delete all keys</button>
                </form>
            </div>
        </div>
    </section>
</div>

<?= $this->modalStart('redis-config', 'Memory &amp; eviction') ?>
<form method="post" action="<?= e($this->url('redis.configure')) ?>" data-wcp-busy>
    <?= $this->csrfField() ?>
    <?= $this->partial('partials/field', ['name' => 'maxmemory_mb', 'label' => 'Memory limit (MB, 0 = no limit)', 'type' => 'number', 'value' => (string) intdiv($maxmem, 1048576), 'attrs' => ['min' => '0']]) ?>
    <label class="form-label" for="r-policy">When the limit is reached</label>
    <select class="form-select mb-3" id="r-policy" name="policy">
        <?php foreach (RedisService::POLICIES as $k => $label): ?><option value="<?= e($k) ?>"<?= $policy === $k ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
    </select>
    <div class="text-end"><button class="btn btn-primary">Save</button></div>
</form>
<?= $this->modalEnd() ?>

<?php if ($websites !== []): ?>
<?= $this->modalStart('redis-user', 'Create / reset website user', false, 'modal') ?>
<form method="post" action="<?= e($this->url('redis.users.store')) ?>">
    <?= $this->csrfField() ?>
    <label class="form-label" for="r-website">Website</label>
    <select class="form-select mb-3" id="r-website" name="website_id" aria-label="Website">
        <?php foreach ($websites as $w): ?><option value="<?= (int) $w['id'] ?>"><?= e($w['domain']) ?> (<?= e($w['system_user']) ?>)</option><?php endforeach; ?>
    </select>
    <p class="small text-body-secondary">The user can only touch keys that start with its own prefix. Running this again resets the user's password.</p>
    <div class="text-end"><button class="btn btn-primary">Create / reset user</button></div>
</form>
<?= $this->modalEnd() ?>
<?php endif; ?>
<?php endif; ?>
