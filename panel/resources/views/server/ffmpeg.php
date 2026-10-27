<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $status */
/** @var list<string> $allowed */
/** @var list<array<string, mixed>> $plans */
use WhaCP\Security\Permissions as P;

$canPackages = $this->can(P::PACKAGES_MANAGE);
$canManage = $this->can(P::SERVER_MANAGE);
?>
<?php $this->start(); ?>FFmpeg<?php $this->end('title'); ?>
<?php $this->start(); ?>Audio and video processing for the hosting plans that include it<?php $this->end('subtitle'); ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php return; endif; ?>
<?php
$members = array_map('strval', (array) ($status['members'] ?? []));
sort($members);
$inSync = $members === $allowed && !empty($status['restricted']);
?>
<div class="row g-3">
    <div class="col-xl-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-film me-1"></i>Installation</div>
            <div class="card-body">
                <dl class="row small mb-3">
                    <dt class="col-sm-4">State</dt><dd class="col-sm-8"><?= !empty($status['installed']) ? '<span class="wcp-state is-ok">Installed</span>' : '<span class="wcp-state">Not installed</span>' ?></dd>
                    <?php if (!empty($status['version'])): ?><dt class="col-sm-4">Version</dt><dd class="col-sm-8 text-break"><?= e((string) $status['version']) ?></dd><?php endif; ?>
                    <dt class="col-sm-4">Package</dt><dd class="col-sm-8"><code><?= e((string) ($status['package'] ?? 'ffmpeg')) ?></code><?= ($status['package'] ?? '') === 'ffmpeg-free' ? ' <span class="text-body-secondary">(EPEL; without patent-encumbered codecs such as H.264 and AAC encoding)</span>' : '' ?></dd>
                    <?php foreach ((array) ($status['binaries'] ?? []) as $b): if (empty($b['present'])) { continue; } ?>
                        <dt class="col-sm-4 font-monospace fw-normal"><?= e((string) $b['path']) ?></dt><dd class="col-sm-8"><code><?= e((string) $b['mode']) ?></code> <?= !empty($b['restricted']) ? '<span class="wcp-state is-ok">group only</span>' : '<span class="wcp-state is-warn">everyone</span>' ?></dd>
                    <?php endforeach; ?>
                </dl>
                <?php if ($canPackages): ?>
                    <div class="d-flex flex-wrap gap-2">
                        <form method="post" action="<?= e($this->url('ffmpeg.install')) ?>" data-wcp-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= !empty($status['installed']) ? 'Update FFmpeg' : 'Install FFmpeg' ?></button></form>
                        <?php if (!empty($status['installed'])): ?>
                            <form method="post" action="<?= e($this->url('ffmpeg.uninstall')) ?>" data-wcp-confirm="Uninstall FFmpeg? Websites and cron jobs that convert audio or video stop working. Nothing is removed when other packages need it."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger">Uninstall</button></form>
                        <?php endif; ?>
                    </div>
                <?php endif; ?>
            </div>
        </div>
    </div>
    <div class="col-xl-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-person-check me-1"></i>Access</div>
            <div class="card-body small">
                <p>Only the accounts in the group <code><?= e((string) ($status['group'] ?? 'whacp-ffmpeg')) ?></code> can run the server's FFmpeg: the website accounts of administrators and of hosting plans that include FFmpeg (for a reseller's customers, the reseller's plan must include it too). Suspended accounts lose access. Changes apply within five minutes, at once when a plan is saved; PHP is reloaded for accounts that lose access.</p>
                <?php if (!empty($status['installed'])): ?>
                    <p class="mb-2"><span class="fw-medium"><?= count($allowed) ?></span> account(s) may use FFmpeg <?= $inSync ? '<span class="wcp-state is-ok">applied</span>' : '<span class="wcp-state is-warn">not applied yet</span>' ?></p>
                    <?php if ($allowed !== []): ?><div class="font-monospace text-break mb-3"><?= e(implode(', ', array_slice($allowed, 0, 50))) ?><?= count($allowed) > 50 ? ' …' : '' ?></div><?php endif; ?>
                    <?php if ($canManage): ?><form method="post" action="<?= e($this->url('ffmpeg.apply')) ?>" data-wcp-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-repeat me-1"></i>Apply now</button></form><?php endif; ?>
                <?php endif; ?>
                <p class="text-body-secondary mt-3 mb-0">This controls the FFmpeg installed on the server. A user could still upload a program of their own into their website folder; WhaCP does not prevent running uploaded programs.</p>
            </div>
        </div>
    </div>
</div>
<div class="card mt-3">
    <div class="card-header wcp-toolbar"><h2 class="wcp-toolbar-title"><i class="bi bi-card-checklist me-1"></i>Plans</h2><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('plans')) ?>">Edit plans</a></div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle mb-0">
            <thead><tr><th>Plan</th><th>Owner</th><th class="text-end">Accounts</th><th>FFmpeg</th></tr></thead>
            <tbody>
            <?php foreach ($plans as $p): ?>
                <tr><td class="fw-medium"><?= e((string) $p['name']) ?></td><td class="small"><?= $p['owner'] !== '' ? e((string) $p['owner']) . ' <span class="text-body-secondary">(reseller)</span>' : 'Administrators' ?></td><td class="text-end"><?= (int) $p['users'] ?></td><td><?= (int) $p['ffmpeg'] === 1 ? '<span class="wcp-state is-ok">Included</span>' : '<span class="wcp-state">Not included</span>' ?></td></tr>
            <?php endforeach; ?>
            <?php if ($plans === []): ?><tr><td colspan="4" class="text-center text-body-secondary">No plans yet.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
</div>
