<?php
/** @var \NimVor\View\View $this */
?>
<?php $this->start(); ?>Status note<?php $this->end('title'); ?>
<?php $this->start(); ?>Example plugin<?php $this->end('subtitle'); ?>
<div class="row g-3">
    <div class="col-lg-6">
        <div class="card"><div class="card-body">
            <dl class="nim-dl mb-0">
                <dt>Host</dt><dd><?= e($info['system']['hostname'] ?? 'agent unavailable') ?></dd>
                <dt>Uptime</dt><dd><?= isset($info['system']['uptime_seconds']) ? e(format_duration((float) $info['system']['uptime_seconds'])) : '—' ?></dd>
                <dt>Websites in NimVor</dt><dd><?= (int) $websites ?></dd>
            </dl>
        </div></div>
    </div>
    <div class="col-lg-6">
        <div class="card"><div class="card-body">
            <?php if ($this->can('plugin.example-status.edit')): ?>
                <form method="post" action="/plugins/example-status/note">
                    <?= $this->csrfField() ?>
                    <label class="form-label" for="note">Team note</label>
                    <textarea class="form-control mb-2" id="note" name="note" rows="4" maxlength="500"><?= e($note) ?></textarea>
                    <button class="btn btn-sm btn-primary">Save</button>
                </form>
            <?php else: ?>
                <p class="mb-0"><?= $note === '' ? '<span class="text-body-secondary">No note.</span>' : nl2br(e($note)) ?></p>
            <?php endif; ?>
        </div></div>
    </div>
</div>
