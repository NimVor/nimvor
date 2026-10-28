<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var array<string, mixed>|null $status
 * @var array{mode: string, exclude: list<string>, disabled_rules: list<int>}|null $setting
 * @var list<array<string, mixed>> $events
 * @var bool $isAdmin
 */
use NimVor\Security\Permissions as P;
use NimVor\Services\ModSecurityService;

$active = (bool) ($status['active'] ?? false);
$canChange = $this->can(P::WEBSITES_MANAGE);
$byDomain = [strtolower((string) $site['domain']) => $site];
$help = [
    'on' => 'Requests that look like attacks (SQL injection, cross-site scripting, file inclusion, remote code execution) are refused with 403.',
    'detect' => 'Nothing is refused; matches are only listed below. Use it to check a website before turning blocking on.',
    'off' => 'Requests are not inspected.',
];
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'firewall']) ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php return; endif; ?>

<?php if (!$active): ?>
    <div class="card"><div class="nim-empty-state">
        <i class="bi bi-shield-slash" aria-hidden="true"></i>
        <div class="fw-medium">The web application firewall is not active on this server</div>
        <?php if ($this->can(P::SECURITY_MANAGE) && $this->hasRoute('security.modsec') && $this->serverScope()): ?>
            <div class="small mb-3">Set up ModSecurity under Security → ModSecurity; each website can then use it.</div>
            <a class="btn btn-primary" href="<?= e($this->url('security.modsec')) ?>">Open ModSecurity</a>
        <?php else: ?>
            <div class="small">Please ask your hosting provider to enable it.</div>
        <?php endif; ?>
    </div></div>
    <?php return; ?>
<?php endif; ?>

<div class="row g-3 mb-3">
    <div class="col-lg-6">
        <div class="nim-panel h-100">
            <div class="fw-semibold mb-2">Web firewall (ModSecurity)</div>
            <form method="post" action="<?= e($this->url('sites.firewall.update', ['id' => (int) $site['id']])) ?>" data-nim-busy>
                <?= $this->csrfField() ?>
                <fieldset<?= $canChange ? '' : ' disabled' ?>>
                <?php foreach (ModSecurityService::MODES as $k => $label): ?>
                    <div class="form-check mb-1">
                        <input class="form-check-input" type="radio" name="mode" id="fw-<?= e($k) ?>" value="<?= e($k) ?>"<?= ($setting['mode'] ?? 'off') === $k ? ' checked' : '' ?>>
                        <label class="form-check-label" for="fw-<?= e($k) ?>"><?= e($label) ?></label>
                        <div class="form-text mt-0"><?= e($help[$k]) ?></div>
                    </div>
                <?php endforeach; ?>
                <?php if ($canChange): ?><button class="btn btn-sm btn-primary mt-2">Save</button><?php endif; ?>
                </fieldset>
            </form>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="nim-panel h-100 small">
            <div class="fw-semibold mb-2">Rules for this website</div>
            <p class="mb-1"><span class="nim-muted">Rule vendors:</span> <?= e(implode(', ', array_map(static fn (string $v): string => $v === 'crs' ? 'OWASP Core Rule Set' : 'Custom rules', array_values(array_diff(['crs', 'custom'], $setting['exclude'] ?? []))))) ?: 'none' ?></p>
            <p class="mb-2"><span class="nim-muted">Rules turned off:</span> <span class="font-monospace"><?= ($setting['disabled_rules'] ?? []) === [] ? 'none' : e(implode(' ', $setting['disabled_rules'])) ?></span></p>
            <?php if ($isAdmin): ?>
                <p class="mb-0">Change them under <a href="<?= e($this->url('security.modsec')) ?>#sites">Security → ModSecurity</a>, or turn a rule off from an event below.</p>
            <?php else: ?>
                <p class="mb-0 nim-muted">If a legitimate request is blocked, ask your hosting provider to turn the rule shown below off for this website.</p>
            <?php endif; ?>
        </div>
    </div>
</div>

<div class="nim-panel" data-nim-dt>
    <div class="nim-actionbar"><div class="fw-semibold">Recent events <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($events) ?></span></div>
        <div class="nim-actionbar-end"><div class="nim-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search events" data-nim-dt-search><i class="bi bi-search" aria-hidden="true"></i></div></div></div>
    <?= $this->partial('partials/modsec_events', ['events' => $events, 'byDomain' => $byDomain, 'showHost' => false, 'disableFor' => $isAdmin ? $site : null]) ?>
</div>
