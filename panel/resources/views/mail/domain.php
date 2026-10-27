<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $domain
 * @var list<array<string, mixed>> $mailboxes
 * @var list<array<string, mixed>> $aliases
 * @var list<array<string, mixed>> $records
 * @var array<string, int> $sizes
 * @var string $host
 * @var array<string, mixed> $webmail
 * @var int $edit
 * @var array<string, mixed>|null $site
 */
$id = (int) $domain['id'];
$dom = (string) $domain['domain'];
$errors ??= [];
$old ??= [];
$webmail ??= [];
$site ??= null;
$openMailbox = isset($errors['local']) || isset($errors['password']);
$openAlias = isset($errors['alias_local']) || isset($errors['destinations']);
$webmailUrl = !empty($webmail['enabled']) && !empty($webmail['hostname']) ? 'https://' . $webmail['hostname'] . '/' : null;
?>
<?php if ($site !== null): ?>
    <?= $this->partial('sites/header', ['site' => $site, 'tab' => 'mail']) ?>
<?php else: ?>
    <?php $this->start(); ?><span class="font-monospace"><?= e($dom) ?></span><?php $this->end('title'); ?>
    <?php $this->start(); ?>Email domain<?php $this->end('subtitle'); ?>

<?php endif; ?>
<?= $this->partial('partials/secret', ['secret' => $secret ?? null]) ?>

<div class="d-flex flex-wrap align-items-center gap-3 mb-3 small">
    <span class="text-body-secondary">Email for <code><?= e($dom) ?></code></span>
    <?php if ($webmailUrl !== null): ?><a href="<?= e($webmailUrl) ?>" target="_blank" rel="noopener"><i class="bi bi-box-arrow-up-right me-1"></i>Open webmail</a><?php endif; ?>
    <a href="#mail-clients"><i class="bi bi-phone me-1"></i>Mail app settings</a>
    <a href="#mail-dns"><i class="bi bi-patch-check me-1"></i>Email authentication</a>
</div>

<div class="card mb-3" id="mailboxes">
    <div class="card-header wcp-toolbar">
        <h2 class="wcp-toolbar-title"><i class="bi bi-inbox me-1"></i>Mailboxes <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($mailboxes) ?></span></h2>
        <?php if (count($mailboxes) > 5): ?><input class="form-control form-control-sm wcp-filter" type="search" placeholder="Filter mailboxes" aria-label="Filter mailboxes" data-wcp-filter="#mailbox-table"><?php endif; ?>
        <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#new-mailbox"><i class="bi bi-plus-lg me-1"></i>Create mailbox</button>
    </div>
    <?= $this->modalStart('new-mailbox', 'Create mailbox', $openMailbox) ?>
        <form method="post" action="<?= e($this->url('mail.mailboxes.store', ['id' => $id])) ?>" class="p-3" data-wcp-busy>
            <?= $this->csrfField() ?>
            <div class="row g-2 align-items-start">
                <div class="col-md-5">
                    <label class="form-label small" for="mb-local">Address</label>
                    <div class="input-group input-group-sm"><input class="form-control<?= isset($errors['local']) ? ' is-invalid' : '' ?>" id="mb-local" name="local" value="<?= e((string) ($old['local'] ?? '')) ?>" placeholder="info" required autocomplete="off"><span class="input-group-text">@<?= e($dom) ?></span></div>
                    <?php if (isset($errors['local'])): ?><div class="invalid-feedback d-block"><?= e($errors['local']) ?></div><?php endif; ?>
                </div>
                <div class="col-md-3"><label class="form-label small" for="mb-pw">Password</label><input class="form-control form-control-sm<?= isset($errors['password']) ? ' is-invalid' : '' ?>" id="mb-pw" type="password" name="password" minlength="10" autocomplete="new-password"></div>
                <div class="col-md-2"><label class="form-label small" for="mb-quota">Quota (MB)</label><input class="form-control form-control-sm" id="mb-quota" type="number" min="0" name="quota_mb" value="<?= e((string) ($old['quota_mb'] ?? '1024')) ?>"></div>
                <div class="col-md-2"><label class="form-label small d-none d-md-block">&nbsp;</label><button class="btn btn-sm btn-primary w-100" type="submit">Create</button></div>
                <div class="col-12"><div class="form-check small"><input class="form-check-input" type="checkbox" name="generate" value="1" id="mb-gen"><label class="form-check-label" for="mb-gen">Generate a strong password (shown once)</label></div>
                    <?php if (isset($errors['password'])): ?><div class="text-danger small"><?= e($errors['password']) ?></div><?php endif; ?></div>
            </div>
        </form>
    <?= $this->modalEnd() ?>
    <?php if ($mailboxes === []): ?>
        <div class="wcp-empty-state"><i class="bi bi-inbox" aria-hidden="true"></i><div class="fw-medium">No mailboxes yet</div><div class="small">Create an address like info@<?= e($dom) ?> with its own inbox.</div></div>
    <?php else: ?>
    <div class="table-responsive">
        <table class="table mb-0 wcp-table align-middle" id="mailbox-table">
            <thead><tr><th>Address</th><th>Usage</th><th class="text-end">Quota</th><th>Status</th><th class="text-end">Actions</th></tr></thead>
            <tbody>
            <?php foreach ($mailboxes as $m): $addr = $m['local'] . '@' . $dom; $used = (int) ($sizes[$addr] ?? 0); $quota = (int) $m['quota_mb'] * 1048576; $pct = $quota > 0 ? min(100, (int) round($used * 100 / $quota)) : null; ?>
                <?php if ($edit === (int) $m['id']): ?>
                <tr><td colspan="5" class="wcp-form-panel">
                    <form method="post" action="<?= e($this->url('mail.mailboxes.update', ['id' => $id, 'mailbox' => (int) $m['id']])) ?>" class="row g-2 align-items-end" data-wcp-busy>
                        <?= $this->csrfField() ?>
                        <div class="col-md-4"><span class="form-label small d-block">Address</span><div class="form-control-plaintext font-monospace small"><?= e($addr) ?></div></div>
                        <div class="col-md-3"><label class="form-label small" for="mb-q">Quota (MB, 0 = none)</label><input class="form-control form-control-sm" id="mb-q" type="number" min="0" name="quota_mb" value="<?= (int) $m['quota_mb'] ?>"></div>
                        <div class="col-md-3"><label class="form-label small" for="mb-p">New password</label><input class="form-control form-control-sm" id="mb-p" type="password" name="password" minlength="10" autocomplete="new-password"></div>
                        <div class="col-md-2"><button class="btn btn-sm btn-primary w-100">Save</button></div>
                        <div class="col-12 d-flex gap-3"><div class="form-check small"><input class="form-check-input" type="checkbox" name="generate" value="1" id="mb-g"><label class="form-check-label" for="mb-g">Generate a new password (shown once)</label></div>
                            <a class="small" href="<?= e($this->url('mail.domain', ['id' => $id])) ?>">Cancel</a></div>
                    </form>
                </td></tr>
                <?php else: ?>
                <tr>
                    <td class="font-monospace"><?= e($addr) ?></td>
                    <td class="small wcp-usage">
                        <?= e(format_bytes($used)) ?>
                        <?php if ($pct !== null): ?><div class="progress mt-1" role="progressbar" aria-label="Mailbox usage" aria-valuenow="<?= $pct ?>" aria-valuemin="0" aria-valuemax="100"><div class="progress-bar<?= $pct >= 90 ? ' bg-danger' : '' ?> wcp-w-<?= (int) (ceil($pct / 5) * 5) ?>"></div></div><?php endif; ?>
                    </td>
                    <td class="text-end small"><?= $quota > 0 ? e(format_bytes($quota)) : 'Unlimited' ?></td>
                    <td class="small"><?php if ($pct !== null && $pct >= 100): ?><span class="wcp-state is-error">Full</span><?php elseif ($pct !== null && $pct >= 90): ?><span class="wcp-state is-warn">Almost full</span><?php else: ?><span class="wcp-state is-ok">Active</span><?php endif; ?></td>
                    <td class="text-end text-nowrap">
                        <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('mail.filters', ['id' => $id, 'mailbox' => (int) $m['id']])) ?>" title="Filters and autoresponder" aria-label="Filters and autoresponder of <?= e($addr) ?>"><i class="bi bi-funnel"></i></a>
                        <a class="btn btn-sm btn-outline-secondary" href="?edit=<?= (int) $m['id'] ?>" title="Change quota or password" aria-label="Change <?= e($addr) ?>"><i class="bi bi-pencil"></i></a>
                        <form class="d-inline" method="post" action="<?= e($this->url('mail.mailboxes.delete', ['id' => $id, 'mailbox' => (int) $m['id']])) ?>" data-wcp-confirm="Delete the mailbox <?= e($addr) ?>? Its mail is moved to the server's trash folder." data-wcp-confirm-word="<?= e($addr) ?>"><?= $this->csrfField() ?><input type="hidden" name="confirm" value="<?= e($addr) ?>"><button class="btn btn-sm btn-outline-danger" title="Delete" aria-label="Delete <?= e($addr) ?>"><i class="bi bi-trash"></i></button></form>
                    </td>
                </tr>
                <?php endif; ?>
            <?php endforeach; ?>
            <tr class="d-none" data-wcp-filter-empty><td colspan="5" class="text-body-secondary small">No mailbox matches the filter.</td></tr>
            </tbody>
        </table>
    </div>
    <?php endif; ?>
</div>

<?php $routing = (string) ($domain['routing'] ?? 'local'); ?>
<div class="card mb-3" id="routing">
    <div class="card-body d-flex flex-wrap align-items-center gap-3">
        <div class="me-auto"><div class="fw-semibold"><i class="bi bi-diagram-2 me-1"></i>Mail routing</div>
            <div class="small text-body-secondary"><?= e(['local' => 'This server receives the domain\'s mail into its mailboxes.', 'backup' => 'This server only queues the domain\'s mail for its primary mail server (a lower-priority MX).', 'remote' => 'Another server receives the domain\'s mail; mail from this server to the domain goes there.'][$routing] ?? '') ?></div></div>
        <form class="d-flex gap-2" method="post" action="<?= e($this->url('mail.routing', ['id' => $id])) ?>" data-wcp-busy data-wcp-confirm="Change the mail routing of <?= e($dom) ?>? With backup or remote routing, mail for the domain is no longer delivered to the mailboxes here.">
            <?= $this->csrfField() ?>
            <select class="form-select form-select-sm" name="routing" aria-label="Mail routing">
                <?php foreach (\WhaCP\Services\MailFilterService::ROUTING as $k => $l): ?><option value="<?= e($k) ?>"<?= $routing === $k ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?>
            </select>
            <button class="btn btn-sm btn-outline-primary">Change</button>
        </form>
    </div>
</div>

<div class="card mb-3" id="aliases">
    <div class="card-header wcp-toolbar">
        <h2 class="wcp-toolbar-title"><i class="bi bi-signpost-split me-1"></i>Aliases and forwarders <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($aliases) ?></span></h2>
        <button class="btn btn-sm btn-outline-primary" type="button" data-bs-toggle="modal" data-bs-target="#new-alias"><i class="bi bi-plus-lg me-1"></i>Add forwarder</button>
    </div>
    <?= $this->modalStart('new-alias', 'Add forwarder', $openAlias) ?>
        <form method="post" action="<?= e($this->url('mail.aliases.store', ['id' => $id])) ?>" class="row g-2 align-items-start p-3" data-wcp-busy>
            <?= $this->csrfField() ?>
            <div class="col-md-5">
                <label class="form-label small" for="al-local">Address (* = all other addresses)</label>
                <div class="input-group input-group-sm"><input class="form-control<?= isset($errors['alias_local']) ? ' is-invalid' : '' ?>" id="al-local" name="alias_local" value="<?= e((string) ($old['alias_local'] ?? '')) ?>" placeholder="sales" required><span class="input-group-text">@<?= e($dom) ?></span></div>
                <?php if (isset($errors['alias_local'])): ?><div class="invalid-feedback d-block"><?= e($errors['alias_local']) ?></div><?php endif; ?>
            </div>
            <div class="col-md-5"><label class="form-label small" for="al-dest">Deliver to (addresses, comma separated)</label><input class="form-control form-control-sm<?= isset($errors['destinations']) ? ' is-invalid' : '' ?>" id="al-dest" name="destinations" required placeholder="info@<?= e($dom) ?>, me@gmail.com">
                <?php if (isset($errors['destinations'])): ?><div class="invalid-feedback d-block"><?= e($errors['destinations']) ?></div><?php endif; ?></div>
            <div class="col-md-2"><label class="form-label small d-none d-md-block">&nbsp;</label><button class="btn btn-sm btn-primary w-100" type="submit">Save</button></div>
        </form>
    <?= $this->modalEnd() ?>
    <?php if ($aliases === []): ?>
        <div class="wcp-empty-state"><i class="bi bi-signpost-split" aria-hidden="true"></i><div class="fw-medium">No forwarders</div><div class="small">Forward an address, e.g. sales@<?= e($dom) ?>, to one or more mailboxes.</div></div>
    <?php else: ?>
    <div class="table-responsive">
        <table class="table mb-0 wcp-table align-middle">
            <thead><tr><th>Address</th><th>Delivers to</th><th class="text-end">Actions</th></tr></thead>
            <tbody>
            <?php foreach ($aliases as $a): ?>
                <tr>
                    <td class="font-monospace"><?= $a['local'] === '' ? '<span class="badge text-bg-light border">catch-all</span> *@' . e($dom) : e($a['local'] . '@' . $dom) ?></td>
                    <td class="small font-monospace text-break"><?= e((string) $a['destinations']) ?></td>
                    <td class="text-end"><form method="post" action="<?= e($this->url('mail.aliases.delete', ['id' => $id, 'alias' => (int) $a['id']])) ?>" data-wcp-confirm="Delete this forwarder?"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" title="Delete" aria-label="Delete forwarder"><i class="bi bi-trash"></i></button></form></td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
    <?php endif; ?>
</div>

<div class="row g-3">
    <div class="col-xl-7">
        <div class="card mb-3" id="mail-dns">
            <div class="card-header wcp-toolbar"><h2 class="wcp-toolbar-title"><i class="bi bi-patch-check me-1"></i>Email authentication (DNS)</h2>
                <div class="d-flex gap-1"><button class="btn btn-sm btn-outline-secondary" type="button" data-wcp-dns-check="<?= e($this->url('mail.dns.check', ['id' => $id])) ?>" title="Look up the published records and test them against this server"><i class="bi bi-clipboard2-check me-1"></i>Check DNS</button>
                <form method="post" action="<?= e($this->url('mail.dns', ['id' => $id])) ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-primary" title="Add the missing records to this domain's zone on this panel">Add to DNS zone</button></form></div></div>
            <div class="card-body small">
                <div class="mb-3" data-wcp-dns-results aria-live="polite" hidden></div>
                <p class="text-body-secondary">Publish these records where the domain's DNS is hosted, so mail reaches this server and other providers trust it (MX, SPF, DKIM, DMARC).</p>
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
    </div>
    <div class="col-xl-5">
        <div class="card mb-3" id="mail-clients">
            <div class="card-header"><i class="bi bi-phone me-1"></i>Mail app settings</div>
            <div class="card-body small">
                <dl class="row mb-0">
                    <dt class="col-4">User name</dt><dd class="col-8">the full address</dd>
                    <dt class="col-4">Incoming</dt><dd class="col-8">IMAP <code><?= e($host) ?></code> port 993, SSL/TLS</dd>
                    <dt class="col-4">Outgoing</dt><dd class="col-8">SMTP <code><?= e($host) ?></code> port 587, STARTTLS, with sign-in</dd>
                    <?php if ($webmailUrl !== null): ?>
                    <dt class="col-4">Webmail</dt><dd class="col-8 mb-0"><a href="<?= e($webmailUrl) ?>" target="_blank" rel="noopener"><?= e($webmailUrl) ?></a></dd>
                    <?php endif; ?>
                </dl>
            </div>
        </div>
<?php if ($this->serverScope()): ?>
        <div class="card wcp-danger-card">
            <div class="card-header"><i class="bi bi-exclamation-octagon me-1"></i>Danger zone</div>
            <div class="card-body small">
                <p class="text-body-secondary">Stop hosting email for <?= e($dom) ?>. Delete the mailboxes and forwarders first.</p>
                <form method="post" action="<?= e($this->url('mail.delete', ['id' => $id])) ?>" data-wcp-confirm="Stop hosting email for <?= e($dom) ?>?" data-wcp-confirm-word="<?= e($dom) ?>">
                    <?= $this->csrfField() ?><input type="hidden" name="confirm" value="<?= e($dom) ?>">
                    <button class="btn btn-sm btn-outline-danger"<?= $mailboxes !== [] || $aliases !== [] ? ' disabled' : '' ?>>Stop hosting email</button>
                </form>
            </div>
        </div>
<?php endif; ?>
    </div>
</div>
