<?php
/**
 * @var \NimVor\View\View $this
 * @var array{available: list<array<string, mixed>>, planned: list<string>} $sources
 */
$icon = ['cpanel' => 'hdd-network', 'linux' => 'terminal'];
$level = ['experimental' => ['Experimental', 'text-bg-warning'], 'partial' => ['Partial support', 'text-bg-info'], 'full' => ['Fully supported', 'text-bg-success']];
?>
<?php $this->start(); ?>New migration<?php $this->end('title'); ?>
<?php $this->start(); ?>Step 1: choose where the website comes from<?php $this->end('subtitle'); ?>

<div class="alert alert-info small">
    <strong>What “experimental” means here.</strong> A source is marked <em>Fully supported</em> only after it has been proven against real servers. Today every source is <em>Experimental</em>: it was written from the panel's documented layout and tested on synthetic data and a test server, not on a production cPanel. Test with a copy of a small site first. Nothing is ever changed or deleted on the source.
</div>
<div class="row g-3 mb-4">
    <?php foreach ($sources['available'] as $s): [$text, $cls] = $level[$s['support']] ?? [(string) $s['support'], 'text-bg-secondary']; ?>
    <div class="col-md-6 col-xl-4">
        <form class="card h-100" method="post" action="<?= e($this->url('migrations.store')) ?>">
            <?= $this->csrfField() ?><input type="hidden" name="panel" value="<?= e((string) $s['panel']) ?>">
            <div class="card-body">
                <div class="d-flex align-items-center mb-2"><i class="bi bi-<?= e($icon[$s['panel']] ?? 'hdd') ?> fs-3 me-2"></i><div class="fw-semibold"><?= e((string) $s['name']) ?></div></div>
                <span class="badge <?= e($cls) ?>"><?= e($text) ?></span>
                <p class="small text-body-secondary mt-2 mb-0"><?= match ($s['panel']) { 'cpanel' => 'Reads accounts, domains, document roots, databases, mailboxes and cron jobs from cPanel\'s files over SSH (log in as root).', 'plesk' => 'Reads subscriptions, domains, databases and mailboxes from Plesk\'s own database with read-only queries (log in as root; Plesk for Linux).', default => 'A server without a control panel: sites are read from the Nginx or Apache configuration, databases from MySQL/MariaDB.' }?></p>
            </div>
            <div class="card-footer bg-transparent"><button class="btn btn-sm btn-primary">Use this source</button></div>
        </form>
    </div>
    <?php endforeach; ?>
</div>
<div class="nim-panel"><div class="p-3">
    <div class="fw-semibold mb-2">Not available yet</div>
    <p class="small text-body-secondary mb-2">These panels are planned. They are listed so nobody assumes they work; they cannot be chosen until their migration has been written and tested.</p>
    <?php foreach ($sources['planned'] as $p): ?><span class="badge text-bg-light border me-1 mb-1"><?= e($p) ?></span><?php endforeach; ?>
</div></div>
