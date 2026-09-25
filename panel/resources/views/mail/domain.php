<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $domain
 * @var list<array<string, mixed>> $mailboxes
 * @var list<array<string, mixed>> $aliases
 * @var list<array<string, mixed>> $records
 * @var array<string, int> $sizes
 * @var string $host
 * @var int $edit
 */
$id = (int) $domain['id'];
$dom = (string) $domain['domain'];
$errors ??= [];
$old ??= [];
?>
<?php $this->start(); ?><span class="font-monospace"><?= e($dom) ?></span><?php $this->end('title'); ?>
<?php $this->start(); ?>Email domain<?php $this->end('subtitle'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('mail')) ?>"><i class="bi bi-arrow-left"></i> Email</a><?php $this->end('actions'); ?>
<?= $this->partial('partials/secret', ['secret' => $secret ?? null]) ?>
<div class="row g-3">
    <div class="col-xl-7">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-inbox me-1"></i>Mailboxes</div>
            <div class="table-responsive">
                <table class="table mb-0 wcp-table align-middle">
                    <thead><tr><th>Address</th><th class="text-end">Used</th><th class="text-end">Quota</th><th></th></tr></thead>
                    <tbody>
                    <?php foreach ($mailboxes as $m): $addr = $m['local'] . '@' . $dom; ?>
                        <?php if ($edit === (int) $m['id']): ?>
                        <tr><td colspan="4">
                            <form method="post" action="<?= e($this->url('mail.mailboxes.update', ['id' => $id, 'mailbox' => (int) $m['id']])) ?>" class="row g-2 align-items-end" data-wcp-busy>
                                <?= $this->csrfField() ?>
                                <div class="col-md-4"><label class="form-label small">Address</label><div class="form-control-plaintext font-monospace small"><?= e($addr) ?></div></div>
                                <div class="col-md-3"><label class="form-label small" for="mb-q">Quota (MB, 0 = none)</label><input class="form-control form-control-sm" id="mb-q" type="number" min="0" name="quota_mb" value="<?= (int) $m['quota_mb'] ?>"></div>
                                <div class="col-md-3"><label class="form-label small" for="mb-p">New password</label><input class="form-control form-control-sm" id="mb-p" type="password" name="password" minlength="10" autocomplete="new-password"></div>
                                <div class="col-md-2"><button class="btn btn-sm btn-primary w-100">Save</button></div>
                                <div class="col-12"><div class="form-check small"><input class="form-check-input" type="checkbox" name="generate" value="1" id="mb-g"><label class="form-check-label" for="mb-g">Generate a new password (shown once)</label></div></div>
                            </form>
                        </td></tr>
                        <?php else: ?>
                        <tr>
                            <td class="font-monospace"><?= e($addr) ?></td>
                            <td class="text-end small"><?= e(format_bytes((int) ($sizes[$addr] ?? 0))) ?></td>
                            <td class="text-end small"><?= (int) $m['quota_mb'] > 0 ? e(format_bytes((int) $m['quota_mb'] * 1048576)) : '—' ?></td>
                            <td class="text-end text-nowrap">
                                <a class="btn btn-sm btn-outline-secondary" href="?edit=<?= (int) $m['id'] ?>" title="Change"><i class="bi bi-pencil"></i></a>
                                <form class="d-inline" method="post" action="<?= e($this->url('mail.mailboxes.delete', ['id' => $id, 'mailbox' => (int) $m['id']])) ?>" data-wcp-confirm="Delete the mailbox <?= e($addr) ?>? Its mail is moved to the server's trash folder." data-wcp-confirm-word="<?= e($addr) ?>"><?= $this->csrfField() ?><input type="hidden" name="confirm" value="<?= e($addr) ?>"><button class="btn btn-sm btn-outline-danger" title="Delete"><i class="bi bi-trash"></i></button></form>
                            </td>
                        </tr>
                        <?php endif; ?>
                    <?php endforeach; ?>
                    <?php if ($mailboxes === []): ?><tr><td colspan="4" class="text-body-secondary">No mailboxes yet.</td></tr><?php endif; ?>
                    </tbody>
                </table>
            </div>
            <div class="card-body border-top">
                <form method="post" action="<?= e($this->url('mail.mailboxes.store', ['id' => $id])) ?>" class="row g-2 align-items-start" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <div class="col-md-5">
                        <label class="form-label small" for="mb-local">New mailbox</label>
                        <div class="input-group input-group-sm"><input class="form-control<?= isset($errors['local']) ? ' is-invalid' : '' ?>" id="mb-local" name="local" value="<?= e((string) ($old['local'] ?? '')) ?>" placeholder="info" required autocomplete="off"><span class="input-group-text">@<?= e($dom) ?></span></div>
                        <?php if (isset($errors['local'])): ?><div class="invalid-feedback d-block"><?= e($errors['local']) ?></div><?php endif; ?>
                    </div>
                    <div class="col-md-3"><label class="form-label small" for="mb-pw">Password</label><input class="form-control form-control-sm<?= isset($errors['password']) ? ' is-invalid' : '' ?>" id="mb-pw" type="password" name="password" minlength="10" autocomplete="new-password"></div>
                    <div class="col-md-2"><label class="form-label small" for="mb-quota">Quota MB</label><input class="form-control form-control-sm" id="mb-quota" type="number" min="0" name="quota_mb" value="<?= e((string) ($old['quota_mb'] ?? '1024')) ?>"></div>
                    <div class="col-md-2"><label class="form-label small d-none d-md-block">&nbsp;</label><button class="btn btn-sm btn-primary w-100">Create</button></div>
                    <div class="col-12"><div class="form-check small"><input class="form-check-input" type="checkbox" name="generate" value="1" id="mb-gen"><label class="form-check-label" for="mb-gen">Generate a strong password (shown once)</label></div>
                        <?php if (isset($errors['password'])): ?><div class="text-danger small"><?= e($errors['password']) ?></div><?php endif; ?></div>
                </form>
            </div>
            <?php if ($host !== ''): ?>
            <div class="card-footer small"><strong>Mail app settings:</strong> user name = the full address · incoming IMAP <code><?= e($host) ?></code> port 993 (SSL/TLS) · outgoing SMTP <code><?= e($host) ?></code> port 587 (STARTTLS), with the same user name and password.</div>
            <?php endif; ?>
        </div>
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-signpost-split me-1"></i>Aliases and forwarding</div>
            <div class="table-responsive">
                <table class="table mb-0 wcp-table align-middle">
                    <thead><tr><th>Address</th><th>Delivers to</th><th></th></tr></thead>
                    <tbody>
                    <?php foreach ($aliases as $a): ?>
                        <tr>
                            <td class="font-monospace"><?= $a['local'] === '' ? '<span class="badge text-bg-light border">catch-all</span> *@' . e($dom) : e($a['local'] . '@' . $dom) ?></td>
                            <td class="small font-monospace text-break"><?= e((string) $a['destinations']) ?></td>
                            <td class="text-end"><form method="post" action="<?= e($this->url('mail.aliases.delete', ['id' => $id, 'alias' => (int) $a['id']])) ?>" data-wcp-confirm="Delete this alias?"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" title="Delete"><i class="bi bi-trash"></i></button></form></td>
                        </tr>
                    <?php endforeach; ?>
                    <?php if ($aliases === []): ?><tr><td colspan="3" class="text-body-secondary">No aliases.</td></tr><?php endif; ?>
                    </tbody>
                </table>
            </div>
            <div class="card-body border-top">
                <form method="post" action="<?= e($this->url('mail.aliases.store', ['id' => $id])) ?>" class="row g-2 align-items-start" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <div class="col-md-5">
                        <label class="form-label small" for="al-local">Address (* = all others)</label>
                        <div class="input-group input-group-sm"><input class="form-control<?= isset($errors['alias_local']) ? ' is-invalid' : '' ?>" id="al-local" name="alias_local" value="<?= e((string) ($old['alias_local'] ?? '')) ?>" placeholder="sales" required><span class="input-group-text">@<?= e($dom) ?></span></div>
                        <?php if (isset($errors['alias_local'])): ?><div class="invalid-feedback d-block"><?= e($errors['alias_local']) ?></div><?php endif; ?>
                    </div>
                    <div class="col-md-5"><label class="form-label small" for="al-dest">Deliver to (addresses, comma separated)</label><input class="form-control form-control-sm<?= isset($errors['destinations']) ? ' is-invalid' : '' ?>" id="al-dest" name="destinations" required placeholder="info@<?= e($dom) ?>, me@gmail.com">
                        <?php if (isset($errors['destinations'])): ?><div class="invalid-feedback d-block"><?= e($errors['destinations']) ?></div><?php endif; ?></div>
                    <div class="col-md-2"><label class="form-label small d-none d-md-block">&nbsp;</label><button class="btn btn-sm btn-primary w-100">Save</button></div>
                </form>
            </div>
        </div>
    </div>
    <div class="col-xl-5">
        <div class="card mb-3">
            <div class="card-header d-flex align-items-center"><i class="bi bi-signpost me-1"></i>DNS records
                <form method="post" action="<?= e($this->url('mail.dns', ['id' => $id])) ?>" class="ms-auto"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-primary" title="Add the missing records to this domain's zone on this panel">Add to DNS zone</button></form></div>
            <div class="card-body small">
                <p class="text-body-secondary">Publish these records where the domain's DNS is hosted, so mail reaches this server and is trusted by others.</p>
                <?php foreach ($records as $r): ?>
                    <div class="border rounded p-2 mb-2">
                        <div class="d-flex gap-2"><span class="badge text-bg-light border"><?= e($r['type']) ?></span><code><?= e($r['name'] === '@' ? $dom : $r['name'] . '.' . $dom) ?></code></div>
                        <code class="d-block text-break mt-1 user-select-all"><?= $r['type'] === 'MX' ? (int) $r['priority'] . ' ' : '' ?><?= e($r['value']) ?></code>
                        <div class="text-body-secondary mt-1"><?= e($r['note']) ?></div>
                    </div>
                <?php endforeach; ?>
                <p class="text-body-secondary mb-0">Also ask your hosting provider to set the reverse DNS (PTR) of this server's IP to <code><?= e($host) ?></code>.</p>
            </div>
        </div>
        <div class="card">
            <div class="card-header"><i class="bi bi-trash me-1"></i>Stop hosting email</div>
            <div class="card-body small">
                <p class="text-body-secondary">Delete the mailboxes and aliases first.</p>
                <form method="post" action="<?= e($this->url('mail.delete', ['id' => $id])) ?>" data-wcp-confirm="Stop hosting email for <?= e($dom) ?>?" data-wcp-confirm-word="<?= e($dom) ?>">
                    <?= $this->csrfField() ?><input type="hidden" name="confirm" value="<?= e($dom) ?>">
                    <button class="btn btn-sm btn-outline-danger"<?= $mailboxes !== [] || $aliases !== [] ? ' disabled' : '' ?>>Remove domain</button>
                </form>
            </div>
        </div>
    </div>
</div>
