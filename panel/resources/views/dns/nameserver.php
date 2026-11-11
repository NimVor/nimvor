<?php
/**
 * Nameserver selection (like cPanel's): PowerDNS, NSD, Knot DNS or BIND serve the same zones.
 *
 * @var \NimVor\View\View $this
 * @var array{backends: list<array<string, mixed>>, active: string, zones: int, signed: int}|null $info
 * @var \NimVor\Agent\AgentException|null $agentError
 */
$errors ??= [];
$old ??= [];
?>
<?php $this->start(); ?>Nameserver selection<?php $this->end('title'); ?>
<?php $this->start(); ?>The DNS server that answers for the zones hosted here<?php $this->end('subtitle'); ?>

<nav class="small mb-3" aria-label="breadcrumb"><a href="<?= e($this->url('dns')) ?>">DNS</a> <span class="text-body-secondary">/</span> Nameserver selection</nav>

<?php if ($agentError !== null || $info === null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError]) ?>
<?php else: ?>
<?php
$active = $info['active'];
$labels = [];
foreach ($info['backends'] as $b) {
    $labels[(string) $b['id']] = (string) $b['label'];
}
$pick = (string) ($old['backend'] ?? $active);
?>
<p class="text-body-secondary">A nameserver is a program that keeps the list of your domain names and their records, so visitors can find the websites hosted on this server. It is a vital part of most hosting setups; servers that use a remote nameserver do not need their own.</p>
<p class="text-body-secondary">Select the nameserver to use. All of them answer for the same zones: <strong><?= (int) $info['zones'] ?></strong> zone<?= (int) $info['zones'] === 1 ? '' : 's' ?> now<?= (int) $info['signed'] > 0 ? ', ' . (int) $info['signed'] . ' signed with DNSSEC' : '' ?>. Switching keeps every zone; only the program that serves them changes.</p>

<?php if (isset($errors['backend'])): ?><div class="alert alert-danger small"><?= e((string) $errors['backend']) ?></div><?php endif; ?>

<form method="post" action="<?= e($this->url('dns.nameserver.select')) ?>" data-nim-busy>
    <?= $this->csrfField() ?>
    <div class="card mb-3">
        <div class="table-responsive">
            <table class="table align-middle mb-0 nim-nameservers">
                <thead>
                    <tr><th style="width:2.5rem"><span class="visually-hidden">Choose</span></th><th>Nameserver</th><th>Advantages</th><th>Disadvantages</th><th>Notes</th></tr>
                </thead>
                <tbody>
                <?php foreach ($info['backends'] as $b): ?>
                    <?php
                    $id = (string) $b['id'];
                    $dnssec = (array) ($b['dnssec'] ?? []);
                    ?>
                    <tr<?= $id === $active ? ' class="table-active"' : '' ?>>
                        <td><input class="form-check-input" type="radio" name="backend" value="<?= e($id) ?>" id="ns-<?= e($id) ?>"<?= $pick === $id ? ' checked' : '' ?> aria-label="Use <?= e((string) $b['label']) ?>"></td>
                        <td>
                            <label class="fw-semibold" for="ns-<?= e($id) ?>"><?= e((string) $b['label']) ?></label>
                            <div class="mt-1 d-flex flex-wrap gap-1">
                                <?php if ($id === $active): ?><span class="badge text-bg-primary">In use</span><?php endif; ?>
                                <?php if (!empty($b['installed'])): ?><span class="badge text-bg-light border">Installed</span><?php else: ?><span class="badge text-bg-light border text-body-secondary">Not installed</span><?php endif; ?>
                                <?php if (!empty($b['running'])): ?><span class="badge text-bg-success">Running</span><?php elseif (!empty($b['installed'])): ?><span class="badge text-bg-secondary">Stopped</span><?php endif; ?>
                            </div>
                        </td>
                        <td class="small"><ul class="mb-0 ps-3"><?php foreach ((array) $b['advantages'] as $line): ?><li><?= e((string) $line) ?></li><?php endforeach; ?></ul></td>
                        <td class="small"><ul class="mb-0 ps-3"><?php foreach ((array) $b['disadvantages'] as $line): ?><li><?= e((string) $line) ?></li><?php endforeach; ?></ul></td>
                        <td class="small">
                            <ul class="mb-0 ps-3">
                                <?php foreach ((array) $b['notes'] as $line): ?><li><?= e((string) $line) ?></li><?php endforeach; ?>
                                <?php if (!empty($b['replaces_config'])): ?><li>NimVor takes over <code><?= e((string) $b['config_file']) ?></code>; the original is kept.</li><?php endif; ?>
                                <?php if (empty($dnssec['supported']) && !empty($dnssec['reason'])): ?><li class="text-body-secondary"><?= e((string) $dnssec['reason']) ?></li><?php endif; ?>
                            </ul>
                        </td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    </div>

    <div class="card mb-3">
        <div class="card-body">
            <h2 class="h6">What happens</h2>
            <ol class="small mb-3">
                <li>The chosen nameserver is installed if it is not yet.</li>
                <li>NimVor writes its configuration for the existing zone files and checks it with the nameserver's own checker; nothing has been stopped so far.</li>
                <li>The current nameserver stops, the new one starts and is asked for every zone.</li>
                <li>If anything fails, the current nameserver is started again with the same zones and the new configuration is removed.</li>
            </ol>
            <div class="form-check mb-2">
                <input class="form-check-input" type="checkbox" name="confirm" value="1" id="ns-confirm" required<?= ($old['confirm'] ?? '') === '1' ? ' checked' : '' ?>>
                <label class="form-check-label" for="ns-confirm">I understand that NimVor changes the chosen nameserver's configuration (shown above, with a backup of the original) and that DNS answers pause for a moment while the nameserver changes.</label>
            </div>
            <?php if ((int) $info['signed'] > 0): ?>
                <div class="form-check mb-2">
                    <input class="form-check-input" type="checkbox" name="confirm_dnssec" value="1" id="ns-confirm-dnssec" required>
                    <label class="form-check-label" for="ns-confirm-dnssec"><strong><?= (int) $info['signed'] ?> zone<?= (int) $info['signed'] === 1 ? ' is' : 's are' ?> signed (DNSSEC).</strong> Keys cannot move between nameservers: the zones are signed again with new keys (the DS record at each registrar must then be replaced), or served unsigned by NSD (remove the DS record at the registrar first). I understand.</label>
                </div>
            <?php endif; ?>
            <button class="btn btn-primary mt-2" type="submit"><i class="bi bi-arrow-left-right me-1"></i>Select nameserver</button>
            <span class="small text-body-secondary ms-2">Choosing the one in use changes nothing.</span>
        </div>
    </div>
</form>
<?php endif; ?>
