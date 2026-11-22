<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $status */
/** @var array<string, mixed> $preview */
$canEdit = $this->can(\NimVor\Security\Permissions::SETTINGS_MANAGE);
$on = (bool) $status['enabled'];
$when = static fn (?int $t): string => $t === null ? 'never' : gmdate('Y-m-d H:i', $t) . ' UTC';
$names = ['heartbeat' => 'Heartbeat (every day)', 'health' => 'Health (every day)', 'inventory' => 'Inventory (every week)', 'features' => 'Feature counts (every week)'];
?>
<?php $this->start(); ?>Telemetry<?php $this->end('title'); ?>
<?php $this->start(); ?>Help the project see which systems and features matter. Off unless you turn it on<?php $this->end('subtitle'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('settings')) ?>"><i class="bi bi-arrow-left me-1" aria-hidden="true"></i>Settings</a><?php $this->end('actions'); ?>

<div class="row g-3">
    <div class="col-lg-7">
        <section class="card mb-3">
            <div class="card-header"><i class="bi bi-activity me-1" aria-hidden="true"></i>Status</div>
            <div class="card-body">
                <?php if ($on): ?>
                    <p class="mb-2"><span class="badge text-bg-success">On</span> This installation reports to the NimVor project. You can turn it off at any time.</p>
                    <dl class="row small mb-0">
                        <dt class="col-sm-4">Installation id</dt><dd class="col-sm-8"><code><?= e((string) ($status['uuid'] ?? '')) ?></code> <span class="text-body-secondary">(random; not made from anything on this server)</span></dd>
                        <dt class="col-sm-4">Registered</dt><dd class="col-sm-8"><?= $status['registered'] ? 'yes' : 'not yet (the next scheduled pass registers it)' ?></dd>
                        <dt class="col-sm-4">Last answer from the service</dt><dd class="col-sm-8"><?= e($when($status['last_success'])) ?></dd>
                        <?php foreach ($names as $k => $label): ?><dt class="col-sm-4"><?= e($label) ?></dt><dd class="col-sm-8">last sent <?= e($when($status['last'][$k])) ?></dd><?php endforeach; ?>
                        <dt class="col-sm-4">Waiting to be sent</dt><dd class="col-sm-8"><?= (int) $status['pending'] ?></dd>
                        <?php if ($status['last_error'] !== ''): ?><dt class="col-sm-4">Last problem</dt><dd class="col-sm-8 text-warning-emphasis"><?= e($status['last_error']) ?></dd><?php endif; ?>
                    </dl>
                <?php elseif ($status['pending_deletion']): ?>
                    <p class="mb-0"><span class="badge text-bg-secondary">Off</span> A request to unregister or delete is waiting to reach the central service; it is retried until it does.</p>
                <?php else: ?>
                    <p class="mb-0"><span class="badge text-bg-secondary">Off</span> Nothing is sent, and no installation id exists, until you turn it on.<?= $status['decided'] ? '' : ' You have not been asked before; the answer is yours.' ?></p>
                <?php endif; ?>
            </div>
        </section>
        <section class="card mb-3">
            <div class="card-header"><i class="bi bi-shield-check me-1" aria-hidden="true"></i>Your choices</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('settings.telemetry.save')) ?>" data-nim-noajax>
                    <?= $this->csrfField() ?>
                    <fieldset<?= $canEdit ? '' : ' disabled' ?> class="d-flex flex-wrap gap-2">
                        <?php if (!$on): ?>
                            <button class="btn btn-primary" name="action" value="enable" type="submit">Turn telemetry on</button>
                        <?php else: ?>
                            <button class="btn btn-outline-secondary" name="action" value="disable" type="submit">Turn off</button>
                            <button class="btn btn-outline-secondary" name="action" value="unregister" type="submit">Turn off and unregister</button>
                        <?php endif; ?>
                    </fieldset>
                </form>
                <?php if ($on || $status['uuid'] !== null): ?>
                    <form class="mt-2" method="post" action="<?= e($this->url('settings.telemetry.save')) ?>" data-nim-noajax data-nim-confirm="Turn telemetry off and delete everything the NimVor project stored about this installation?">
                        <?= $this->csrfField() ?><input type="hidden" name="action" value="delete">
                        <button class="btn btn-outline-danger"<?= $canEdit ? '' : ' disabled' ?> type="submit">Turn off and delete my data</button>
                    </form>
                <?php endif; ?>
                <p class="small text-body-secondary mt-3 mb-0"><strong>Off</strong> stops the reports. <strong>Unregister</strong> also tells the service this installation is gone (what was sent stays as anonymous totals). <strong>Delete</strong> erases everything stored for this installation; only a one-way hash of its id is kept so a late retry cannot bring it back. It is never used for licensing and no feature depends on it.</p>
            </div>
        </section>
        <section class="card">
            <div class="card-header"><i class="bi bi-lock me-1" aria-hidden="true"></i>What is never sent</div>
            <div class="card-body small">
                IP address, host name, domain and website names, user names, e-mail addresses, file paths or contents, logs, passwords, keys, the machine id or MAC address, anything a customer typed. Only counts, versions and yes/no answers from the fixed list on the right. Full description: <a href="https://github.com/NimVor/nimvor/blob/main/docs/telemetry/README.md" rel="noopener">docs/telemetry</a>.
            </div>
        </section>
    </div>
    <div class="col-lg-5">
        <section class="card">
            <div class="card-header"><i class="bi bi-eye me-1" aria-hidden="true"></i>What the next reports would contain</div>
            <div class="card-body">
                <p class="small text-body-secondary">Read from this server right now. This is exactly the data; the installation id and a report id are added when it is sent.</p>
                <?php foreach ($names as $k => $label): ?>
                    <h3 class="h6 mt-3"><?= e($label) ?></h3>
                    <?php if ($preview[$k] === null): ?>
                        <p class="small text-body-secondary mb-0">Not available right now (the agent could not be reached).</p>
                    <?php else: ?>
                        <pre class="small border rounded p-2 mb-0 bg-body-tertiary"><?= e((string) json_encode($preview[$k], JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES)) ?></pre>
                    <?php endif; ?>
                <?php endforeach; ?>
            </div>
        </section>
    </div>
</div>
