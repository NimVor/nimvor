<?php
/**
 * Setup wizard: what is done and what is left, and the server profile with
 * its steps run from the browser.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed>|null $last
 * @var string $profile
 * @var string $recommended
 * @var int $memoryMb
 * @var string $hostname
 * @var list<string> $php
 * @var bool $thirdParty
 * @var array<string, list<string>> $steps
 * @var list<array{id: string, label: string, detail: string, done: bool, route: string}> $checklist
 * @var bool $mayRun
 * @var bool $done
 */
use NimVor\Http\Controllers\SetupController;
use NimVor\Services\ServerSetupService;

$descriptions = [
    'full' => ['Web server, PHP, MariaDB, email (Postfix, Dovecot, OpenDKIM, Rspamd), DNS (authoritative), FTP, Redis, Memcached, ClamAV, firewall with protections, Fail2ban, hardening, SMTP restrictions, automatic security updates.', '4 GB of memory or more'],
    'web' => ['Web server, PHP, MariaDB, FTP, Redis, Memcached, firewall with protections, Fail2ban, hardening, automatic security updates. No email, DNS or malware scanner.', '2 GB or more'],
    'minimal' => ['Web server, PHP, MariaDB, Let\'s Encrypt, firewall, Fail2ban, hardening, automatic security updates.', 'less than 2 GB'],
];
$lastSteps = (array) ($last['steps'] ?? []);
$icons = ['ok' => 'bi-check-circle-fill text-success', 'skip' => 'bi-check-circle text-success', 'fail' => 'bi-x-circle-fill text-danger', 'running' => 'bi-hourglass-split text-primary', '' => 'bi-circle text-body-secondary'];
$labels = ['ok' => 'Done', 'skip' => 'Already in place', 'fail' => 'Failed', '' => 'Not run'];
$left = count(array_filter($checklist, static fn (array $i): bool => !$i['done']));
?>
<?php $this->start(); ?>Setup wizard<?php $this->end('title'); ?>
<?php $this->start(); ?>Server profile, what is done and what is left<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
    <?php if (!$done): ?>
        <form method="post" action="<?= e($this->url('setup.dismiss')) ?>" data-nim-noajax><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-check2 me-1" aria-hidden="true"></i>Close the wizard</button></form>
    <?php endif; ?>
<?php $this->end('actions'); ?>

<div class="nim-panel mb-4" id="setup-checklist">
    <div class="nim-statusbar">
        <span class="fw-medium">This server</span>
        <span><?= $left === 0 ? '<span class="nim-state is-ok">Everything recommended is done</span>' : '<span class="nim-state is-warn">' . $left . ' of ' . count($checklist) . ' left</span>' ?></span>
        <?php if ($last !== null): ?><span class="small nim-muted">Last setup: <?= e(ServerSetupService::PROFILE_NAMES[$last['profile']] ?? (string) $last['profile']) ?>, <?= e(str_replace('T', ' ', substr((string) $last['started_at'], 0, 16))) ?> UTC<?= $last['finished_at'] === null ? ' (not finished)' : '' ?></span><?php endif; ?>
    </div>
    <ul class="list-unstyled mb-0">
        <?php foreach ($checklist as $i): ?>
            <li class="d-flex align-items-start gap-2 px-3 py-2 border-top" data-check="<?= e($i['id']) ?>" data-done="<?= $i['done'] ? '1' : '0' ?>">
                <i class="bi <?= $i['done'] ? 'bi-check-circle-fill text-success' : 'bi-circle text-body-secondary' ?> mt-1" aria-hidden="true"></i>
                <div class="flex-grow-1 min-w-0">
                    <div class="fw-medium"><?= e($i['label']) ?><span class="visually-hidden"><?= $i['done'] ? ' (done)' : ' (to do)' ?></span></div>
                    <div class="small nim-muted"><?= e($i['detail']) ?></div>
                </div>
                <?php if (!$i['done'] && $i['id'] !== 'setup' && $this->hasRoute($i['route']) && $this->routeAllowed($i['route'])): ?>
                    <a class="btn btn-sm btn-outline-primary text-nowrap" href="<?= e($this->url($i['route'])) ?>">Open</a>
                <?php endif; ?>
            </li>
        <?php endforeach; ?>
    </ul>
</div>

<?php if (!$mayRun): ?>
    <div class="nim-infobar mb-3"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>Running the setup needs an administrator: it installs packages and changes the firewall, email, DNS, plans and backups.</span></div>
<?php else: ?>
<form class="nim-panel p-3" data-nim-setup data-step-url="<?= e($this->url('setup.step')) ?>" data-nim-noajax>
    <h2 class="h6 mb-3">1. Server profile</h2>
    <div class="row g-3 mb-3">
        <?php foreach (ServerSetupService::PROFILE_NAMES as $p => $name): ?>
            <div class="col-md-4">
                <label class="nim-panel d-block p-3 h-100" for="sp-<?= e($p) ?>">
                    <div class="d-flex align-items-center gap-2 mb-1">
                        <input class="form-check-input mt-0" type="radio" name="profile" value="<?= e($p) ?>" id="sp-<?= e($p) ?>"<?= $p === $profile ? ' checked' : '' ?>>
                        <span class="fw-semibold"><?= e($name) ?></span>
                        <?php if ($p === $recommended): ?><span class="badge text-bg-primary ms-auto">Recommended</span><?php endif; ?>
                    </div>
                    <div class="small nim-muted"><?= e($descriptions[$p][0]) ?></div>
                    <div class="small mt-1">For servers with <?= e($descriptions[$p][1]) ?>.</div>
                </label>
            </div>
        <?php endforeach; ?>
    </div>
    <?php if ($memoryMb > 0): ?><p class="small nim-muted">This server has <?= e(number_format($memoryMb / 1024, 1)) ?> GB of memory. Every profile also creates the plans Starter, Business, Pro and Email only with their feature lists, and weekly backups of every website.</p><?php endif; ?>

    <h2 class="h6 mb-3">2. Options</h2>
    <div class="row g-3 mb-3">
        <div class="col-md-6">
            <label class="form-label small" for="sp-hostname">Server host name (for email)</label>
            <input class="form-control form-control-sm" id="sp-hostname" name="hostname" value="<?= e($hostname) ?>" placeholder="server.example.com" maxlength="253" pattern="[a-z0-9.\-]+\.[a-z]{2,63}">
            <div class="form-text">Its address record should point to this server and the server's reverse DNS (PTR) name should match it. Empty: the system's host name.</div>
        </div>
        <div class="col-md-6">
            <span class="form-label small d-block">PHP versions</span>
            <?php foreach (SetupController::PHP_CHOICES as $v): ?>
                <div class="form-check form-check-inline"><input class="form-check-input" type="checkbox" name="php" value="<?= e($v) ?>" id="sp-php-<?= e(str_replace('.', '', $v)) ?>"<?= in_array($v, $php, true) ? ' checked' : '' ?>><label class="form-check-label small" for="sp-php-<?= e(str_replace('.', '', $v)) ?>">PHP <?= e($v) ?></label></div>
            <?php endforeach; ?>
            <div class="form-text">Installed versions are always kept. More later in the PHP Manager.</div>
        </div>
        <div class="col-12">
            <div class="form-check">
                <input class="form-check-input" type="checkbox" name="third_party_repos" value="1" id="sp-repos"<?= $thirdParty ? ' checked' : '' ?>>
                <label class="form-check-label small" for="sp-repos">Add the third-party PHP repository when the system lacks a chosen version (Ondřej Surý's PPA on Ubuntu, with its signing key pinned; Remi's repository on AlmaLinux, Rocky Linux and RHEL). Its packages are then trusted like the system's.</label>
            </div>
        </div>
    </div>

    <h2 class="h6 mb-3">3. Steps</h2>
    <?php foreach ($steps as $p => $ids): ?>
        <ol class="list-unstyled nim-panel mb-3" data-setup-steps="<?= e($p) ?>"<?= $p === $profile ? '' : ' hidden' ?>>
            <?php foreach ($ids as $id):
                $st = ($last['profile'] ?? '') === $p ? (string) ($lastSteps[$id]['status'] ?? '') : '';
                $detail = ($last['profile'] ?? '') === $p ? (string) ($lastSteps[$id]['detail'] ?? '') : ''; ?>
                <li class="d-flex align-items-start gap-2 px-3 py-2 border-top" data-step="<?= e($id) ?>" data-status="<?= e($st) ?>">
                    <i class="bi <?= e($icons[$st] ?? $icons['']) ?> mt-1" data-step-icon aria-hidden="true"></i>
                    <div class="flex-grow-1 min-w-0">
                        <div><span class="fw-medium"><?= e(ServerSetupService::STEPS[$id]) ?></span> <span class="small nim-muted" data-step-state><?= e($labels[$st] ?? $st) ?></span></div>
                        <div class="small nim-muted text-break" data-step-detail><?= e($detail) ?></div>
                        <pre class="small bg-body-tertiary p-2 mt-1 mb-0 nim-setup-log" data-step-log hidden></pre>
                    </div>
                    <button type="button" class="btn btn-link btn-sm p-0 text-nowrap" data-step-job hidden>Job log</button>
                </li>
            <?php endforeach; ?>
        </ol>
    <?php endforeach; ?>

    <div class="form-check mb-3">
        <input class="form-check-input" type="checkbox" id="sp-confirm" required>
        <label class="form-check-label small" for="sp-confirm">Set this server up with the chosen profile: packages are installed; the firewall is turned on (SSH, the panel and the agent stay reachable: a firewall change is kept only after checking that); with email and DNS, NimVor edits Postfix's, Dovecot's and BIND's configuration (backups are kept). Settings already in place are left alone and nothing is removed.</label>
    </div>
    <div class="d-flex flex-wrap align-items-center gap-2">
        <button class="btn btn-primary" data-setup-run><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= $last === null ? 'Run the setup' : 'Run the setup again' ?></button>
        <span class="small nim-muted" data-setup-summary role="status" aria-live="polite">Steps already in place are skipped. Keep this page open while it runs; installing packages takes a few minutes.</span>
        <a class="btn btn-outline-secondary ms-auto" href="<?= e($this->url('setup')) ?>" data-setup-reload hidden>Show what is left</a>
    </div>
</form>
<?php endif; ?>
