<?php
/**
 * Server → Components: each part of the server with the steps it needs and
 * a Fix per step.
 *
 * @var \WhaCP\View\View $this
 * @var array<int, array<string, mixed>>|null $components
 * @var list<string> $expected
 */
use WhaCP\Http\Controllers\ComponentsController;
use WhaCP\Security\Permissions as P;

$labels = ['installed' => 'Packages', 'setup' => 'Set up', 'running' => 'Running', 'boot' => 'Starts at boot', 'ports' => 'Ports open'];
$fixFor = ['installed' => ['install', P::PACKAGES_MANAGE, 'Install'], 'running' => ['start', P::SERVICES_MANAGE, 'Start'], 'boot' => ['boot', P::SERVICES_MANAGE, 'Start at boot'], 'ports' => ['ports', P::SECURITY_MANAGE, 'Open ports']];
$components ??= null;
$expected ??= [];
?>
<?php $this->start(); ?>Components<?php $this->end('title'); ?>
<?php $this->start(); ?>What each part of the server needs, and what is missing<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
    <?php if ($this->can(P::SERVER_MANAGE) && $components !== null): ?>
        <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#components-profile"><i class="bi bi-ui-checks me-1" aria-hidden="true"></i>What this server should have</button>
    <?php endif; ?>
<?php $this->end('actions'); ?>

<?php if ($components === null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError ?? null]) ?>
<?php else:
    $used = array_values(array_filter($components, static fn (array $c): bool => in_array($c['id'], $expected, true)));
    $unused = array_values(array_filter($components, static fn (array $c): bool => !in_array($c['id'], $expected, true)));
    $ready = count(array_filter($used, static fn (array $c): bool => !empty($c['ready'])));
    $card = function (array $c, bool $isUsed) use ($labels, $fixFor): string {
        ob_start();
        $id = (string) $c['id'];
        $page = ComponentsController::PAGES[$id] ?? null;
        $state = !$c['installed'] ? ['Not installed', ''] : (!empty($c['ready']) ? ['Ready', 'is-ok'] : ['Needs attention', 'is-warn']);
        ?>
        <div class="col-md-6 col-xxl-4">
            <div class="wcp-panel h-100 d-flex flex-column" id="c-<?= e($id) ?>" data-component="<?= e($id) ?>">
                <div class="d-flex align-items-start gap-2 p-3 pb-2">
                    <div class="flex-grow-1 min-w-0">
                        <div class="fw-semibold"><?= e((string) $c['name']) ?></div>
                        <div class="small wcp-muted"><?= e((string) $c['description']) ?></div>
                    </div>
                    <span class="wcp-state <?= e($state[1]) ?> text-nowrap small"><?= e($state[0]) ?></span>
                </div>
                <ul class="list-unstyled small px-3 mb-2 flex-grow-1">
                    <?php foreach ((array) $c['steps'] as $s): $st = (string) $s['status']; if ($st === 'na' && empty($s['detail'])) { continue; } ?>
                        <li class="d-flex align-items-start gap-2 py-1 border-top" data-step="<?= e((string) $s['id']) ?>" data-status="<?= e($st) ?>">
                            <i class="bi <?= ['ok' => 'bi-check-circle-fill text-success', 'todo' => 'bi-exclamation-circle-fill text-warning', 'na' => 'bi-dash-circle text-body-secondary'][$st] ?? 'bi-dash-circle' ?> mt-1" aria-hidden="true"></i>
                            <div class="flex-grow-1 min-w-0">
                                <span class="fw-medium"><?= e($labels[$s['id']] ?? (string) $s['id']) ?></span>
                                <?php if (!empty($s['detail'])): ?><div class="wcp-muted text-break"><?= e((string) $s['detail']) ?></div><?php endif; ?>
                            </div>
                            <?php if ($st === 'todo'):
                                $fix = $fixFor[$s['id']] ?? null;
                                $viaPage = $s['id'] === 'setup' || ($s['id'] === 'installed' && ($id === 'php' || $id === 'mongodb')) || ($s['id'] === 'running' && $id === 'firewall');
                                if ($viaPage && $page !== null && $this->hasRoute($page) && $this->routeAllowed($page)): ?>
                                    <a class="btn btn-sm btn-outline-primary text-nowrap" href="<?= e($this->url($page)) ?>"><?= $s['id'] === 'setup' ? 'Set up' : 'Open' ?></a>
                                <?php elseif (!$viaPage && $fix !== null && $this->can($fix[1])): ?>
                                    <form method="post" action="<?= e($this->url('components.fix', ['id' => $id, 'step' => $fix[0]])) ?>" data-wcp-busy<?= $fix[0] === 'ports' ? ' data-wcp-confirm="Open ' . e(implode(', ', (array) ($s['items'] ?? []))) . ' in the firewall for ' . e((string) $c['name']) . '? You then have 2 minutes to keep the change."' : '' ?>>
                                        <?= $this->csrfField() ?>
                                        <button class="btn btn-sm btn-outline-primary text-nowrap"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= e($fix[2]) ?></button>
                                    </form>
                                <?php endif; ?>
                            <?php endif; ?>
                        </li>
                    <?php endforeach; ?>
                </ul>
                <div class="d-flex flex-wrap gap-2 px-3 pb-3 small">
                    <?php if ($page !== null && $c['installed'] && $this->hasRoute($page) && $this->routeAllowed($page)): ?><a href="<?= e($this->url($page)) ?>"><i class="bi bi-gear me-1" aria-hidden="true"></i>Settings</a><?php endif; ?>
                    <?php if ((array) $c['units'] !== []): ?><span class="wcp-muted font-monospace text-truncate"><?= e(implode(' ', (array) $c['units'])) ?></span><?php endif; ?>
                    <?php if (!$isUsed && $c['installed']): ?><span class="wcp-muted">Installed, though not marked as needed.</span><?php endif; ?>
                </div>
            </div>
        </div>
        <?php
        return (string) ob_get_clean();
    };
    ?>
    <div class="wcp-panel mb-3">
        <div class="wcp-statusbar">
            <span class="fw-medium">This server</span>
            <span><?= $ready === count($used) ? '<span class="wcp-state is-ok">All ' . count($used) . ' components ready</span>' : '<span class="wcp-state is-warn">' . $ready . ' of ' . count($used) . ' components ready</span>' ?></span>
            <span class="small wcp-muted">Each step has a Fix. Opening ports is undone after 2 minutes unless you keep the change; installing runs in the background (see the job log).</span>
        </div>
    </div>
    <div class="row g-3 mb-4"><?php foreach ($used as $c) { echo $card($c, true); } ?></div>
    <?php if ($unused !== []): ?>
        <details class="mb-3" id="components-unused">
            <summary class="fw-medium mb-2">Not used on this server (<?= count($unused) ?>)</summary>
            <div class="row g-3 mt-1"><?php foreach ($unused as $c) { echo $card($c, false); } ?></div>
        </details>
    <?php endif; ?>

    <?php if ($this->can(P::SERVER_MANAGE)): ?>
        <?= $this->modalStart('components-profile', 'What this server should have', false, 'modal-md') ?>
        <form method="post" action="<?= e($this->url('components.profile')) ?>" data-wcp-busy>
            <?= $this->csrfField() ?>
            <p class="small wcp-muted">Components you tick are checked and shown first; the others are listed under “Not used on this server”. Nothing is installed or removed here.</p>
            <?php foreach ($components as $c): ?>
                <div class="form-check">
                    <input class="form-check-input" type="checkbox" name="expected[]" value="<?= e((string) $c['id']) ?>" id="cp-<?= e((string) $c['id']) ?>"<?= in_array($c['id'], $expected, true) ? ' checked' : '' ?>>
                    <label class="form-check-label" for="cp-<?= e((string) $c['id']) ?>"><?= e((string) $c['name']) ?><?= $c['installed'] ? '' : ' <span class="small wcp-muted">(not installed)</span>' ?></label>
                </div>
            <?php endforeach; ?>
            <div class="d-flex justify-content-end gap-2 mt-3">
                <button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
                <button class="btn btn-sm btn-primary">Save</button>
            </div>
        </form>
        <?= $this->modalEnd() ?>
    <?php endif; ?>
<?php endif; ?>
