<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $site */
$ssl = $site['ssl'] ?? null;
$php = $site['php'] ?? null;
?>
<?php $this->start(); ?><?= e($site['domain']) ?><?php $this->end('title'); ?>
<?php $this->start(); ?>Discovered website · <?= !empty($site['enabled']) ? 'enabled' : 'not enabled' ?> in Nginx<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
<?php if (!empty($site['enabled'])): ?>
    <a class="btn btn-sm btn-outline-primary" href="<?= e((!empty($site['https']) ? 'https://' : 'http://') . $site['domain']) ?>" target="_blank" rel="noopener noreferrer"><i class="bi bi-box-arrow-up-right"></i> Open</a>
<?php endif; ?>
<?php if ($this->can(\NimVor\Security\Permissions::IMPORT_RUN) && empty($site['managed_by'])): ?>
    <a class="btn btn-sm btn-primary" href="<?= e($this->url('websites.import', ['id' => $site['id']])) ?>"><i class="bi bi-box-arrow-in-down"></i> Import into NimVor</a>
<?php endif; ?>
<?php $this->end('actions'); ?>

<?= $this->partial('partials/discovery_bar', ['discoveredAt' => $discoveredAt]) ?>

<div class="alert alert-info small"><i class="bi bi-info-circle me-1"></i>
    This website was found by read-only discovery. NimVor has not taken over its management; its configuration is shown exactly as found.
</div>

<div class="row g-3">
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-globe2 me-1"></i>General</div>
            <div class="card-body">
                <dl class="nim-dl">
                    <dt>Domain</dt><dd><?= e($site['domain']) ?></dd>
                    <dt>Aliases</dt><dd><?= $site['aliases'] === [] ? '<span class="text-body-secondary">none</span>' : e(implode(', ', $site['aliases'])) ?></dd>
                    <dt>Status</dt><dd><?= !empty($site['enabled']) ? $this->statusBadge('enabled') : $this->statusBadge('disabled') ?></dd>
                    <dt>Type</dt><dd><?= e($site['kind']) ?></dd>
                    <dt>Document root</dt><dd>
                        <?php if (!empty($site['document_root'])): ?>
                            <code><?= e($site['document_root']) ?></code>
                            <?= !empty($site['document_root_exists']) ? '' : $this->statusBadge('warning', 'missing') ?>
                        <?php else: ?><span class="text-body-secondary">—</span><?php endif; ?>
                    </dd>
                    <?php if (!empty($site['proxy_pass'])): ?>
                        <dt>Proxies to</dt><dd><code><?= e($site['proxy_pass']) ?></code></dd>
                    <?php endif; ?>
                    <dt>Listen</dt><dd><?= e(implode(' · ', $site['listen'])) ?></dd>
                    <?php if (!empty($site['managed_by'])): ?>
                        <dt>Managed by</dt><dd><?= e($site['managed_by']) ?> <span class="small text-body-secondary">(detected)</span></dd>
                    <?php endif; ?>
                </dl>
            </div>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-filetype-php me-1"></i>PHP</div>
            <div class="card-body">
                <?php if ($php === null): ?>
                    <p class="text-body-secondary mb-0">This website does not pass requests to PHP-FPM.</p>
                <?php else: ?>
                    <dl class="nim-dl">
                        <dt>Version</dt><dd><?= e($php['version'] !== '' ? $php['version'] : 'unknown') ?></dd>
                        <dt>FPM pool</dt><dd><?= e(($php['pool'] ?? '') !== '' ? $php['pool'] : '—') ?></dd>
                        <dt>Pool user</dt><dd><?= e(($php['user'] ?? '') !== '' ? $php['user'] : '—') ?></dd>
                        <dt>FastCGI</dt><dd><code><?= e($php['listen']) ?></code></dd>
                    </dl>
                    <?php if (empty($php['resolved'])): ?>
                        <div class="small text-warning-emphasis"><i class="bi bi-question-circle me-1"></i>No PHP-FPM pool listens on this address; the version was inferred from the socket name.</div>
                    <?php endif; ?>
                <?php endif; ?>
            </div>
        </div>
        <div class="card">
            <div class="card-header"><i class="bi bi-shield-lock me-1"></i>SSL / HTTPS</div>
            <div class="card-body">
                <?php if ($ssl === null): ?>
                    <p class="mb-0 text-body-secondary"><?= !empty($site['https']) ? 'HTTPS is configured, but no certificate file was referenced.' : 'No HTTPS server block was found.' ?></p>
                <?php else: ?>
                    <?php $st = (int) $ssl['days_remaining'] <= $warnDays && $ssl['status'] === 'valid' ? 'expiring' : (string) $ssl['status']; ?>
                    <dl class="nim-dl">
                        <dt>Status</dt><dd><?= $this->statusBadge($st) ?></dd>
                        <?php if (empty($ssl['error'])): ?>
                            <dt>Expires</dt><dd><?= e(gmdate('Y-m-d', (int) strtotime((string) $ssl['not_after']))) ?> (<?= (int) $ssl['days_remaining'] ?> days)</dd>
                            <dt>Issuer</dt><dd class="small"><?= e($ssl['issuer']) ?></dd>
                            <dt>Covers domain</dt><dd><?= !empty($ssl['covers_domain']) ? $this->statusBadge('yes') : $this->statusBadge('error', 'no') ?></dd>
                            <dt>Let's Encrypt</dt><dd><?= !empty($ssl['lets_encrypt']) ? 'yes' : 'no' ?></dd>
                        <?php else: ?>
                            <dt>Problem</dt><dd><?= e($ssl['error']) ?></dd>
                        <?php endif; ?>
                        <dt>Certificate</dt><dd><code class="small"><?= e($ssl['cert_file']) ?></code></dd>
                        <dt>HTTP → HTTPS</dt><dd><?= !empty($site['https_redirect']) ? $this->statusBadge('yes') : $this->statusBadge('unknown', 'no') ?></dd>
                    </dl>
                <?php endif; ?>
            </div>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-file-earmark-code me-1"></i>Nginx configuration files</div>
            <ul class="list-group list-group-flush small">
                <?php foreach ($site['config_files'] as $f): ?>
                    <li class="list-group-item"><code><?= e($f) ?></code></li>
                <?php endforeach; ?>
            </ul>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-journal-text me-1"></i>Logs</div>
            <ul class="list-group list-group-flush small">
                <?php if ($site['access_logs'] === [] && $site['error_logs'] === []): ?>
                    <li class="list-group-item text-body-secondary">Uses the server-wide Nginx logs.</li>
                <?php endif; ?>
                <?php foreach ($site['access_logs'] as $f): ?>
                    <li class="list-group-item"><span class="badge text-bg-light border me-1">access</span><code><?= e($f) ?></code></li>
                <?php endforeach; ?>
                <?php foreach ($site['error_logs'] as $f): ?>
                    <li class="list-group-item"><span class="badge text-bg-light border me-1">error</span><code><?= e($f) ?></code></li>
                <?php endforeach; ?>
            </ul>
        </div>
    </div>
    <div class="col-12">
        <div class="card">
            <div class="card-header"><i class="bi bi-clock-history me-1"></i>Related cron jobs</div>
            <?php if ($cron === []): ?>
                <div class="card-body text-body-secondary small">No cron jobs reference this website's directory or domain.</div>
            <?php else: ?>
                <div class="table-responsive">
                    <table class="table table-sm mb-0 nim-table">
                        <thead><tr><th>Schedule</th><th>User</th><th>Command</th></tr></thead>
                        <tbody>
                        <?php foreach ($cron as $job): ?>
                            <tr><td><code><?= e($job['schedule']) ?></code></td><td><?= e($job['user']) ?></td><td><code class="small"><?= e($job['command']) ?></code></td></tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
            <?php endif; ?>
        </div>
    </div>
</div>
