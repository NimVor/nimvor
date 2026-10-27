<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed>|null $result */
/** @var bool $ran */
/** @var string|null $error */
$error = $error ?? null;
$canManage = $this->can(\NimVor\Security\Permissions::SECURITY_MANAGE);
$findings = (array) ($result['findings'] ?? []);
$sev = static fn (string $s): string => match ($s) {
    'critical', 'high' => 'text-bg-danger',
    'medium' => 'text-bg-warning',
    default => 'text-bg-secondary',
};
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>

<div class="nim-panel mb-3">
    <div class="nim-statusbar">
        <span class="fw-medium">File integrity</span>
        <span class="small nim-muted">dpkg/rpm verification + NimVor baseline</span>
        <form class="ms-auto d-flex gap-2" method="post" action="<?= e($this->url('security.integrity.run')) ?>" data-nim-busy>
            <?= $this->csrfField() ?>
            <button class="btn btn-sm btn-primary"><i class="bi bi-fingerprint me-1"></i>Run check</button>
        </form>
        <?php if ($canManage): ?>
        <form method="post" action="<?= e($this->url('security.integrity.baseline')) ?>" data-nim-busy>
            <?= $this->csrfField() ?>
            <button class="btn btn-sm btn-outline-secondary" title="Record the current agent binary and configuration as the known-good baseline. Do this after installing or upgrading NimVor."><i class="bi bi-bookmark-check me-1"></i>Set baseline</button>
        </form>
        <?php endif; ?>
    </div>
    <p class="small nim-muted mb-0 mt-2">Package-owned files are checked against the operating system's own records (<code>dpkg --verify</code> on Debian/Ubuntu, <code>rpm -Va</code> on RHEL), so a normal package update is not flagged. NimVor's own files — the agent binary and its configuration under <code>/etc/nimvor</code> — are checked against a baseline. Running the full check takes up to a couple of minutes.</p>
</div>

<?php if ($error !== null): ?>
    <div class="nim-infobar is-error"><i class="bi bi-x-circle-fill" aria-hidden="true"></i><span>The integrity check could not run: <?= e($error) ?></span></div>
<?php elseif ($ran && $findings === []): ?>
    <div class="nim-infobar is-ok"><i class="bi bi-check-circle-fill" aria-hidden="true"></i><span>No integrity problems found<?= !empty($result['has_baseline']) ? '' : '. No NimVor baseline has been recorded yet — use “Set baseline” so changes to the agent binary and configuration are detected.' ?></span></div>
<?php elseif ($ran): ?>
    <div class="nim-panel">
        <div class="table-responsive">
            <table class="table table-sm align-middle mb-0">
                <thead><tr><th>Severity</th><th>File</th><th>Issue</th><th>Detail</th></tr></thead>
                <tbody>
                <?php foreach ($findings as $f): $f = (array) $f; ?>
                    <tr>
                        <td><span class="badge <?= e($sev((string) ($f['severity'] ?? ''))) ?>"><?= e(strtoupper((string) ($f['severity'] ?? ''))) ?></span></td>
                        <td class="font-monospace small text-break"><?= e((string) ($f['path'] ?? '')) ?></td>
                        <td><span class="small"><?= e((string) ($f['issue'] ?? '')) ?></span> <span class="small nim-muted">(<?= e((string) ($f['class'] ?? '')) ?>)</span></td>
                        <td class="small nim-muted text-break"><?= e((string) ($f['detail'] ?? '')) ?></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    </div>
<?php else: ?>
    <div class="nim-infobar"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>Run the check to verify the agent binary, NimVor configuration and the installed packages.</span></div>
<?php endif; ?>
