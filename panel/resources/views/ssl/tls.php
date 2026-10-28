<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed>|null $tls
 */
use NimVor\Security\Permissions as P;

$canManage = $this->can(P::SERVER_MANAGE);
$p = (array) ($tls['profile'] ?? []);
?>
<?= $this->partial('ssl/_tabs', get_defined_vars()) ?>
<?php if ($tls === null) { return; } ?>

<div class="row g-3">
    <div class="col-xl-6">
        <form class="card h-100" method="post" action="<?= e($this->url('ssl.tls.save')) ?>" data-nim-busy data-nim-confirm="Apply this TLS configuration to every NimVor website and webmail? Nginx is checked and reloaded; if the check fails nothing changes.">
            <?= $this->csrfField() ?>
            <div class="card-header"><i class="bi bi-shield-lock me-1"></i>TLS configuration of NimVor websites</div>
            <div class="card-body">
                <fieldset class="mb-3"><legend class="form-label small fw-semibold">Protocol versions</legend>
                    <?php foreach (['tls12' => ['TLS 1.2 and 1.3', 'Works with every browser and client still in use.'], 'tls13' => ['TLS 1.3 only', 'Strongest; older clients (e.g. some old Android versions, older mail and monitoring tools) can no longer connect.']] as $v => [$l, $h]): ?>
                        <div class="form-check"><input class="form-check-input" type="radio" name="protocols" value="<?= $v ?>" id="tls-p-<?= $v ?>"<?= ($p['protocols'] ?? 'tls12') === $v ? ' checked' : '' ?><?= $canManage ? '' : ' disabled' ?>><label class="form-check-label" for="tls-p-<?= $v ?>"><?= e($l) ?><div class="form-text mt-0"><?= e($h) ?></div></label></div>
                    <?php endforeach; ?>
                </fieldset>
                <fieldset class="mb-3"><legend class="form-label small fw-semibold">TLS 1.2 ciphers</legend>
                    <?php foreach (['default' => ['Nginx default', 'Nginx\'s built-in list.'], 'intermediate' => ['Modern ciphers only (Mozilla "intermediate")', 'Forward secrecy and authenticated encryption only; recommended.']] as $v => [$l, $h]): ?>
                        <div class="form-check"><input class="form-check-input" type="radio" name="ciphers" value="<?= $v ?>" id="tls-c-<?= $v ?>"<?= ($p['ciphers'] ?? 'default') === $v ? ' checked' : '' ?><?= $canManage ? '' : ' disabled' ?>><label class="form-check-label" for="tls-c-<?= $v ?>"><?= e($l) ?><div class="form-text mt-0"><?= e($h) ?></div></label></div>
                    <?php endforeach; ?>
                </fieldset>
                <div class="form-check"><input class="form-check-input" type="checkbox" name="session_tickets" value="1" id="tls-t"<?= !empty($p['session_tickets']) ? ' checked' : '' ?><?= $canManage ? '' : ' disabled' ?>><label class="form-check-label" for="tls-t">Session tickets<div class="form-text mt-0">Faster reconnects; off keeps forward secrecy for resumed sessions (recommended off).</div></label></div>
            </div>
            <?php if ($canManage): ?><div class="card-footer text-end"><button class="btn btn-primary btn-sm"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Apply</button></div><?php endif; ?>
        </form>
    </div>
    <div class="col-xl-6">
        <div class="card h-100">
            <div class="card-header d-flex align-items-center gap-2"><span>Current snippet</span><span class="small nim-muted font-monospace ms-auto text-break"><?= e((string) $tls['path']) ?></span></div>
            <div class="card-body">
                <pre class="nim-db-create small mb-2"><?= e((string) $tls['snippet']) ?></pre>
                <p class="small nim-muted mb-1">Included by every NimVor website and webmail (<?= (int) $tls['vhosts'] ?> Nginx files). Certificates and HSTS stay per website (each website's SSL page).</p>
                <?php if ((int) $tls['legacy_vhosts'] > 0): ?><p class="small mb-1"><span class="nim-state is-warn"><?= (int) $tls['legacy_vhosts'] ?> file(s)</span> still carry their own TLS lines from an earlier version; applying moves them to this snippet.</p><?php endif; ?>
                <p class="small nim-muted mb-0">OCSP stapling is not offered: Let's Encrypt no longer runs OCSP. The panel's own HTTPS (set up by the installer) is not changed here.</p>
            </div>
        </div>
    </div>
</div>
