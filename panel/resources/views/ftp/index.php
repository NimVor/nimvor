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
$serverIp ??= null;
$v = static fn (string $k, mixed $d): string => (string) ($old[$k] ?? $cfg[$k] ?? $d);
$openSettings = $errors !== [] || empty($status['configured']);
$tls = ($cfg['tls'] ?? 'required') === 'optional' ? 'optional' : 'required';
?>
<?php $this->start(); ?>FTP<?php $this->end('title'); ?>
<?php $this->start(); ?>FTPS accounts for websites (pure-ftpd)<?php $this->end('subtitle'); ?>
<?php if ($agentError !== null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError]) ?>
<?php elseif (empty($status['installed'])): ?>
    <div class="wcp-infobar"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>The FTP server (pure-ftpd) is not installed. Websites can still use SFTP, which needs no extra software.</span></div>
    <?php if ($this->can(P::PACKAGES_MANAGE)): ?>
        <div class="wcp-panel">
            <form method="post" action="<?= e($this->url('ftp.install')) ?>"><?= $this->csrfField() ?><button class="btn btn-primary btn-sm"><i class="bi bi-download me-1"></i>Install pure-ftpd</button></form>
            <p class="small text-body-secondary mt-2 mb-0">The distribution packages of vsftpd and ProFTPD conflict with pure-ftpd; if one is installed, remove it first (its accounts are not carried over).</p>
        </div>
    <?php endif; ?>
<?php else: ?>
<div class="wcp-infobar">
    <i class="bi bi-info-circle-fill" aria-hidden="true"></i>
    <span>FTP address: <code><?= e('ftp://' . ($serverIp ?? 'your-server') . ':21') ?></code></span>
    <span class="wcp-muted small"><?= $tls === 'optional' ? 'TLS optional — plain FTP allowed' : 'TLS required (FTPS): choose “FTP over explicit TLS” in your client' ?> · passive ports <?= (int) ($cfg['passive_min'] ?? 0) ?>–<?= (int) ($cfg['passive_max'] ?? 0) ?></span>
</div>
<?php if ($others !== []): ?>
    <div class="alert alert-warning small"><?= e(implode(', ', array_map('strval', $others))) ?> is running. Setting up WhaCP's FTP server stops and disables it (tick the confirmation in the settings); its own accounts are not carried over.</div>
<?php endif; ?>

<div class="wcp-panel" data-wcp-dt id="ftp-accounts">
    <div class="wcp-actionbar">
        <?php if ($sitesByUser !== [] && $this->can(P::FTP_MANAGE)): ?>
            <details class="position-relative" data-wcp-menu>
                <summary class="btn btn-sm btn-primary"><i class="bi bi-plus-lg me-1"></i>Add FTP</summary>
                <div class="wcp-dropmenu">
                    <div class="small wcp-muted px-2 pb-1">For the website…</div>
                    <?php foreach ($sitesByUser as $s): ?><a href="<?= e($this->url('sites.ftp', ['id' => (int) $s['id']])) ?>#new-ftp"><i class="bi bi-globe2"></i><?= e((string) $s['domain']) ?></a><?php endforeach; ?>
                </div>
            </details>
        <?php endif; ?>
        <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#ftp-settings"><i class="bi bi-sliders me-1"></i><?= empty($status['configured']) ? 'Set up' : 'Settings' ?></button>
        <span class="wcp-svc" title="whacp-ftpd.service"><i class="bi bi-hdd-network" aria-hidden="true"></i>Pure-FTPd <?= !empty($status['active']) ? 'running <i class="bi bi-play-fill" aria-hidden="true"></i>' : (empty($status['configured']) ? 'not set up <i class="bi bi-stop-fill" aria-hidden="true"></i>' : 'stopped <i class="bi bi-stop-fill" aria-hidden="true"></i>') ?></span>
        <?php if (!empty($status['active'])): ?>
            <form method="post" action="<?= e($this->url('ftp.disable')) ?>" data-wcp-confirm="Stop the FTP server? Accounts are kept and come back when you start it again."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-stop-circle me-1"></i>Stop</button></form>
        <?php endif; ?>
        <div class="wcp-actionbar-end">
            <div class="wcp-search"><input class="form-control form-control-sm" type="search" placeholder="FTP search" aria-label="Search FTP accounts" data-wcp-dt-search><i class="bi bi-search" aria-hidden="true"></i></div>
        </div>
    </div>

    <?= $this->modalStart('ftp-settings', 'FTP server settings', $errors !== []) ?>
        <form class="wcp-form-panel border rounded-3 p-3 mb-3" method="post" action="<?= e($this->url('ftp.configure')) ?>" data-wcp-busy>
            <?= $this->csrfField() ?>
            <div class="fw-semibold mb-2"><?= empty($status['configured']) ? 'Set up the FTP server' : 'FTP server settings' ?></div>
            <div class="row g-2">
                <div class="col-6 col-md-3"><?= $this->partial('partials/field', ['name' => 'passive_min', 'label' => 'Passive ports from', 'type' => 'number', 'value' => $v('passive_min', 40110)]) ?></div>
                <div class="col-6 col-md-3"><?= $this->partial('partials/field', ['name' => 'passive_max', 'label' => 'to', 'type' => 'number', 'value' => $v('passive_max', 40210)]) ?></div>
                <div class="col-6 col-md-3"><?= $this->partial('partials/field', ['name' => 'max_clients', 'label' => 'Connections', 'type' => 'number', 'value' => $v('max_clients', 50)]) ?></div>
                <div class="col-6 col-md-3"><?= $this->partial('partials/field', ['name' => 'max_per_ip', 'label' => 'Per address', 'type' => 'number', 'value' => $v('max_per_ip', 8)]) ?></div>
                <div class="col-md-6"><?= $this->partial('partials/field', ['name' => 'passive_ip', 'label' => 'Public IP for passive mode (optional)', 'value' => $v('passive_ip', ''), 'help' => 'Only behind NAT, when clients must connect to a different address than the server sees.']) ?></div>
                <div class="col-md-6">
                    <label class="form-label" for="ftp-tls">Encryption</label>
                    <select class="form-select" id="ftp-tls" name="tls">
                        <option value="required">TLS required (recommended)</option>
                        <option value="optional"<?= $v('tls', 'required') === 'optional' ? ' selected' : '' ?>>TLS optional — allows plain FTP, which sends passwords unencrypted</option>
                    </select>
                </div>
            </div>
            <?php if ($others !== []): ?>
                <div class="form-check mb-3"><input class="form-check-input" type="checkbox" name="confirm_replace" value="1" id="ftp-confirm"><label class="form-check-label" for="ftp-confirm">Stop and disable <?= e(implode(', ', array_map('strval', $others))) ?></label></div>
            <?php endif; ?>
            <?php if ($cert !== []): ?>
                <div class="small wcp-muted mb-2">Certificate: <?= e((string) $cert['subject']) ?> (self-signed, until <?= e(substr((string) $cert['not_after'], 0, 10)) ?>) · SHA-256 <code class="text-break"><?= e(implode(':', str_split((string) $cert['sha256'], 2))) ?></code></div>
            <?php endif; ?>
            <button class="btn btn-primary btn-sm"><?= empty($status['configured']) ? 'Set up and start' : 'Save and restart' ?></button>
            <div class="form-text">WhaCP runs pure-ftpd from its own configuration (<code>/etc/whacp-ftp</code>); the distribution's configuration is not changed. If the firewall is on, port 21 and the passive range are opened.</div>
        </form>
    <?= $this->modalEnd() ?>

    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Username</th><th>Website</th><th>Document root</th><th>Quota</th><th class="text-end">Operate</th></tr></thead>
            <tbody>
            <?php foreach ($accounts as $a): $a = (array) $a; $site = $sitesByUser[(string) $a['user']] ?? null; ?>
                <tr data-wcp-row>
                    <td class="fw-semibold"><?= e((string) $a['name']) ?></td>
                    <td><?php if ($site !== null): ?><?= e((string) $site['domain']) ?><?php else: ?><span class="wcp-muted"><?= e((string) ($a['user'] ?: 'unknown user')) ?></span><?php endif; ?></td>
                    <td class="small"><?php if ($site !== null && $this->can(P::FILES_MANAGE)): ?><a class="font-monospace" href="<?= e($this->url('sites.files', ['id' => (int) $site['id']])) ?>"><?= e((string) $a['dir']) ?></a><?php else: ?><span class="font-monospace"><?= e((string) $a['dir']) ?></span><?php endif; ?></td>
                    <td class="small"><?= (int) $a['quota_mb'] > 0 ? e(format_bytes((int) $a['quota_mb'] * 1048576)) : '<span class="wcp-good">Not set</span>' ?></td>
                    <td class="text-end"><?php if ($site !== null): ?><span class="wcp-ops"><a class="wcp-op" href="<?= e($this->url('sites.ftp', ['id' => (int) $site['id']])) ?>">Manage</a></span><?php endif; ?></td>
                </tr>
            <?php endforeach; ?>
            <?php if ($accounts === []): ?>
                <tr><td colspan="5" class="text-center py-4 wcp-muted">No FTP accounts yet. <?= $sitesByUser !== [] ? 'Use “Add FTP” to create one for a website.' : 'Add them from a website\'s FTP / SFTP page.' ?></td></tr>
            <?php else: ?>
                <tr class="d-none" data-wcp-dt-empty><td colspan="5" class="text-center py-4 wcp-muted">No account matches.</td></tr>
            <?php endif; ?>
            </tbody>
        </table>
    </div>
    <div class="wcp-pager" data-wcp-dt-pager></div>
</div>
<?php endif; ?>
