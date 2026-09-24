<?php /** @var \WhaCP\View\View $this */ ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>
<div class="card" id="wcp-logs" data-tail="<?= e($this->url('logs.tail')) ?>" data-download="<?= e($this->url('logs.download')) ?>">
    <div class="card-header d-flex flex-wrap gap-2 align-items-center">
        <select class="form-select form-select-sm w-auto" data-log-source aria-label="Log">
            <?php if ($sources === []): ?><option value="">No logs yet</option><?php endif; ?>
            <?php $group = null; foreach ($sources as $s): ?>
                <?php if (($s['group'] ?? '') !== $group): ?><?php if ($group !== null): ?></optgroup><?php endif; $group = $s['group'] ?? ''; ?><optgroup label="<?= e($group) ?>"><?php endif; ?>
                <option value="<?= e($s['id']) ?>"<?= ($initialSource ?? '') === $s['id'] ? ' selected' : '' ?>><?= e($s['label']) ?><?= isset($s['size']) && $s['kind'] === 'file' && isset($s['exists']) ? ' (' . e(format_bytes((int) $s['size'])) . ')' : '' ?></option>
            <?php endforeach; ?>
            <?php if ($group !== null): ?></optgroup><?php endif; ?>
        </select>
        <select class="form-select form-select-sm w-auto" data-log-lines aria-label="Lines">
            <?php foreach ([100, 200, 500, 1000, 5000] as $n): ?><option value="<?= $n ?>"<?= $n === 200 ? ' selected' : '' ?>>last <?= $n ?> lines</option><?php endforeach; ?>
        </select>
        <input class="form-control form-control-sm w-auto" type="search" placeholder="Filter" data-log-filter aria-label="Filter">
        <div class="form-check form-switch mb-0 ms-2"><input class="form-check-input" type="checkbox" role="switch" id="log-live" data-log-live><label class="form-check-label small" for="log-live">Live</label></div>
        <div class="ms-auto d-flex gap-2">
            <button class="btn btn-sm btn-outline-secondary" type="button" data-log-refresh><i class="bi bi-arrow-clockwise"></i> Refresh</button>
            <a class="btn btn-sm btn-outline-secondary" href="#" data-log-download><i class="bi bi-download"></i> Download</a>
        </div>
    </div>
    <pre class="wcp-pre wcp-logview mb-0 rounded-0" data-log-output aria-live="polite"></pre>
    <div class="card-footer small text-body-secondary" data-log-status></div>
</div>
<script src="<?= e($this->asset('assets/js/logs.js')) ?>" defer></script>
