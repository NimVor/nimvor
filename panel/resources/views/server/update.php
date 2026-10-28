<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed>|null $status
 * @var array<string, mixed>|null $check
 * @var \NimVor\Agent\AgentException|null $agentError
 */
$short = static fn (string $sha): string => $sha !== '' ? substr($sha, 0, 7) : '—';
$running = !empty($status['running']);
?>
<?php $this->start(); ?>NimVor updates<?php $this->end('title'); ?>
<?php $this->start(); ?>Installed version, the newest version and the last update<?php $this->end('subtitle'); ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>
<div class="row g-3">
    <div class="col-lg-5">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-cloud-arrow-down me-1"></i>Version</div>
            <div class="card-body">
                <dl class="row small mb-3">
                    <dt class="col-5">Installed</dt><dd class="col-7 font-monospace"><?= e((string) ($app_version ?? '')) ?> <span class="nim-muted">(<?= e($short((string) ($status['commit'] ?? ''))) ?>)</span></dd>
                    <dt class="col-5">Branch</dt><dd class="col-7 font-monospace"><?= e((string) ($status['branch'] ?? '—')) ?></dd>
                    <dt class="col-5">Newest</dt><dd class="col-7 font-monospace"><?php if ($check !== null && ($check['latest'] ?? '') !== ''): ?><?= e($short((string) $check['latest'])) ?><?php else: ?>unknown<?php endif; ?></dd>
                    <dt class="col-5">Last looked up</dt><dd class="col-7"><?= $check !== null ? e(date('Y-m-d H:i', (int) $check['checked_at'])) : 'never' ?></dd>
                    <dt class="col-5">Source folder</dt><dd class="col-7 font-monospace text-break"><?= e((string) ($status['path'] ?? '—')) ?></dd>
                </dl>
                <?php if ($check !== null && !empty($check['available'])): ?>
                    <div class="alert alert-info small py-2"><i class="bi bi-arrow-up-circle me-1"></i>A newer version is available.</div>
                <?php elseif ($check !== null && ($check['state'] ?? '') === 'current'): ?>
                    <div class="alert alert-success small py-2"><i class="bi bi-check-circle me-1"></i>NimVor is up to date.</div>
                <?php elseif ($check !== null && ($check['reason'] ?? '') !== ''): ?>
                    <div class="alert alert-secondary small py-2"><?= e((string) $check['reason']) ?></div>
                <?php endif; ?>
                <div class="d-flex flex-wrap gap-2">
                    <form method="post" action="<?= e($this->url('nimvor.update.check')) ?>" data-nim-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Check now</button></form>
                    <?php if (!empty($status['updatable'])): ?>
                        <form method="post" action="<?= e($this->url('nimvor.update.start')) ?>" data-nim-noajax data-nim-confirm="Update NimVor now? The installer runs from <?= e((string) $status['path']) ?>: it pulls the newest version, then installs it. The panel and the agent restart during it (about a minute); websites, mail and databases keep running.">
                            <?= $this->csrfField() ?><button class="btn btn-sm btn-primary"<?= $running ? ' disabled' : '' ?>><i class="bi bi-cloud-arrow-down me-1"></i>Update now</button>
                        </form>
                    <?php endif; ?>
                </div>
                <?php if ($status !== null && empty($status['updatable'])): ?>
                    <p class="small nim-muted mt-3 mb-1"><?= e((string) ($status['reason'] ?? '')) ?></p>
                    <p class="small mb-0">Update over SSH: <code><?= e((string) ($status['command'] ?? '')) ?></code></p>
                <?php endif; ?>
            </div>
        </div>
    </div>
    <div class="col-lg-7">
        <div class="card h-100">
            <div class="card-header d-flex align-items-center gap-2"><span>Last update</span>
                <span class="ms-auto small" data-nim-update-state><?= $running ? '<span class="nim-state is-warn">running</span>' : (($status['last_result'] ?? '') === 'success' ? '<span class="nim-state is-ok">finished</span>' : (($status['log'] ?? '') !== '' ? '<span class="nim-state">' . e((string) ($status['last_result'] ?? '')) . '</span>' : '')) ?></span>
            </div>
            <div class="card-body">
                <pre class="nim-db-create small mb-0 nim-update-log" data-nim-update-log="<?= e($this->url('nimvor.update')) ?>" data-running="<?= $running ? '1' : '0' ?>"><?= e((string) ($status['log'] ?? '')) ?: 'No update has run from the panel yet.' ?></pre>
            </div>
        </div>
    </div>
</div>
