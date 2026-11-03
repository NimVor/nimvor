<?php
/**
 * Server → Nginx.
 *
 * @var \NimVor\View\View $this
 * @var array{installed: bool, running: bool, config_ok: bool, config_error: string, tuning: bool}|null $status
 * @var array<string, mixed> $tuning
 */
use NimVor\Security\Permissions as P;

$status ??= null;
$canManage = $this->can(P::SERVER_MANAGE);
$canReload = $this->can(P::SERVICES_MANAGE);
?>
<?php $this->start(); ?>Nginx<?php $this->end('title'); ?>
<?php $this->start(); ?>Web server state, configuration test and server-wide tuning<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
    <?php if ($status !== null && $status['installed']): ?>
        <form method="post" action="<?= e($this->url('nginx.test')) ?>" data-nim-busy class="d-inline"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-clipboard-check me-1"></i>Test config</button></form>
        <?php if ($canReload): ?><form method="post" action="<?= e($this->url('nginx.reload')) ?>" data-nim-busy class="d-inline"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-clockwise me-1"></i>Reload</button></form><?php endif; ?>
    <?php endif; ?>
<?php $this->end('actions'); ?>

<?php if ($status === null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError ?? null]) ?>
    <?php return; ?>
<?php endif; ?>

<?php if (!$status['installed']): ?>
    <div class="card"><div class="card-body small text-body-secondary">Nginx is not installed on this server.</div></div>
    <?php return; ?>
<?php endif; ?>

<div class="nim-infobar <?= $status['config_ok'] ? '' : 'is-error' ?> mb-3">
    <i class="bi bi-<?= $status['config_ok'] ? 'check-circle' : 'exclamation-triangle' ?>" aria-hidden="true"></i>
    <span><?= $status['running'] ? 'Running' : 'Stopped' ?> · configuration <?= $status['config_ok'] ? 'valid' : 'invalid' ?><?= !$status['config_ok'] && $status['config_error'] !== '' ? ': ' . e($status['config_error']) : '' ?></span>
</div>

<div class="card">
    <div class="card-header">Tuning</div>
    <div class="card-body">
        <p class="small text-body-secondary">These server-wide settings are written to a managed drop-in in <code>conf.d</code> and validated with <code>nginx -t</code>; if a value would make the configuration invalid it is rolled back automatically. Per-website settings are unaffected.</p>
        <form method="post" action="<?= e($this->url('nginx.tuning')) ?>" data-nim-busy>
            <?= $this->csrfField() ?>
            <div class="row g-3">
                <div class="col-sm-6 col-lg-3"><?= $this->partial('partials/field', ['name' => 'client_max_body_mb', 'label' => 'Max upload size (MB)', 'type' => 'number', 'value' => (string) $tuning['client_max_body_mb'], 'attrs' => ['min' => '1', 'max' => '4096', 'disabled' => $canManage ? null : 'disabled']]) ?></div>
                <div class="col-sm-6 col-lg-3"><?= $this->partial('partials/field', ['name' => 'keepalive_timeout', 'label' => 'Keep-alive timeout (s)', 'type' => 'number', 'value' => (string) $tuning['keepalive_timeout'], 'attrs' => ['min' => '1', 'max' => '3600', 'disabled' => $canManage ? null : 'disabled']]) ?></div>
                <div class="col-sm-6 col-lg-3"><?= $this->partial('partials/field', ['name' => 'client_body_timeout', 'label' => 'Request body timeout (s)', 'type' => 'number', 'value' => (string) $tuning['client_body_timeout'], 'attrs' => ['min' => '1', 'max' => '3600', 'disabled' => $canManage ? null : 'disabled']]) ?></div>
                <div class="col-sm-6 col-lg-3"><?= $this->partial('partials/field', ['name' => 'send_timeout', 'label' => 'Send timeout (s)', 'type' => 'number', 'value' => (string) $tuning['send_timeout'], 'attrs' => ['min' => '1', 'max' => '3600', 'disabled' => $canManage ? null : 'disabled']]) ?></div>
            </div>
            <div class="form-check form-switch mt-2"><input type="hidden" name="gzip" value="0"><input class="form-check-input" type="checkbox" role="switch" id="ng-gzip" name="gzip" value="1"<?= $tuning['gzip'] ? ' checked' : '' ?><?= $canManage ? '' : ' disabled' ?>><label class="form-check-label" for="ng-gzip">Gzip compression</label></div>
            <?php $bs = (string) ($status['brotli']['state'] ?? 'missing'); $bOk = $bs === 'functional'; $bCfg = !empty($status['brotli']['in_config']); ?>
            <div class="form-check form-switch"><input type="hidden" name="brotli" value="0"><input class="form-check-input" type="checkbox" role="switch" id="ng-brotli" name="brotli" value="1"<?= $tuning['brotli'] ? ' checked' : '' ?><?= $canManage ? '' : ' disabled' ?>><label class="form-check-label" for="ng-brotli">Brotli compression <span class="text-body-secondary">(smaller pages than gzip for browsers that support it; gzip stays for the others; a website can turn it off on its Headers &amp; cache page)</span></label></div>
            <?php if ($bCfg && !$bOk): ?>
                <div class="form-text ms-5 mb-1 text-warning"><i class="bi bi-exclamation-triangle me-1" aria-hidden="true"></i>Brotli is in the Nginx configuration but its module is not loaded, so Nginx will refuse to start or reload. Install the module on the <a href="<?= e($this->url('packages')) ?>">Packages</a> page, or turn Brotli off here and press Apply.</div>
            <?php elseif ($tuning['brotli'] && !$bOk): ?>
                <div class="form-text ms-5 mb-1">Turned on, but not active yet: the Nginx Brotli module is <?= $bs === 'installed' ? 'installed but not loaded' : 'not installed' ?>. Install it on the <a href="<?= e($this->url('packages')) ?>">Packages</a> page (“Brotli compression for Nginx”), then press Apply.</div>
            <?php elseif ($tuning['brotli'] && $bOk && !$bCfg): ?>
                <div class="form-text ms-5 mb-1">The module is installed. Press Apply to start using Brotli.</div>
            <?php elseif ($tuning['brotli'] && $bOk): ?>
                <div class="form-text ms-5 mb-1 text-success"><i class="bi bi-check-circle me-1" aria-hidden="true"></i>Active.</div>
            <?php endif; ?>
            <div class="form-check form-switch"><input type="hidden" name="hide_version" value="0"><input class="form-check-input" type="checkbox" role="switch" id="ng-tok" name="hide_version" value="1"<?= $tuning['hide_version'] ? ' checked' : '' ?><?= $canManage ? '' : ' disabled' ?>><label class="form-check-label" for="ng-tok">Hide Nginx version (server_tokens off)</label></div>
            <?php if ($canManage): ?><button class="btn btn-primary btn-sm mt-3" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Apply</button><?php endif; ?>
        </form>
    </div>
</div>
