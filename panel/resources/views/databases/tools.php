<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed>|null $status
 * @var bool $isAdmin
 */
?>
<?php $this->start(); ?>Database tools<?php $this->end('title'); ?>
<?php $this->start(); ?>phpMyAdmin and Adminer next to NimVor's own database browser<?php $this->end('subtitle'); ?>

<p class="text-body-secondary">NimVor's own database browser needs nothing installed and works with the database you open it from. If you or your customers prefer phpMyAdmin or Adminer, an administrator can install them here. They open on the panel's own address and you sign in with a database user's name and password.</p>

<?php if ($status === null): ?>
    <div class="alert alert-warning"><i class="bi bi-exclamation-triangle me-1"></i>The agent did not answer, so what is installed is not known. Try again in a moment.</div>
<?php else: ?>
    <?php if (empty($status['panel_https']) && $isAdmin): ?>
        <div class="alert alert-info small"><i class="bi bi-info-circle me-1"></i>The panel is not served over HTTPS by NimVor's own Nginx site, so the tools have no address to be reached on. Run the installer again with <code>--public-https</code>, then come back.</div>
    <?php endif; ?>
    <div class="row g-3">
        <div class="col-md-6">
            <div class="card h-100">
                <div class="card-header"><i class="bi bi-table me-1"></i>Built-in database browser</div>
                <div class="card-body small">
                    <p class="mb-2">Tables, rows, structure and a SQL console, with an account that can reach that one database only. Always available: open <strong>Browse</strong> next to a database.</p>
                    <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('databases')) ?>">Databases</a>
                </div>
            </div>
        </div>
        <?php foreach ((array) ($status['tools'] ?? []) as $t): $t = (array) $t; $id = (string) $t['id']; ?>
        <div class="col-md-6">
            <div class="card h-100">
                <div class="card-header d-flex justify-content-between"><span><i class="bi bi-database-gear me-1"></i><?= e((string) $t['name']) ?></span><?php if (!empty($t['installed'])): ?><span class="badge text-bg-success">Installed</span><?php else: ?><span class="badge text-bg-secondary">Not installed</span><?php endif; ?></div>
                <div class="card-body small">
                    <p class="mb-2"><?= e((string) $t['description']) ?></p>
                    <?php if (!empty($t['installed'])): ?>
                        <p class="mb-2">Opens at <a href="<?= e((string) $t['path']) ?>" target="_blank" rel="noopener"><code><?= e((string) $t['path']) ?></code></a>. Sign in with a database user's name and password (shown when the user was created).</p>
                        <a class="btn btn-sm btn-primary" href="<?= e((string) $t['path']) ?>" target="_blank" rel="noopener">Open</a>
                        <?php if ($isAdmin): ?>
                        <form method="post" action="<?= e($this->url('databases.tools.remove', ['tool' => $id])) ?>" class="d-inline" data-nim-confirm="Remove <?= e((string) $t['name']) ?>? Its files are deleted; your databases are not touched."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger">Remove</button></form>
                        <?php endif; ?>
                    <?php elseif ($isAdmin): ?>
                        <p class="text-body-secondary mb-2">Downloaded from the vendor and checked against its published checksum, kept in a read-only folder and run in a PHP-FPM pool and account of their own.</p>
                        <form method="post" action="<?= e($this->url('databases.tools.install', ['tool' => $id])) ?>" data-nim-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-primary"<?= empty($status['panel_https']) ? ' disabled' : '' ?>><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Install</button></form>
                    <?php else: ?>
                        <p class="text-body-secondary mb-0">An administrator can install this.</p>
                    <?php endif; ?>
                </div>
            </div>
        </div>
        <?php endforeach; ?>
    </div>
    <p class="small text-body-secondary mt-3 mb-0">phpMyAdmin and Adminer are third-party programs and run next to the panel on the same address. Keep them updated by installing them again, and remove them when nobody uses them.</p>
<?php endif; ?>
