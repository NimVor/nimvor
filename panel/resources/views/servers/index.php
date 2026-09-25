<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $servers */
?>
<?php $this->start(); ?>Servers<?php $this->end('title'); ?>
<?php $this->start(); ?>Manage several servers from this panel<?php $this->end('subtitle'); ?>
<div class="row g-3">
    <div class="col-xl-8">
        <div class="card">
            <div class="table-responsive"><table class="table align-middle mb-0">
                <thead><tr><th>Server</th><th>Agent</th><th>Status</th><th>Websites</th><th class="text-end">Actions</th></tr></thead>
                <tbody>
                <?php foreach ($servers as $s): ?>
                    <tr>
                        <td><strong><?= e($s['name']) ?></strong><div class="small text-body-secondary"><?= $s['kind'] === 'local' ? 'this machine (Unix socket)' : e($s['url']) ?></div>
                            <?php if ($s['kind'] === 'remote'): ?><div class="small text-body-secondary text-break">pin <code><?= e($s['fingerprint']) ?></code></div><?php endif; ?></td>
                        <td class="small"><?= e($s['agent_version']) ?></td>
                        <td><?= $s['kind'] === 'local' ? $this->statusBadge('active', 'local') : $this->statusBadge($s['status'] === 'online' ? 'active' : ($s['status'] === 'version_mismatch' ? 'warning' : 'error'), ['online' => 'online', 'offline' => 'unreachable', 'timeout' => 'timed out', 'auth_failed' => 'authentication failed', 'certificate_mismatch' => 'certificate mismatch', 'refused' => 'refused', 'version_mismatch' => 'version mismatch'][(string) $s['status']] ?? (string) $s['status']) ?>
                            <?php if ($s['last_error'] !== ''): ?><div class="small text-danger"><?= e($s['last_error']) ?></div><?php endif; ?>
                            <?php if ($s['last_seen_at'] !== null): ?><div class="small text-body-secondary">seen <?= e(substr((string) $s['last_seen_at'], 0, 16)) ?></div><?php endif; ?></td>
                        <td><?= (int) $s['websites'] ?></td>
                        <td class="text-end text-nowrap">
                            <?php if ($s['kind'] === 'remote'): ?>
                                <form class="d-inline" method="post" action="<?= e($this->url('servers.check', ['id' => (int) $s['id']])) ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary">Check</button></form>
                                <form class="d-inline" method="post" action="<?= e($this->url('servers.delete', ['id' => (int) $s['id']])) ?>" data-wcp-confirm="Remove <?= e($s['name']) ?> from WhaCP? Nothing is changed on the server itself."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger"><i class="bi bi-trash"></i></button></form>
                            <?php endif; ?>
                        </td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table></div>
        </div>
    </div>
    <div class="col-xl-4">
        <div class="card">
            <div class="card-header"><i class="bi bi-plus-lg me-1"></i>Connect a server</div>
            <div class="card-body">
                <ol class="small ps-3">
                    <li>Install WhaCP's agent on the new server with <code>install.sh --agent-only --remote-access 0.0.0.0:8443 --allow-from <em>this server's IP</em></code>.</li>
                    <li>The installer prints the <strong>pin</strong> and <strong>token</strong>. Compare the pin over a trusted channel (e.g. your SSH session) — it proves you are talking to that machine.</li>
                </ol>
                <form method="post" action="<?= e($this->url('servers.store')) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <?= $this->partial('partials/field', ['name' => 'name', 'label' => 'Name', 'value' => (string) ($old['name'] ?? ''), 'attrs' => ['required' => 'required', 'placeholder' => 'web2']]) ?>
                    <?= $this->partial('partials/field', ['name' => 'url', 'label' => 'Agent address', 'value' => (string) ($old['url'] ?? ''), 'attrs' => ['required' => 'required', 'placeholder' => 'https://203.0.113.10:8443']]) ?>
                    <?= $this->partial('partials/field', ['name' => 'pin', 'label' => 'Certificate pin', 'value' => (string) ($old['pin'] ?? ''), 'attrs' => ['required' => 'required', 'placeholder' => 'sha256//…']]) ?>
                    <?= $this->partial('partials/field', ['name' => 'token', 'label' => 'Agent token', 'type' => 'password', 'value' => '', 'attrs' => ['required' => 'required', 'autocomplete' => 'off']]) ?>
                    <button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Test and connect</button>
                    <div class="form-text">The token is stored encrypted. Requests are refused unless the server presents exactly this pinned key.</div>
                </form>
            </div>
        </div>
    </div>
</div>
