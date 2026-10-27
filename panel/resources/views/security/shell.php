<?php
/** @var \NimVor\View\View $this */
/** @var array{accounts: list<array<string, mixed>>, jail_problem: string}|null $status */
$canManage = $this->can(\NimVor\Security\Permissions::SECURITY_MANAGE) && ($user['role_name'] ?? '') === 'admin';
$labels = ['disabled' => ['Disabled', 'is-off'], 'jailed' => ['Jailed', 'is-ok'], 'normal' => ['Normal', 'is-warn']];
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($status === null) { return; } ?>

<div class="nim-panel mb-3">
    <div class="row g-3 small">
        <div class="col-lg-4"><span class="nim-state is-off">Disabled</span> <span class="fw-medium">(default)</span> — SFTP only, locked in the websites folder. No commands.</div>
        <div class="col-lg-4"><span class="nim-state is-ok">Jailed</span> — an SSH shell in a sandbox: only the account's own website folder is writable, the system is read-only, other accounts' files and processes are invisible, no privilege escalation. SFTP runs in the sandbox too.</div>
        <div class="col-lg-4"><span class="nim-state is-warn">Normal</span> — a full shell on the server as the account. It can see other processes, <code>/tmp</code> and files readable by everyone. Give it only to trusted accounts.</div>
    </div>
    <div class="small text-body-secondary mt-2">A mode applies while the account's SSH/SFTP login is on (keys or password, on the website's FTP / SFTP page).</div>
</div>
<?php if ($status['jail_problem'] !== ''): ?>
    <div class="nim-infobar mb-3"><i class="bi bi-exclamation-triangle-fill" aria-hidden="true"></i><span>Jailed shells are not available: <?= e($status['jail_problem']) ?></span></div>
<?php endif; ?>

<?php if ($canManage): ?>
<div class="nim-panel mb-3">
    <form class="d-flex flex-wrap align-items-end gap-2" method="post" action="<?= e($this->url('security.shell.update')) ?>" data-nim-busy data-nim-confirm="Change the shell access of every website account on this server?" data-nim-confirm-word="all">
        <?= $this->csrfField() ?><input type="hidden" name="scope" value="all"><input type="hidden" name="confirm" value="all">
        <div><label class="form-label small mb-1" for="sh-all">Change every account to</label>
            <select class="form-select form-select-sm" id="sh-all" name="mode"><option value="disabled">Disabled</option><option value="jailed">Jailed</option><option value="normal">Normal (full shell)</option></select></div>
        <button class="btn btn-sm btn-outline-danger">Apply to all accounts</button>
    </form>
</div>
<?php endif; ?>

<div class="nim-panel" data-nim-dt id="shell-table">
    <div class="nim-actionbar">
        <div class="fw-semibold">Website accounts</div>
        <input class="form-control form-control-sm ms-auto nim-w-30" type="search" placeholder="Search" aria-label="Search accounts" data-nim-dt-search>
    </div>
    <div class="nim-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Account</th><th>Websites</th><th>SSH/SFTP login</th><th>Shell</th><?php if ($canManage): ?><th>Change</th><?php endif; ?></tr></thead>
            <tbody>
            <?php foreach ($status['accounts'] as $a): [$label, $cls] = $labels[$a['effective']] ?? [$a['effective'], '']; ?>
                <tr>
                    <td><span class="fw-semibold font-monospace"><?= e($a['user']) ?></span><?php if ($a['owner'] !== null): ?><div class="small text-body-secondary"><?= e($a['owner']) ?></div><?php endif; ?></td>
                    <td class="small"><?= $a['domains'] === [] ? e($a['folder']) : e(implode(', ', $a['domains'])) ?></td>
                    <td class="small"><?= !empty($a['ssh_login']) ? 'On (' . (int) $a['keys'] . ' key' . ((int) $a['keys'] === 1 ? '' : 's') . (!empty($a['password_login']) ? ', password' : '') . ')' : '<span class="text-body-secondary">Off</span>' ?></td>
                    <td><span class="nim-state <?= e($cls) ?>"><?= e($label) ?></span><?php if ($a['mode'] !== $a['effective']): ?><div class="small text-body-secondary">Set to <?= e($a['mode']) ?>; applies when SSH/SFTP login is on</div><?php endif; ?></td>
                    <?php if ($canManage): ?>
                        <td>
                            <form class="d-flex gap-1" method="post" action="<?= e($this->url('security.shell.update')) ?>" data-nim-busy>
                                <?= $this->csrfField() ?><input type="hidden" name="users[]" value="<?= e($a['user']) ?>">
                                <select class="form-select form-select-sm" name="mode" aria-label="Shell for <?= e($a['user']) ?>">
                                    <?php foreach (['disabled' => 'Disabled', 'jailed' => 'Jailed', 'normal' => 'Normal'] as $k => $l): ?><option value="<?= e($k) ?>"<?= $a['mode'] === $k ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?>
                                </select>
                                <button class="btn btn-sm btn-outline-primary">Set</button>
                            </form>
                        </td>
                    <?php endif; ?>
                </tr>
            <?php endforeach; ?>
            <?php if ($status['accounts'] === []): ?><tr><td colspan="5" class="text-center py-4 nim-muted">No website accounts yet.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
</div>
