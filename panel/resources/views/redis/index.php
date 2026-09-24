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
$users = array_values(array_filter((array) ($info['acl_users'] ?? []), static fn ($u) => $u !== 'default'));
$hits = (int) ($stats['keyspace_hits'] ?? 0);
$misses = (int) ($stats['keyspace_misses'] ?? 0);
?>
<?php $this->start(); ?>Redis<?php $this->end('title'); ?>
<?php $this->start(); ?>In-memory cache and queue server<?php $this->end('subtitle'); ?>
<?= $this->partial('partials/secret', ['secret' => $secret]) ?>
<?php if ($agentError !== null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError]) ?>
    <p class="text-body-secondary small">Install Redis with <code>apt install redis-server</code> to use it with your websites.</p>
<?php else: ?>
<div class="row g-3">
    <div class="col-lg-6">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-lightning me-1"></i>Status</div>
            <div class="card-body">
                <dl class="wcp-dl mb-0">
                    <dt>Version</dt><dd><?= e($server['redis_version'] ?? '') ?></dd>
                    <dt>Uptime</dt><dd><?= e(number_format(((int) ($server['uptime_in_seconds'] ?? 0)) / 86400, 1)) ?> days</dd>
                    <dt>Memory used</dt><dd><?= e($mem['used_memory_human'] ?? '') ?><?= $maxmem > 0 ? ' of ' . e(format_bytes($maxmem)) : ' (no limit)' ?></dd>
                    <dt>Eviction</dt><dd><code><?= e($info['maxmemory_policy'] ?? '') ?></code></dd>
                    <dt>Clients</dt><dd><?= (int) ($clients['connected_clients'] ?? 0) ?></dd>
                    <dt>Hit rate</dt><dd><?= $hits + $misses > 0 ? e(number_format(100 * $hits / ($hits + $misses), 1)) . ' %' : '—' ?></dd>
                    <dt>Keys</dt><dd class="small"><?php if ($keyspace === []): ?>none<?php else: foreach ($keyspace as $db => $v): ?><div><code><?= e($db) ?></code> <?= e($v) ?></div><?php endforeach; endif; ?></dd>
                </dl>
            </div>
        </div>
        <div class="card">
            <div class="card-header"><i class="bi bi-sliders me-1"></i>Memory</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('redis.configure')) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <?= $this->partial('partials/field', ['name' => 'maxmemory_mb', 'label' => 'Memory limit (MB, 0 = no limit)', 'type' => 'number', 'value' => (string) intdiv($maxmem, 1048576), 'attrs' => ['min' => '0']]) ?>
                    <label class="form-label" for="r-policy">When the limit is reached</label>
                    <select class="form-select mb-3" id="r-policy" name="policy">
                        <?php foreach (RedisService::POLICIES as $k => $label): ?><option value="<?= e($k) ?>"<?= ($info['maxmemory_policy'] ?? '') === $k ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                    </select>
                    <button class="btn btn-sm btn-primary">Save</button>
                </form>
            </div>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-people me-1"></i>Website users</div>
            <div class="card-body">
                <p class="small text-body-secondary">A website user can only read and write keys that start with its prefix, and cannot run administrative commands. Use it to keep websites apart on a shared Redis.</p>
                <?php if ($users !== []): ?>
                    <ul class="list-group list-group-flush mb-3">
                        <?php foreach ($users as $u): ?>
                            <li class="list-group-item d-flex align-items-center px-0"><code><?= e($u) ?></code> <span class="small text-body-secondary ms-2">keys <?= e($u) ?>:*</span>
                                <form class="ms-auto" method="post" action="<?= e($this->url('redis.users.delete')) ?>" data-wcp-confirm="Remove Redis user <?= e($u) ?>? Applications using it lose access."><?= $this->csrfField() ?><input type="hidden" name="user" value="<?= e($u) ?>"><button class="btn btn-sm btn-outline-danger"><i class="bi bi-trash"></i></button></form>
                            </li>
                        <?php endforeach; ?>
                    </ul>
                <?php endif; ?>
                <?php if ($websites !== []): ?>
                    <form class="d-flex gap-2" method="post" action="<?= e($this->url('redis.users.store')) ?>">
                        <?= $this->csrfField() ?>
                        <select class="form-select form-select-sm" name="website_id" aria-label="Website">
                            <?php foreach ($websites as $w): ?><option value="<?= (int) $w['id'] ?>"><?= e($w['domain']) ?> (<?= e($w['system_user']) ?>)</option><?php endforeach; ?>
                        </select>
                        <button class="btn btn-sm btn-outline-primary text-nowrap">Create / reset user</button>
                    </form>
                <?php endif; ?>
            </div>
        </div>
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-key me-1"></i>Password</div>
            <div class="card-body d-flex flex-wrap gap-2">
                <form method="post" action="<?= e($this->url('redis.password')) ?>" data-wcp-confirm="Set a new random password for the default Redis user? Applications that connect without a user name must be updated."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-primary">Generate new password</button></form>
                <form method="post" action="<?= e($this->url('redis.password')) ?>" data-wcp-confirm="Remove the password of the default Redis user?"><?= $this->csrfField() ?><input type="hidden" name="clear" value="1"><button class="btn btn-sm btn-outline-secondary">Remove password</button></form>
            </div>
        </div>
        <div class="card border-danger-subtle">
            <div class="card-header"><i class="bi bi-eraser me-1"></i>Flush a database</div>
            <div class="card-body">
                <form class="d-flex gap-2 align-items-end" method="post" action="<?= e($this->url('redis.flush')) ?>">
                    <?= $this->csrfField() ?>
                    <div><label class="form-label small" for="r-db">Database</label><input class="form-control form-control-sm" id="r-db" type="number" name="db" min="0" max="255" value="0"></div>
                    <div class="flex-grow-1"><label class="form-label small" for="r-confirm">Type db&lt;number&gt; to confirm</label><input class="form-control form-control-sm" id="r-confirm" name="confirm" placeholder="db0" autocomplete="off"></div>
                    <button class="btn btn-sm btn-outline-danger">Delete all keys</button>
                </form>
            </div>
        </div>
    </div>
</div>
<?php endif; ?>
