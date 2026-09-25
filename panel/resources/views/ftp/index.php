<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed>|null $status
 * @var array<string, array<string, mixed>> $sitesByUser
 */
use WhaCP\Security\Permissions as P;

$cfg = (array) ($status['settings'] ?? []);
$cert = (array) ($status['certificate'] ?? []);
$others = (array) ($status['other_ftp_servers'] ?? []);
$accounts = (array) ($status['accounts'] ?? []);
$errors ??= [];
$old ??= [];
$v = static fn (string $k, mixed $d): string => (string) ($old[$k] ?? $cfg[$k] ?? $d);
?>
<?php $this->start(); ?>FTP server<?php $this->end('title'); ?>
<?php $this->start(); ?>FTPS accounts for websites (pure-ftpd)<?php $this->end('subtitle'); ?>
<?php if ($agentError !== null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError]) ?>
<?php elseif (empty($status['installed'])): ?>
    <div class="card"><div class="card-body">
        <p>The FTP server (pure-ftpd) is not installed. Websites can still use SFTP, which needs no extra software.</p>
        <?php if ($this->can(P::PACKAGES_MANAGE)): ?>
            <form method="post" action="<?= e($this->url('ftp.install')) ?>"><?= $this->csrfField() ?><button class="btn btn-primary"><i class="bi bi-download me-1"></i>Install pure-ftpd</button></form>
            <p class="small text-body-secondary mt-2 mb-0">The distribution packages of vsftpd and ProFTPD conflict with pure-ftpd; if one is installed, remove it first (its accounts are not carried over).</p>
        <?php endif; ?>
    </div></div>
<?php else: ?>
<div class="row g-3">
    <div class="col-lg-6">
        <div class="card mb-3">
            <div class="card-header d-flex align-items-center"><i class="bi bi-hdd-network me-1"></i>Status
                <span class="ms-auto"><?= !empty($status['active']) ? $this->statusBadge('active', 'running') : $this->statusBadge('inactive', empty($status['configured']) ? 'not set up' : 'stopped') ?></span></div>
            <div class="card-body">
                <dl class="wcp-dl">
                    <dt>Service</dt><dd><code>whacp-ftpd.service</code></dd>
                    <dt>Encryption</dt><dd><?= ($cfg['tls'] ?? 'required') === 'optional' ? 'TLS optional — plain FTP allowed' : 'TLS required (FTPS)' ?></dd>
                    <dt>Passive ports</dt><dd><?= (int) ($cfg['passive_min'] ?? 0) ?>–<?= (int) ($cfg['passive_max'] ?? 0) ?></dd>
                    <?php if ($cert !== []): ?>
                        <dt>Certificate</dt><dd><?= e((string) $cert['subject']) ?> <span class="text-body-secondary small">(self-signed, until <?= e(substr((string) $cert['not_after'], 0, 10)) ?>)</span></dd>
                        <dt>SHA-256</dt><dd><code class="small text-break"><?= e(implode(':', str_split((string) $cert['sha256'], 2))) ?></code></dd>
                    <?php endif; ?>
                </dl>
                <?php if ($others !== []): ?>
                    <div class="alert alert-warning small mb-0"><?= e(implode(', ', array_map('strval', $others))) ?> is running. Setting up WhaCP's FTP server stops and disables it (tick the confirmation below); its own accounts are not carried over.</div>
                <?php endif; ?>
            </div>
            <?php if (!empty($status['active'])): ?>
                <div class="card-footer"><form method="post" action="<?= e($this->url('ftp.disable')) ?>" data-wcp-confirm="Stop the FTP server? Accounts are kept and come back when you start it again."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger"><i class="bi bi-stop-circle me-1"></i>Stop FTP server</button></form></div>
            <?php endif; ?>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-sliders me-1"></i><?= empty($status['configured']) ? 'Set up' : 'Settings' ?></div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('ftp.configure')) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <div class="row g-2">
                        <div class="col-6"><?= $this->partial('partials/field', ['name' => 'passive_min', 'label' => 'Passive ports from', 'type' => 'number', 'value' => $v('passive_min', 40110)]) ?></div>
                        <div class="col-6"><?= $this->partial('partials/field', ['name' => 'passive_max', 'label' => 'to', 'type' => 'number', 'value' => $v('passive_max', 40210)]) ?></div>
                        <div class="col-6"><?= $this->partial('partials/field', ['name' => 'max_clients', 'label' => 'Connections', 'type' => 'number', 'value' => $v('max_clients', 50)]) ?></div>
                        <div class="col-6"><?= $this->partial('partials/field', ['name' => 'max_per_ip', 'label' => 'Per address', 'type' => 'number', 'value' => $v('max_per_ip', 8)]) ?></div>
                        <div class="col-12"><?= $this->partial('partials/field', ['name' => 'passive_ip', 'label' => 'Public IP for passive mode (optional)', 'value' => $v('passive_ip', ''), 'help' => 'Only behind NAT, when clients must connect to a different address than the server sees.']) ?></div>
                    </div>
                    <div class="mb-3">
                        <label class="form-label" for="ftp-tls">Encryption</label>
                        <select class="form-select" id="ftp-tls" name="tls">
                            <option value="required">TLS required (recommended)</option>
                            <option value="optional"<?= $v('tls', 'required') === 'optional' ? ' selected' : '' ?>>TLS optional — allows plain FTP, which sends passwords unencrypted</option>
                        </select>
                    </div>
                    <?php if ($others !== []): ?>
                        <div class="form-check mb-3"><input class="form-check-input" type="checkbox" name="confirm_replace" value="1" id="ftp-confirm"><label class="form-check-label" for="ftp-confirm">Stop and disable <?= e(implode(', ', array_map('strval', $others))) ?></label></div>
                    <?php endif; ?>
                    <button class="btn btn-primary btn-sm"><?= empty($status['configured']) ? 'Set up and start' : 'Save and restart' ?></button>
                    <div class="form-text">WhaCP runs pure-ftpd from its own configuration (<code>/etc/whacp-ftp</code>); the distribution's configuration is not changed. If the firewall is on, port 21 and the passive range are opened.</div>
                </form>
            </div>
        </div>
    </div>
</div>
<div class="card">
    <div class="card-header"><i class="bi bi-people me-1"></i>Accounts</div>
    <div class="table-responsive">
        <table class="table mb-0 wcp-table">
            <thead><tr><th>Login</th><th>Website</th><th>Folder</th><th class="text-end">Quota</th></tr></thead>
            <tbody>
            <?php foreach ($accounts as $a): $a = (array) $a; $site = $sitesByUser[(string) $a['user']] ?? null; ?>
                <tr>
                    <td class="font-monospace"><?= e((string) $a['name']) ?></td>
                    <td><?php if ($site !== null): ?><a href="<?= e($this->url('sites.ftp', ['id' => (int) $site['id']])) ?>"><?= e((string) $site['domain']) ?></a><?php else: ?><span class="text-body-secondary"><?= e((string) ($a['user'] ?: 'unknown user')) ?></span><?php endif; ?></td>
                    <td class="small font-monospace"><?= e((string) $a['dir']) ?></td>
                    <td class="text-end"><?= (int) $a['quota_mb'] > 0 ? e(format_bytes((int) $a['quota_mb'] * 1048576)) : '—' ?></td>
                </tr>
            <?php endforeach; ?>
            <?php if ($accounts === []): ?><tr><td colspan="4" class="text-body-secondary">No FTP accounts yet. Add them from a website's <strong>FTP / SFTP</strong> tab.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
</div>
<?php endif; ?>
