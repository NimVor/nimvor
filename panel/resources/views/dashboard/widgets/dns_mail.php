<?php
/**
 * DNS and mail.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
?>
<div class="mb-3">
    <div class="fw-semibold mb-1">Nameserver</div>
    <?php if ($d['dns'] === null): ?>
        <div class="text-body-secondary">N/A: the nameserver status could not be read.</div>
    <?php elseif (!$d['dns']['installed']): ?>
        <div class="text-body-secondary">No nameserver is installed. <?= (int) $d['zones_in_panel'] > 0 ? (int) $d['zones_in_panel'] . ' zone(s) are recorded in the panel.' : '' ?></div>
    <?php else: ?>
        <div><?= e($d['dns']['label']) ?> <span class="<?= $d['dns']['running'] ? 'text-success' : 'text-danger' ?>"><?= $d['dns']['running'] ? 'running' : 'not running' ?></span>
            · <?= (int) $d['dns']['zones'] ?> zone<?= (int) $d['dns']['zones'] === 1 ? '' : 's' ?><?= (int) $d['dns']['signed'] > 0 ? ', ' . (int) $d['dns']['signed'] . ' signed (DNSSEC)' : '' ?></div>
    <?php endif; ?>
</div>
<div>
    <div class="fw-semibold mb-1">Mail</div>
    <?php if ($d['mail'] === null): ?>
        <div class="text-body-secondary">N/A: the mail services could not be read.</div>
    <?php elseif ($d['mail']['services'] === []): ?>
        <div class="text-body-secondary">The mail server is not installed.</div>
    <?php else: ?>
        <div>
            <?php foreach ((array) $d['mail']['services'] as $s): ?>
                <span class="me-2"><?= e((string) $s['name']) ?> <span class="<?= $s['active'] ? 'text-success' : 'text-danger' ?>"><?= $s['active'] ? 'running' : 'stopped' ?></span></span>
            <?php endforeach; ?>
        </div>
        <div class="text-body-secondary"><?= (int) $d['mail_domains'] ?> mail domain<?= (int) $d['mail_domains'] === 1 ? '' : 's' ?>.</div>
        <?php if ($d['queue'] === null): ?>
            <div class="text-body-secondary">Queue: N/A.</div>
        <?php else: ?>
            <div class="<?= $d['queue']['deferred'] > 0 ? 'text-warning' : '' ?>">Queue: <?= (int) $d['queue']['total'] ?> message<?= (int) $d['queue']['total'] === 1 ? '' : 's' ?><?= $d['queue']['deferred'] > 0 ? ', ' . (int) $d['queue']['deferred'] . ' deferred (delivery is failing)' : '' ?><?= $d['queue']['hold'] > 0 ? ', ' . (int) $d['queue']['hold'] . ' on hold' : '' ?>.</div>
        <?php endif; ?>
    <?php endif; ?>
</div>
