<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $status */
$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE) && ($user['role_name'] ?? '') === 'admin';
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($status === null) { return; } ?>
<?php
$s = (array) ($status['egress'] ?? []);
$meta = !empty($s['metadata_blocked']);
$private = !empty($s['block_private']);
$tcp = implode(', ', array_map('strval', (array) ($s['tcp_ports'] ?? [])));
$udp = implode(', ', array_map('strval', (array) ($s['udp_ports'] ?? [])));
$limited = $meta || $private || $tcp !== '' || $udp !== '';
?>

<div class="wcp-panel mb-3">
    <p class="mb-2">Limits the connections that websites' code (PHP, cron jobs, commands run as the website account) can open to other machines. Root, the mail server and the system's services are not affected, and connections to this server itself always work.</p>
    <div class="d-flex flex-wrap align-items-center gap-2 mb-2">
        <span class="fw-medium">Status:</span>
        <?php if ($limited && !empty($s['active'])): ?><span class="wcp-state is-ok">Active</span>
        <?php elseif ($limited): ?><span class="wcp-state is-warn">Saved, rules not loaded</span>
        <?php else: ?><span class="wcp-state is-off">No limits</span><?php endif; ?>
        <span class="small text-body-secondary"><?= (int) ($s['accounts'] ?? 0) ?> website account(s)</span>
    </div>
    <?php if (empty($s['available'])): ?>
        <div class="wcp-infobar mt-2"><i class="bi bi-exclamation-triangle-fill" aria-hidden="true"></i><span>nftables is not installed on this server; install the <code>nftables</code> package to limit outgoing connections.</span></div>
    <?php endif; ?>
</div>
<form class="wcp-panel" method="post" action="<?= e($this->url('security.egress.save')) ?>" data-wcp-busy>
    <?= $this->csrfField() ?>
    <fieldset<?= $canManage ? '' : ' disabled' ?>>
    <div class="form-check mb-2">
        <input class="form-check-input" type="checkbox" name="metadata_blocked" value="1" id="eg-meta"<?= $meta ? ' checked' : '' ?>>
        <label class="form-check-label" for="eg-meta"><strong>Block cloud metadata services</strong> (recommended)</label>
        <div class="form-text">The address <span class="font-monospace"><?= e(implode(', ', array_map('strval', (array) ($s['metadata'] ?? [])))) ?></span> hands out the server's cloud credentials and configuration on AWS, Google Cloud, Azure, DigitalOcean, Hetzner and others. Turn this off only if a website must use the server's cloud role.</div>
    </div>
    <div class="form-check mb-3">
        <input class="form-check-input" type="checkbox" name="block_private" value="1" id="eg-private"<?= $private ? ' checked' : '' ?>>
        <label class="form-check-label" for="eg-private"><strong>Block private networks</strong></label>
        <div class="form-text">10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10, link-local and IPv6 private ranges: the provider's internal network and other machines in it. Leave off if websites use a database or cache on another private server.</div>
    </div>
    <div class="row g-2 mb-3">
        <div class="col-md-6">
            <label class="form-label" for="eg-tcp">Allowed TCP ports</label>
            <input class="form-control form-control-sm font-monospace" id="eg-tcp" name="tcp_ports" value="<?= e($tcp) ?>" placeholder="any (e.g. 53, 80, 443)">
        </div>
        <div class="col-md-6">
            <label class="form-label" for="eg-udp">Allowed UDP ports</label>
            <input class="form-control form-control-sm font-monospace" id="eg-udp" name="udp_ports" value="<?= e($udp) ?>" placeholder="any (e.g. 53, 123)">
        </div>
        <div class="form-text">When set, websites may connect only to these remote ports (for example 53, 80, 443 for DNS and web APIs). Empty: any port.</div>
    </div>
    <?php if ($canManage): ?><button class="btn btn-sm btn-primary"<?= empty($s['available']) ? ' disabled' : '' ?>>Save</button><?php endif; ?>
    </fieldset>
</form>
