<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $site */
use NimVor\Security\Permissions as P;
use NimVor\View\SiteMenu;

$id = (int) $site['id'];
$u = fn (string $key, string $hash = ''): ?string => ($url = SiteMenu::url($this, $site, $key)) === null ? null : $url . $hash;
?>
<?php if (empty($site['enabled']) && !$this->mayToggleSite()): ?>
    <div class="alert alert-warning" id="site-disabled" role="alert"><i class="bi bi-pause-circle me-1" aria-hidden="true"></i><strong>This website is disabled by the administrator.</strong> Visitors cannot reach it, and it cannot be enabled or deleted from your account. Your files, databases and settings are kept. Contact your hosting provider to have it enabled.</div>
<?php endif; ?>

<?php if ($site['status'] === 'error' && $site['last_error'] !== ''): ?>
    <div class="alert alert-danger small"><strong>The last change failed:</strong> <?= e($site['last_error']) ?><?php if ($u('advanced') !== null): ?> · <a href="<?= e($u('advanced', '#danger-zone')) ?>">Re-apply configuration</a><?php endif; ?></div>
<?php endif; ?>
<?php if (!empty($site['bandwidth_blocked'])): ?>
    <div class="alert alert-danger small" id="bandwidth-blocked"><i class="bi bi-speedometer me-1"></i><strong>Monthly bandwidth used up.</strong> This website answers “509 Bandwidth limit exceeded” until the month ends or the hosting plan allows more<?= $this->serverScope() ? ' (or you <a href="' . e($this->url('accounts.bandwidth', [], ['show' => 'all'])) . '">reset the counter</a>)' : '' ?>.</div>
<?php endif; ?>
<?php if ($inspectError !== null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $inspectError]) ?>
<?php elseif ($inspect !== null && !$inspect['config_exists']): ?>
    <div class="alert alert-warning small"><i class="bi bi-exclamation-triangle me-1"></i>This website's configuration is missing on the server.<?php if ($u('advanced') !== null): ?> <a href="<?= e($u('advanced', '#danger-zone')) ?>">Re-apply the configuration</a> to recreate it.<?php endif; ?></div>
<?php endif; ?>

<?php if (!empty($import)): ?>
    <?php if ($import['status'] === 'running'): ?>
        <div class="alert alert-info small"><span class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>Importing from <code><?= e($import['source']) ?></code>. The old website keeps serving visitors until the switch. Reload this page to see the result.</div>
    <?php elseif ($import['status'] === 'succeeded'): ?>
        <div class="alert alert-secondary small d-flex flex-wrap align-items-center gap-2">
            <span class="me-auto"><i class="bi bi-box-arrow-in-down me-1"></i>Imported from <code><?= e($import['source']) ?></code> on <?= e(substr((string) $import['created_at'], 0, 10)) ?>. The original files and configuration are kept.</span>
            <?php if ($this->can(P::IMPORT_RUN)): ?>
                <form method="post" action="<?= e($this->url('sites.import.undo', ['id' => $id])) ?>" data-nim-confirm="Undo the import? The original configuration will serve <?= e($site['domain']) ?> again, and the NimVor copy (files in <?= e($site['root']) ?>, its account and settings) will be deleted. Changes made since the import are lost.">
                    <?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger">Undo import</button>
                </form>
            <?php endif; ?>
        </div>
    <?php endif; ?>
<?php endif; ?>
