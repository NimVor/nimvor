<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed>|null $result */
/** @var bool $ran */
/** @var string|null $error */
$error = $error ?? null;
$advs = (array) ($result['advisories'] ?? []);
$kind = static fn (string $k): string => $k === 'security' ? 'text-bg-danger' : ($k === 'unknown' ? 'text-bg-warning' : 'text-bg-secondary');
$sev = static fn (string $s): string => match (strtolower($s)) {
    'critical' => 'text-bg-danger',
    'important' => 'text-bg-danger',
    'moderate' => 'text-bg-warning',
    default => 'text-bg-secondary',
};
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>

<div class="nim-panel mb-3">
    <div class="nim-statusbar">
        <span class="fw-medium">Vulnerability advisories</span>
        <span class="small nim-muted">from the operating system's own advisories</span>
        <form class="ms-auto" method="post" action="<?= e($this->url('security.advisories.run')) ?>" data-nim-busy>
            <?= $this->csrfField() ?>
            <button class="btn btn-sm btn-primary"><i class="bi bi-shield-exclamation me-1"></i>Check now</button>
        </form>
    </div>
    <p class="small nim-muted mb-0 mt-2">Pending updates with the vulnerability information the operating system provides. On AlmaLinux and Rocky this includes the advisory, severity and CVEs (from <code>dnf updateinfo</code>); on Ubuntu the security pocket tells security updates from ordinary ones. No updates are installed — apply them from Packages.</p>
</div>

<?php if ($error !== null): ?>
    <div class="nim-infobar is-error"><i class="bi bi-x-circle-fill" aria-hidden="true"></i><span>The advisory check could not run: <?= e($error) ?></span></div>
<?php elseif ($ran && $advs === []): ?>
    <div class="nim-infobar is-ok"><i class="bi bi-check-circle-fill" aria-hidden="true"></i><span>No pending updates. The system is up to date.</span></div>
<?php elseif ($ran): ?>
    <div class="nim-panel">
        <div class="nim-statusbar">
            <span class="small"><span class="badge text-bg-danger"><?= (int) ($result['security'] ?? 0) ?></span> security</span>
            <span class="small"><span class="badge text-bg-secondary"><?= (int) ($result['normal'] ?? 0) ?></span> normal</span>
            <?php if ((int) ($result['unknown'] ?? 0) > 0): ?><span class="small"><span class="badge text-bg-warning"><?= (int) $result['unknown'] ?></span> unknown</span><?php endif; ?>
        </div>
        <div class="table-responsive mt-2">
            <table class="table table-sm align-middle mb-0">
                <thead><tr><th>Package</th><th>Installed</th><th>Fixed in</th><th>Kind</th><th>Advisory</th><th>CVEs</th></tr></thead>
                <tbody>
                <?php foreach ($advs as $a): $a = (array) $a; $cves = (array) ($a['cves'] ?? []); ?>
                    <tr>
                        <td class="font-monospace small text-break"><?= e((string) ($a['package'] ?? '')) ?></td>
                        <td class="small nim-muted"><?= e((string) ($a['installed'] ?? '')) ?: '—' ?></td>
                        <td class="small"><?= e((string) ($a['fixed'] ?? '')) ?: '—' ?></td>
                        <td>
                            <span class="badge <?= e($kind((string) ($a['kind'] ?? ''))) ?>"><?= e((string) ($a['kind'] ?? '')) ?></span>
                            <?php if (!empty($a['severity'])): ?><span class="badge <?= e($sev((string) $a['severity'])) ?>"><?= e((string) $a['severity']) ?></span><?php endif; ?>
                        </td>
                        <td class="small nim-muted"><?= e((string) ($a['advisory'] ?? '')) ?: '—' ?></td>
                        <td class="small text-break"><?= $cves === [] ? '—' : e(implode(', ', array_map('strval', $cves))) ?></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    </div>
<?php else: ?>
    <div class="nim-infobar"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>Check now to list pending updates and their advisories.</span></div>
<?php endif; ?>
