<?php
/**
 * Backups > Remote fetched files: the files fetched from a remote SFTP or FTPS server, and the window that
 * fetches one. Administrators only.
 *
 * @var \NimVor\View\View $this
 * @var list<array<string, mixed>> $fetched
 * @var bool $canManage
 * @var array<string, mixed> $old
 * @var array<string, string> $errors
 */
$running = count(array_filter($fetched, static fn (array $f): bool => $f['status'] === 'running'));
?>
<?php if ($canManage): ?>
<div class="nim-actionbar mb-3 px-0">
    <div class="small text-body-secondary">Files fetched from a remote SFTP or FTPS server onto this server. They are kept in a folder only NimVor's agent writes to. Fetch one, and delete it here when you no longer need it.</div>
    <div class="nim-actionbar-end">
        <button type="button" class="btn btn-sm btn-primary" data-bs-toggle="modal" data-bs-target="#fetch-remote"><i class="bi bi-cloud-download me-1"></i>Fetch a remote file</button>
    </div>
</div>
<?php endif; ?>
<form id="fetched-bulk" method="post" action="<?= e($this->url('backups.fetched.delete')) ?>"><?= $this->csrfField() ?></form>
<div class="nim-panel" data-nim-dt data-fetched-running="<?= $running ?>">
    <div class="nim-actionbar"><div class="fw-semibold">Remote fetched files <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($fetched) ?></span></div>
        <div class="nim-actionbar-end">
            <?php if ($canManage): ?><button class="btn btn-sm btn-outline-danger" form="fetched-bulk" data-fx-bulk data-nim-confirm-submit="Delete the selected files? They are removed from this server and cannot be recovered." disabled><i class="bi bi-trash me-1"></i>Delete selected <span class="badge text-bg-danger d-none" data-fx-count>0</span></button><?php endif; ?>
            <div class="nim-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search fetched files" data-nim-dt-search><i class="bi bi-search" aria-hidden="true"></i></div>
        </div></div>
    <div class="nim-dtable table-responsive">
        <table class="table align-middle" data-fx-table>
            <thead><tr>
                <th class="nim-check"><?php if ($canManage): ?><input class="form-check-input" type="checkbox" data-nim-select-all="ids[]" aria-label="Select all fetched files"><?php endif; ?></th>
                <th>File</th><th>Fetched from</th><th class="text-end">Size</th><th>Fetched</th><th>Status</th><th class="text-end">Operate</th></tr></thead>
            <tbody>
            <?php foreach ($fetched as $f): $fid = (int) $f['id']; $name = (string) ($f['file_name'] !== '' ? $f['file_name'] : basename((string) $f['remote_path'])); ?>
                <tr data-nim-row>
                    <td><?php if ($canManage): ?><input class="form-check-input" type="checkbox" name="ids[]" value="<?= $fid ?>" form="fetched-bulk" data-fx-check<?= $f['status'] === 'running' ? ' disabled title="Still being fetched"' : '' ?> aria-label="Select <?= e($name) ?>"><?php endif; ?></td>
                    <td class="fw-semibold text-break"><?= e($name) ?><?php if ($f['sha256'] !== ''): ?><div class="small nim-muted fw-normal font-monospace text-break" title="SHA-256 of the file">sha256 <?= e((string) $f['sha256']) ?></div><?php endif; ?></td>
                    <td class="small text-break"><span class="badge text-bg-light border"><?= e(strtoupper($f['protocol'] === 'ftp' ? 'ftps' : (string) $f['protocol'])) ?></span> <?= e((string) $f['source']) ?><div class="font-monospace"><?= e((string) $f['remote_path']) ?></div></td>
                    <td class="text-end small text-nowrap"><?= $f['status'] === 'completed' ? e(format_bytes((int) $f['size'])) : '—' ?></td>
                    <td class="small text-nowrap"><?= e(str_replace('T', ' ', substr((string) $f['created_at'], 0, 16))) ?></td>
                    <td class="small">
                        <?php if ($f['status'] === 'completed'): ?><span class="nim-state is-ok">Completed</span>
                        <?php elseif ($f['status'] === 'running'): ?><span class="nim-state is-warn"><span class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>Fetching…</span>
                        <?php else: ?><span class="nim-state is-error">Failed</span><div class="text-danger-emphasis text-break"><?= e((string) $f['error']) ?></div><?php endif; ?>
                    </td>
                    <td class="text-end text-nowrap">
                        <?php if ($canManage): ?>
                            <form class="d-inline" method="post" action="<?= e($this->url('backups.fetched.delete')) ?>" data-nim-confirm="<?= e($f['status'] === 'completed' ? 'Delete the fetched file ' . $name . '? It is removed from this server and cannot be recovered.' : 'Remove this record of a failed fetch?') ?>"><?= $this->csrfField() ?><input type="hidden" name="ids[]" value="<?= $fid ?>"><button class="btn btn-sm btn-outline-danger"<?= $f['status'] === 'running' ? ' disabled title="Wait until the fetch is finished"' : '' ?> aria-label="Delete <?= e($name) ?>"><i class="bi bi-trash"></i></button></form>
                        <?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            <tr class="<?= $fetched === [] ? '' : 'd-none' ?>" data-nim-dt-empty><td colspan="7" class="text-center py-4 nim-muted">No file fetched yet. Use “Fetch a remote file” to copy a file from an SFTP or FTPS server to this server.</td></tr>
            </tbody>
        </table>
    </div>
    <div class="nim-pager" data-nim-dt-pager></div>
</div>
