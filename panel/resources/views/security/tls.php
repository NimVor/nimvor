<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed>|null $result */
/** @var bool $ran */
/** @var int $endpoints */
/** @var string|null $error */
$error = $error ?? null;
$results = (array) ($result['results'] ?? []);
$sev = static fn (string $s): string => $s === 'high' ? 'text-bg-danger' : ($s === 'medium' ? 'text-bg-warning' : 'text-bg-secondary');
$withFindings = array_filter($results, static fn ($r): bool => !empty(((array) $r)['findings']));
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>

<div class="nim-panel mb-3">
    <div class="nim-statusbar">
        <span class="fw-medium">TLS configuration audit</span>
        <span class="small nim-muted"><?= (int) $endpoints ?> endpoint(s): HTTPS websites and mail</span>
        <form class="ms-auto" method="post" action="<?= e($this->url('security.tls.run')) ?>" data-nim-busy>
            <?= $this->csrfField() ?>
            <button class="btn btn-sm btn-primary"<?= $endpoints === 0 ? ' disabled' : '' ?>><i class="bi bi-shield-lock me-1"></i>Run audit</button>
        </form>
    </div>
    <p class="small nim-muted mb-0 mt-2">Each endpoint on this server is probed for obsolete protocols (TLS 1.0/1.1 accepted), weak cipher suites, and certificate problems (SHA-1, expired, untrusted chain, host-name mismatch). This only reads — it never changes a website's configuration. Apply a fix from SSL/TLS afterwards.</p>
</div>

<?php if ($endpoints === 0): ?>
    <div class="nim-infobar"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>No HTTPS websites or mail endpoints to audit yet. Enable HTTPS on a website, or set up the mail server, then run the audit.</span></div>
<?php elseif ($error !== null): ?>
    <div class="nim-infobar is-error"><i class="bi bi-x-circle-fill" aria-hidden="true"></i><span>The TLS audit could not run: <?= e($error) ?></span></div>
<?php elseif ($ran && $withFindings === []): ?>
    <div class="nim-infobar is-ok"><i class="bi bi-check-circle-fill" aria-hidden="true"></i><span>No TLS problems found on the <?= count($results) ?> endpoint(s) reached.</span></div>
<?php elseif ($ran): ?>
    <?php foreach ($results as $r): $r = (array) $r; $fs = (array) ($r['findings'] ?? []); if ($fs === []) { continue; } ?>
    <div class="nim-panel mb-2">
        <div class="nim-statusbar">
            <span class="fw-medium font-monospace small"><?= e((string) ($r['endpoint'] ?? '')) ?></span>
            <span class="small nim-muted ms-2"><?= e((string) ($r['kind'] ?? '')) ?></span>
            <?php if (!empty($r['best_version'])): ?><span class="small nim-muted ms-auto">best: <?= e((string) $r['best_version']) ?><?= !empty($r['min_version']) ? ', lowest: ' . e((string) $r['min_version']) : '' ?></span><?php endif; ?>
        </div>
        <div class="table-responsive mt-2">
            <table class="table table-sm align-middle mb-0">
                <thead><tr><th>Severity</th><th>Issue</th><th>Detail</th></tr></thead>
                <tbody>
                <?php foreach ($fs as $f): $f = (array) $f; ?>
                    <tr>
                        <td><span class="badge <?= e($sev((string) ($f['severity'] ?? ''))) ?>"><?= e(strtoupper((string) ($f['severity'] ?? ''))) ?></span></td>
                        <td class="small"><?= e((string) ($f['issue'] ?? '')) ?></td>
                        <td class="small nim-muted text-break"><?= e((string) ($f['detail'] ?? '')) ?></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    </div>
    <?php endforeach; ?>
<?php else: ?>
    <div class="nim-infobar"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>Run the audit to probe the server's HTTPS and mail endpoints.</span></div>
<?php endif; ?>
