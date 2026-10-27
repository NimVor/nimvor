<?php
/**
 * Server → Cloudflare.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed>|null $account
 * @var list<array<string, mixed>> $zones
 * @var array<string, mixed>|null $zone
 * @var array<string, string> $sslModes
 */
use NimVor\Security\Permissions as P;

$account ??= null;
$zones ??= [];
$zone ??= null;
$canManage = $this->can(P::SERVER_MANAGE);
?>
<?php $this->start(); ?>Cloudflare<?php $this->end('title'); ?>
<?php $this->start(); ?>CDN and DNS through your Cloudflare account (free plan)<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
    <?php if ($account !== null && $canManage): ?>
        <form method="post" action="<?= e($this->url('cloudflare.disconnect')) ?>" data-nim-confirm="Disconnect Cloudflare? The stored API token is deleted.">
            <?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger"><i class="bi bi-x-circle me-1"></i>Disconnect</button>
        </form>
    <?php endif; ?>
<?php $this->end('actions'); ?>

<?php if (($cfError ?? null) !== null): ?>
    <div class="nim-infobar is-error mb-3"><i class="bi bi-exclamation-triangle" aria-hidden="true"></i><span><?= e((string) $cfError) ?></span></div>
<?php endif; ?>

<?php if ($account === null): ?>
    <div class="card">
        <div class="card-header">Connect Cloudflare</div>
        <div class="card-body">
            <p class="small text-body-secondary">Create an API token in your Cloudflare dashboard (My Profile → API Tokens) with the <strong>Zone.Zone</strong>, <strong>Zone.DNS</strong>, <strong>Zone.Zone Settings</strong> and <strong>Zone.Cache Purge</strong> permissions, then paste it here. It is stored encrypted and used only for your zones.</p>
            <?php if ($canManage): ?>
            <form method="post" action="<?= e($this->url('cloudflare.connect')) ?>" data-nim-busy>
                <?= $this->csrfField() ?>
                <label class="form-label" for="cf-token">API token</label>
                <input class="form-control font-monospace" id="cf-token" name="token" type="password" autocomplete="off" required>
                <button class="btn btn-primary btn-sm mt-3" type="submit">Connect</button>
            </form>
            <?php else: ?><div class="small text-body-secondary">Ask an administrator to connect Cloudflare.</div><?php endif; ?>
        </div>
    </div>
    <?php return; ?>
<?php endif; ?>

<div class="card mb-3">
    <div class="card-header">Zones</div>
    <?php if ($zones === []): ?>
        <div class="card-body small text-body-secondary">No zones on this Cloudflare account yet. Add your domain in the Cloudflare dashboard, then it appears here.</div>
    <?php else: ?>
        <div class="nim-dtable table-responsive">
            <table class="table align-middle mb-0">
                <thead><tr><th>Domain</th><th>Status</th><th class="text-end"></th></tr></thead>
                <tbody>
                <?php foreach ($zones as $z): ?>
                    <tr>
                        <td class="font-monospace"><?= e((string) $z['name']) ?></td>
                        <td><span class="nim-state <?= $z['status'] === 'active' ? 'is-ok' : 'is-warn' ?>"><?= e((string) $z['status']) ?></span></td>
                        <td class="text-end"><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('cloudflare', [], ['zone' => (string) $z['id']])) ?>">Manage</a></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
</div>

<?php if ($zone !== null): ?>
<div class="card" id="cf-zone">
    <div class="card-header d-flex flex-wrap gap-2 align-items-center">
        <span class="fw-medium me-2"><?= e((string) $zone['name']) ?></span>
        <span class="nim-state <?= $zone['status'] === 'active' ? 'is-ok' : 'is-warn' ?>"><?= e((string) $zone['status']) ?></span>
        <?php if ($canManage): ?>
        <form class="ms-auto" method="post" action="<?= e($this->url('cloudflare.purge')) ?>" data-nim-busy data-nim-confirm="Purge the entire cache for <?= e((string) $zone['name']) ?>?">
            <?= $this->csrfField() ?><input type="hidden" name="zone" value="<?= e((string) $zone['id']) ?>">
            <button class="btn btn-sm btn-outline-secondary"><i class="bi bi-trash3 me-1"></i>Purge cache</button>
        </form>
        <?php endif; ?>
    </div>
    <?php if ($zone['status'] !== 'active'): ?>
        <div class="card-body small">
            This zone is pending. Point the domain's nameservers at Cloudflare to activate it:
            <ul class="mb-0 mt-1"><?php foreach ((array) $zone['name_servers'] as $ns): ?><li class="font-monospace"><?= e((string) $ns) ?></li><?php endforeach; ?></ul>
        </div>
    <?php endif; ?>
    <div class="card-body border-top">
        <div class="row g-3">
            <div class="col-md-6">
                <form method="post" action="<?= e($this->url('cloudflare.ssl')) ?>" data-nim-busy class="d-flex gap-2 align-items-end">
                    <?= $this->csrfField() ?><input type="hidden" name="zone" value="<?= e((string) $zone['id']) ?>">
                    <div class="flex-grow-1">
                        <label class="form-label" for="cf-ssl">SSL mode</label>
                        <select class="form-select" id="cf-ssl" name="mode"<?= $canManage ? '' : ' disabled' ?>>
                            <?php foreach ($sslModes as $k => $label): ?><option value="<?= e($k) ?>"<?= ($zone['ssl'] ?? '') === $k ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                        </select>
                    </div>
                    <?php if ($canManage): ?><button class="btn btn-sm btn-primary">Save</button><?php endif; ?>
                </form>
            </div>
            <div class="col-md-6 d-flex align-items-end">
                <?php if ($canManage): ?>
                <form method="post" action="<?= e($this->url('cloudflare.always_https')) ?>" data-nim-busy>
                    <?= $this->csrfField() ?><input type="hidden" name="zone" value="<?= e((string) $zone['id']) ?>">
                    <input type="hidden" name="on" value="<?= $zone['always_https'] ? '0' : '1' ?>">
                    <div class="form-check form-switch">
                        <input class="form-check-input" type="checkbox" role="switch" id="cf-ah" onchange="this.form.submit()"<?= $zone['always_https'] ? ' checked' : '' ?>>
                        <label class="form-check-label" for="cf-ah">Always Use HTTPS</label>
                    </div>
                </form>
                <?php else: ?><span class="small text-body-secondary">Always Use HTTPS: <?= $zone['always_https'] ? 'on' : 'off' ?></span><?php endif; ?>
            </div>
        </div>
    </div>
    <div class="card-header border-top">DNS records</div>
    <div class="nim-dtable table-responsive">
        <table class="table align-middle mb-0">
            <thead><tr><th>Type</th><th>Name</th><th>Content</th><th>Proxy</th></tr></thead>
            <tbody>
            <?php foreach ((array) $zone['records'] as $rec): ?>
                <tr>
                    <td class="small fw-medium"><?= e((string) $rec['type']) ?></td>
                    <td class="small font-monospace text-break"><?= e((string) $rec['name']) ?></td>
                    <td class="small font-monospace text-break text-body-secondary"><?= e((string) $rec['content']) ?></td>
                    <td>
                        <?php if (!$rec['proxiable']): ?><span class="small text-body-secondary">—</span>
                        <?php elseif ($canManage): ?>
                        <form method="post" action="<?= e($this->url('cloudflare.proxy')) ?>" data-nim-busy>
                            <?= $this->csrfField() ?>
                            <input type="hidden" name="zone" value="<?= e((string) $zone['id']) ?>">
                            <input type="hidden" name="record" value="<?= e((string) $rec['id']) ?>">
                            <input type="hidden" name="proxied" value="<?= $rec['proxied'] ? '0' : '1' ?>">
                            <div class="form-check form-switch mb-0">
                                <input class="form-check-input" type="checkbox" role="switch" onchange="this.form.submit()"<?= $rec['proxied'] ? ' checked' : '' ?> title="<?= $rec['proxied'] ? 'Proxied (orange cloud)' : 'DNS only' ?>">
                            </div>
                        </form>
                        <?php else: ?><span class="nim-state <?= $rec['proxied'] ? 'is-ok' : '' ?>"><?= $rec['proxied'] ? 'Proxied' : 'DNS only' ?></span><?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
</div>
<?php endif; ?>
