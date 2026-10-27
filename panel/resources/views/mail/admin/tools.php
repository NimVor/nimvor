<?php
/** @var \WhaCP\View\View $this */
/** @var string $address */
/** @var array<string, mixed>|null $trace */
/** @var list<string> $domains */
$canManage = $this->can(\WhaCP\Security\Permissions::SERVER_MANAGE);
$icons = ['mailbox' => 'bi-inbox text-success', 'forward' => 'bi-arrow-return-right', 'catchall' => 'bi-asterisk', 'remote' => 'bi-box-arrow-up-right', 'backup' => 'bi-hdd-stack', 'external' => 'bi-globe2', 'rejected' => 'bi-x-octagon text-danger', 'loop' => 'bi-arrow-repeat text-warning'];
?>
<?= $this->partial('mail/admin/_tabs', get_defined_vars()) ?>
<div class="row g-3">
    <div class="col-xl-7">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-signpost-split me-1"></i>Mail troubleshooter</div>
            <div class="card-body">
                <form class="d-flex gap-2 mb-3" method="get" action="<?= e($this->url('mail.admin.tools')) ?>">
                    <label class="visually-hidden" for="trace-address">Email address</label>
                    <input class="form-control" id="trace-address" type="email" name="address" value="<?= e($address) ?>" placeholder="someone@example.com" required maxlength="254">
                    <button class="btn btn-primary text-nowrap">Trace</button>
                </form>
                <p class="small text-body-secondary">Shows where mail sent to an address ends up on this server: its mailbox, forwarders, catch-all, the domain's routing, or the server it is sent on to.</p>
                <?php if ($trace !== null): ?>
                    <ol class="list-unstyled mb-0">
                        <?php foreach ((array) $trace['steps'] as $s): ?>
                            <li class="d-flex gap-2 mb-2 wcp-trace-<?= min(5, (int) $s['depth']) ?>">
                                <i class="bi <?= e($icons[$s['result']] ?? 'bi-dot') ?> mt-1" aria-hidden="true"></i>
                                <div><div class="font-monospace small fw-semibold"><?= e((string) $s['address']) ?></div><div class="small"><?= e((string) $s['detail']) ?></div></div>
                            </li>
                        <?php endforeach; ?>
                    </ol>
                <?php endif; ?>
            </div>
        </div>
    </div>
    <div class="col-xl-5">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-wrench-adjustable me-1"></i>Repair mailbox permissions</div>
            <div class="card-body">
                <p class="small text-body-secondary">Gives mailbox folders back to the mail user (vmail) and removes access for other users, e.g. after files were copied in by hand. Links and special files are skipped, never followed.</p>
                <?php if ($canManage): ?>
                    <form class="d-flex gap-2" method="post" action="<?= e($this->url('mail.admin.repair')) ?>" data-wcp-busy data-wcp-confirm="Repair the ownership and permissions of the selected mailboxes?">
                        <?= $this->csrfField() ?>
                        <label class="visually-hidden" for="repair-domain">Domain</label>
                        <select class="form-select form-select-sm" id="repair-domain" name="domain">
                            <option value="">All domains</option>
                            <?php foreach ($domains as $d): ?><option value="<?= e($d) ?>"><?= e($d) ?></option><?php endforeach; ?>
                        </select>
                        <button class="btn btn-sm btn-outline-primary text-nowrap"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Repair</button>
                    </form>
                <?php endif; ?>
            </div>
        </div>
        <div class="card">
            <div class="card-header"><i class="bi bi-info-circle me-1"></i>Other tools</div>
            <div class="card-body small">
                <ul class="mb-0 ps-3">
                    <li>Spam filter (Rspamd) state and restart: <a href="<?= e($this->url('mail.admin.server')) ?>">Mail server</a>; its settings: <a href="<?= e($this->url('mail')) ?>">Email</a> → spam filter.</li>
                    <li>Mailbox format: WhaCP stores every mailbox as Maildir, so there is nothing to convert.</li>
                    <li>DNS of a domain (MX, SPF, DKIM, DMARC, reverse DNS): the domain's page under Email → Check DNS.</li>
                </ul>
            </div>
        </div>
    </div>
</div>
