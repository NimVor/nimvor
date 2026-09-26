<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $storages */
/** @var string $prefix */
/** @var bool $alwaysEncrypted server backups: private keys inside */
$prefix ??= 'b';
$alwaysEncrypted ??= false;
?>
<?php if ($alwaysEncrypted): ?>
<div class="small text-body-secondary"><i class="bi bi-lock me-1"></i>Always encrypted (AES-256): it contains private keys.</div>
<?php else: ?>
<div class="form-check form-switch"><input type="hidden" name="encrypt" value="0"><input class="form-check-input" type="checkbox" role="switch" id="<?= e($prefix) ?>-enc" name="encrypt" value="1" checked><label class="form-check-label" for="<?= e($prefix) ?>-enc">Encrypt (AES-256)</label></div>
<?php endif; ?>
<?php if ($storages !== []): ?>
    <label class="form-label small mt-2" for="<?= e($prefix) ?>-st">Copy to</label>
    <select class="form-select form-select-sm" id="<?= e($prefix) ?>-st" name="storage_id">
        <option value="">This server only</option>
        <?php foreach ($storages as $s): ?><option value="<?= (int) $s['id'] ?>"><?= e($s['name']) ?> (<?= e(\WhaCP\Services\BackupService::STORAGE_TYPES[$s['kind']] ?? $s['kind']) ?>)</option><?php endforeach; ?>
    </select>
    <div class="form-check small mt-1"><input type="hidden" name="keep_local" value="0"><input class="form-check-input" type="checkbox" id="<?= e($prefix) ?>-keep" name="keep_local" value="1" checked><label class="form-check-label" for="<?= e($prefix) ?>-keep">Also keep a copy on this server</label></div>
<?php endif; ?>
