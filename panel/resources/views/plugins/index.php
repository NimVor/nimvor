<?php
/** @var \NimVor\View\View $this */
/** @var array<string, array<string, mixed>> $plugins */
?>
<?php $this->start(); ?>Plugins<?php $this->end('title'); ?>
<?php $this->start(); ?>Extend the panel<?php $this->end('subtitle'); ?>
<div class="alert alert-info small"><i class="bi bi-shield-exclamation me-1"></i>Plugins are PHP code that runs inside the panel with full access to it. Install only plugins you trust by copying them (as root) to <code><?= e($dir) ?>/&lt;name&gt;/</code>; they cannot be uploaded here. Files must not be writable by group or others.</div>
<div class="card">
    <?php if ($plugins === []): ?>
        <div class="card-body text-body-secondary">No plugins found in <code><?= e($dir) ?></code>.</div>
    <?php else: ?>
        <ul class="list-group list-group-flush">
            <?php foreach ($plugins as $p): $m = $p['manifest']; ?>
                <li class="list-group-item d-flex gap-3 align-items-start">
                    <div class="me-auto">
                        <div class="fw-semibold"><?= e($m['title'] ?? $p['name']) ?> <span class="text-body-secondary small"><?= e($m['version'] ?? '') ?></span>
                            <?= $p['enabled'] ? ($p['error'] === null ? $this->statusBadge('active', 'enabled') : $this->statusBadge('error', 'failed to load')) : $this->statusBadge('inactive', 'disabled') ?></div>
                        <div class="small"><?= e($m['description'] ?? '') ?></div>
                        <div class="small text-body-secondary"><code><?= e($p['name']) ?></code><?= isset($m['author']) ? ' · ' . e($m['author']) : '' ?><?= isset($m['license']) ? ' · ' . e($m['license']) : '' ?></div>
                        <?php if (!empty($m['permissions'])): ?><div class="small text-body-secondary">Permissions: <?= e(implode(', ', array_keys((array) $m['permissions']))) ?></div><?php endif; ?>
                        <?php foreach ($p['problems'] as $pr): ?><div class="small text-danger"><i class="bi bi-x-circle me-1"></i><?= e($pr) ?></div><?php endforeach; ?>
                        <?php if ($p['error'] !== null): ?><div class="small text-danger"><i class="bi bi-bug me-1"></i><?= e($p['error']) ?></div><?php endif; ?>
                    </div>
                    <form method="post" action="<?= e($this->url('plugins.toggle', ['name' => $p['name']])) ?>"<?= $p['enabled'] ? '' : ' data-nim-confirm="Enable ' . e($m['title'] ?? $p['name']) . '? It will run as part of the panel."' ?>>
                        <?= $this->csrfField() ?><input type="hidden" name="enabled" value="<?= $p['enabled'] ? '0' : '1' ?>">
                        <button class="btn btn-sm <?= $p['enabled'] ? 'btn-outline-secondary' : 'btn-primary' ?>"<?= !$p['enabled'] && $p['problems'] !== [] ? ' disabled' : '' ?>><?= $p['enabled'] ? 'Disable' : 'Enable' ?></button>
                    </form>
                </li>
            <?php endforeach; ?>
        </ul>
    <?php endif; ?>
</div>
<p class="small text-body-secondary mt-3">Writing a plugin: see <code>docs/plugins/README.md</code> and the example in <code>plugins/example-status</code>.</p>
