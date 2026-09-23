<?php
/** @var \WhaCP\View\View $this */
$canEdit = $this->can(\WhaCP\Security\Permissions::SETTINGS_MANAGE);
?>
<?php $this->start(); ?>Settings<?php $this->end('title'); ?>

<div class="row g-3">
    <div class="col-lg-7">
        <div class="card">
            <div class="card-header"><i class="bi bi-sliders me-1"></i>Panel settings</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('settings.update')) ?>" data-wcp-busy novalidate>
                    <?= $this->csrfField() ?>
                    <fieldset<?= $canEdit ? '' : ' disabled' ?>>
                        <?php foreach ($definitions as $key => $def): ?>
                            <?= $this->partial('partials/field', [
                                'name' => $key,
                                'label' => $def['label'],
                                'type' => $def['type'] === 'int' ? 'number' : 'text',
                                'value' => (string) ($old[$key] ?? $values[$key]),
                                'help' => $def['help'],
                                'attrs' => $def['type'] === 'int'
                                    ? ['min' => (string) $def['min'], 'max' => (string) $def['max'], 'required' => 'required']
                                    : ['maxlength' => (string) ($def['max_length'] ?? 255), 'required' => 'required'],
                            ]) ?>
                        <?php endforeach; ?>
                        <?php if ($canEdit): ?>
                            <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save settings</button>
                        <?php endif; ?>
                    </fieldset>
                </form>
            </div>
        </div>
    </div>
    <div class="col-lg-5">
        <div class="card">
            <div class="card-header"><i class="bi bi-info-circle me-1"></i>Installation</div>
            <div class="card-body">
                <dl class="wcp-dl">
                    <?php foreach ($system as $k => $v): ?>
                        <dt><?= e($k) ?></dt><dd><?= e($v) ?></dd>
                    <?php endforeach; ?>
                </dl>
                <p class="small text-body-secondary mb-0">Infrastructure settings (socket paths, database location, cookies) live in <code>/etc/whacp/panel.php</code> and are not editable from the browser.</p>
            </div>
        </div>
    </div>
</div>
