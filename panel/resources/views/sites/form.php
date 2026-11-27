<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed>|null $site */
$isNew = $site === null;
// In a window (Create website opened from the dashboard or the websites list): no page sections, a window header, and the form is sent in the background.
$modal = $isNew && !empty($modal);
$v = static function (string $k, mixed $default = '') use ($old, $site) {
    if (array_key_exists($k, $old)) {
        return $old[$k];
    }
    if ($site !== null) {
        if (array_key_exists($k, $site['php_settings'] ?? [])) {
            return $site['php_settings'][$k];
        }
        if (array_key_exists($k, $site)) {
            return $site[$k];
        }
    }
    return $default;
};
$kind = (string) $v('kind', 'php');
// Process limits, hardening and PHP-FPM tuning are the administrator's.
$serverPhp = $this->serverScope();
$aliases = $old['aliases'] ?? implode("\n", $site['aliases'] ?? []);
$docSub = $site === null ? '' : ltrim(substr((string) $site['document_root'], strlen((string) $site['root'])), '/');
$err = static fn (string $k): string => isset($errors[$k]) ? '<div class="invalid-feedback d-block">' . e($errors[$k]) . '</div>' : '';
$cls = static fn (string $k): string => isset($errors[$k]) ? ' is-invalid' : '';
// A new website is created step by step: 1 type, 2 domain and owner, 3 settings of that type, 4 review. The form and its field names are the same.
$st = static fn (int $n): string => $site === null ? ' data-nim-step="' . $n . '"' : '';
$steps = [1 => 'Type', 2 => 'Domain', 3 => 'Settings', 4 => 'Review'];
// The website types, with what each needs (cards whose feature is off for this account are not shown). Node.js and Python are hosted as a
// reverse proxy to the app's local port.
$allowedTypes ??= ['type_php' => true, 'type_static' => true, 'type_nodejs' => true, 'type_python' => true, 'type_proxy' => true];
$kindCards = array_values(array_filter([
    ['php', 'PHP application', 'filetype-php', 'type_php', 'WordPress, Laravel and other PHP apps, with PHP-FPM and a database.'],
    ['static', 'Static site', 'file-earmark-code', 'type_static', 'HTML, CSS and JavaScript files, served directly by Nginx.'],
    ['nodejs', 'Node.js', 'hexagon', 'type_nodejs', 'A Node.js app kept running as a service behind Nginx.'],
    ['python', 'Python', 'filetype-py', 'type_python', 'A Python app (Django, Flask, FastAPI) kept running as a service.'],
    ['proxy', 'Reverse proxy', 'hdd-network', 'type_proxy', 'Send a domain to an app already running on a local port or another server.'],
], static fn (array $c): bool => !empty($allowedTypes[$c[3]])));
?>
<?php if ($modal): ?>
    <div class="modal-header">
        <h2 class="modal-title h5"><i class="bi bi-plus-circle me-2" aria-hidden="true"></i>New website</h2>
        <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
    </div>
    <div class="modal-body">
<?php elseif ($isNew): ?>
    <?php $this->start(); ?>New website<?php $this->end('title'); ?>

<?php else: ?>
    <?= $this->partial('sites/header', ['site' => $site, 'tab' => 'settings']) ?>
<?php endif; ?>

<?php /* Errors on fields this page does not show (the account name is derived for customers) would otherwise leave "correct the highlighted fields" with nothing highlighted. */
$hidden = array_filter($errors, static fn (string $k): bool => in_array($k, $serverPhp ? ['kind', 'app_runtime'] : ['system_user', 'kind', 'app_runtime', 'owner_id'], true), ARRAY_FILTER_USE_KEY); ?>
<?php if ($hidden !== []): ?>
    <div class="alert alert-danger"><?php foreach ($hidden as $m): ?><div><?= e((string) $m) ?></div><?php endforeach; ?></div>
<?php endif; ?>
<?php if ($isNew): ?>
<ol class="nim-wizard-steps" aria-label="Steps">
    <?php foreach ($steps as $n => $label): ?><li class="nim-wizard-tab" data-nim-step-tab="<?= $n ?>"><span class="nim-wizard-num"><?= $n ?></span><span class="nim-wizard-label"><?= e($label) ?></span></li><?php endforeach; ?>
</ol>
<?php endif; ?>
<form method="post" action="<?= e($isNew ? $this->url('sites.store') : $this->url('sites.update', ['id' => (int) $site['id']])) ?>"<?= $modal ? ' data-nim-site-modal-form' : ' data-nim-busy' ?> novalidate<?= $isNew ? ' data-nim-wizard' : '' ?>>
    <?= $this->csrfField() ?>
    <?php if ($modal): ?><div class="alert alert-danger d-none" role="alert" data-nim-modal-error></div><?php endif; ?>
    <div class="row g-3">
        <div class="<?= $isNew ? ($modal ? 'col-12' : 'col-lg-8 mx-auto') : 'col-lg-6' ?>">
            <?php if ($isNew): ?>
            <?php /* The chosen type stays in view from step 2 on (hidden on step 1 by nimvor.css). */ ?>
            <?php foreach ($kindCards as [$k, $label, $icon, , $about]): ?>
                <div class="nim-type-chip" data-nim-kind="<?= e($k) ?>"><span class="nim-type-tile"><?= $this->logo($k === 'proxy' ? 'nginx' : $k, 24) ?: '<i class="bi bi-' . e($icon) . '" aria-hidden="true"></i>' ?></span><span><strong><?= e($label) ?></strong></span></div>
            <?php endforeach; ?>
            <div class="nim-step-head" data-nim-step="2"><h3>Domain and owner</h3><p>The address the website answers on, and who it belongs to.</p></div>
            <div class="nim-step-head" data-nim-step="3"><h3>Settings</h3><p>How the website runs. All of this can be changed later.</p></div>
            <div class="nim-step-head" data-nim-step="4"><h3>Review and create</h3><p>Check the details, then create the website.</p></div>
            <?php endif; ?>
            <div class="card mb-3" id="domains"<?= $st(2) ?>>
                <div class="card-header"><i class="bi bi-globe2 me-1"></i>Domains</div>
                <div class="card-body">
                    <div class="mb-3">
                        <label class="form-label" for="f-domain">Domain</label>
                        <input class="form-control<?= $cls('domain') ?>" id="f-domain" name="domain" value="<?= e($v('domain')) ?>" placeholder="example.com" <?= $isNew ? 'required' : 'disabled' ?> maxlength="253">
                        <?= $err('domain') ?>
                        <?php if (!$isNew): ?><div class="form-text">The primary domain cannot be changed; create a new website instead.</div><?php endif; ?>
                    </div>
                    <div class="mb-0">
                        <label class="form-label" for="f-aliases">Aliases</label><?= $this->hint('One per line. Point their DNS records at this server.', 'Aliases') ?>
                        <textarea class="form-control<?= $cls('aliases') ?>" id="f-aliases" name="aliases" rows="3" placeholder="www.example.com"><?= e($aliases) ?></textarea>
                        <?= $err('aliases') ?>
                    </div>
                </div>
            </div>
            <?php if ($isNew): $ownerMode = (string) ($old['owner_mode'] ?? ''); $customers = array_values(array_filter((array) ($owners ?? []), static fn (array $o): bool => ($o['role_scope'] ?? 'all') === 'own')); ?>
            <div class="mb-3" data-nim-step="2" data-nim-owner>
                <?php if ($serverPhp): ?>
                    <input type="hidden" name="wizard" value="1">
                    <span class="form-label d-block">Website owner<?= $this->hint('Who the website belongs to. An existing customer, or a new customer account that is created together with the website (its login is named after the domain unless you give one, and its password is generated and shown once), or nobody: administrators only.', 'Website owner') ?></span>
                    <div class="nim-choice-row" role="radiogroup" aria-label="Website owner">
                        <label class="nim-choice"><input type="radio" name="owner_mode" value="existing" required<?= $ownerMode === 'existing' ? ' checked' : '' ?>><span>Existing customer</span></label>
                        <label class="nim-choice"><input type="radio" name="owner_mode" value="new" required<?= $ownerMode === 'new' ? ' checked' : '' ?>><span>New customer account</span></label>
                        <label class="nim-choice"><input type="radio" name="owner_mode" value="none" required<?= $ownerMode === 'none' ? ' checked' : '' ?>><span>No customer (administrators only)</span></label>
                    </div>
                    <?= $err('owner_mode') ?>
                    <div class="invalid-feedback" data-nim-owner-error></div>
                    <div class="mt-2<?= $ownerMode === 'existing' ? '' : ' d-none' ?>" data-nim-owner-pane="existing">
                        <?php if ($customers === []): ?>
                            <div class="small text-body-secondary">There are no customers yet. Choose "New customer account" to create one with this website.</div>
                        <?php else: ?>
                            <label class="visually-hidden" for="f-owner">Customer</label>
                            <select class="form-select<?= $cls('owner_id') ?>" id="f-owner" name="owner_id"<?= $ownerMode === 'existing' ? '' : ' disabled' ?>>
                                <option value="">Choose a customer…</option>
                                <?php foreach ($customers as $o): ?>
                                    <option value="<?= (int) $o['id'] ?>"<?= (string) ($old['owner_id'] ?? '') === (string) $o['id'] ? ' selected' : '' ?>><?= e($o['username']) ?> (<?= e($o['role_label'] ?? 'customer') ?>)</option>
                                <?php endforeach; ?>
                            </select>
                            <?= $err('owner_id') ?>
                        <?php endif; ?>
                    </div>
                    <div class="mt-2<?= $ownerMode === 'new' ? '' : ' d-none' ?>" data-nim-owner-pane="new">
                        <div class="row g-2">
                            <div class="col-sm-6">
                                <label class="form-label small" for="f-new-email">Customer email</label>
                                <input class="form-control<?= $cls('new_email') ?>" type="email" id="f-new-email" name="new_email" value="<?= e((string) ($old['new_email'] ?? '')) ?>" placeholder="customer@example.com" autocomplete="off"<?= $ownerMode === 'new' ? '' : ' disabled' ?>>
                                <?= $err('new_email') ?>
                            </div>
                            <div class="col-sm-6">
                                <label class="form-label small" for="f-new-user">Login name</label>
                                <input class="form-control<?= $cls('new_username') ?>" id="f-new-user" name="new_username" value="<?= e((string) ($old['new_username'] ?? '')) ?>" placeholder="derived from the domain" maxlength="31" autocomplete="off"<?= $ownerMode === 'new' ? '' : ' disabled' ?>>
                                <?= $err('new_username') ?>
                            </div>
                        </div>
                        <div class="small text-body-secondary mt-1">A customer account is created with the website, on the default plan. Its password is generated and shown once afterwards.</div>
                    </div>
                <?php else: $me = (array) ($this->shared('user') ?? []); ?>
                    <span class="form-label d-block">Website owner</span>
                    <div class="small"><i class="bi bi-person-check me-1" aria-hidden="true"></i><strong><?= e((string) (($me['display_name'] ?? '') !== '' ? $me['display_name'] : ($me['username'] ?? 'You'))) ?></strong> <span class="text-body-secondary">(you): your websites belong to your account.</span></div>
                <?php endif; ?>
            </div>
            <?php endif; ?>
            <div class="card mb-3"<?= $st(1) ?>>
                <div class="card-header"><i class="bi bi-diagram-3 me-1"></i><?= $isNew ? 'What are you hosting?' : 'Application' ?></div>
                <div class="card-body">
                    <div class="mb-3">
                        <span class="form-label d-block<?= $isNew ? ' visually-hidden' : '' ?>">Type</span>
                        <?php
                        // Image cards instead of a dropdown. Node.js and Python
                        // are hosted as a reverse proxy to the app's local port,
                        // so they select the "proxy" type.
                        // Each card names the feature it needs; cards whose
                        // feature is off for this account are not shown.
                        $kindChosen = false;
                        ?>
                        <div class="row row-cols-1 row-cols-md-2 g-2" role="radiogroup" aria-label="Application type">
                            <?php foreach ($kindCards as $i => [$k, $label, $icon, , $about]): $checked = !$kindChosen && $kind === $k; if ($checked) { $kindChosen = true; } ?>
                                <div class="col">
                                    <input type="radio" class="btn-check" name="kind" id="f-kind-<?= $i ?>" value="<?= e($k) ?>" data-nim-toggle-kind autocomplete="off"<?= $checked ? ' checked' : '' ?>>
                                    <label class="nim-type-card" for="f-kind-<?= $i ?>">
                                        <span class="nim-type-tile"><?= $this->logo($k === 'proxy' ? 'nginx' : $k, 36) ?: '<i class="bi bi-' . e($icon) . '" aria-hidden="true"></i>' ?></span>
                                        <span class="nim-type-text">
                                            <span class="nim-type-title" data-nim-label><?= e($label) ?></span>
                                            <span class="nim-type-desc"><?= e($about) ?></span>
                                        </span>
                                    </label>
                                </div>
                            <?php endforeach; ?>
                        </div>
                        <?= $err('kind') ?>
                    </div>
                    <?php if ($isNew): /* the type is step 1; what it needs is step 3 */ ?>
                </div>
            </div>
            <div class="card mb-3"<?= $st(3) ?>>
                <div class="card-header"><i class="bi bi-sliders me-1"></i>Application settings</div>
                <div class="card-body">
                    <?php endif; ?>
                    <?php if ($isNew): ?>
                    <div class="mb-3" data-nim-kind="php">
                        <span class="form-label d-block">PHP version</span>
                        <?php if ($phpVersions === []): ?>
                            <select class="form-select<?= $cls('php_version') ?>" id="f-php" name="php_version" aria-label="PHP version"><option value="">No PHP-FPM installed</option></select>
                        <?php else: ?>
                            <?php $phpPick = (string) $v('php_version', $defaultPhp ?? ''); if (!in_array($phpPick, $phpVersions, true)) { $phpPick = (string) end($phpVersions); } ?>
                            <div class="nim-choice-row" role="radiogroup" aria-label="PHP version">
                                <?php foreach (array_reverse($phpVersions) as $pv): ?>
                                    <label class="nim-choice"><input type="radio" name="php_version" value="<?= e($pv) ?>"<?= $phpPick === $pv ? ' checked' : '' ?>><span>PHP <?= e($pv) ?></span></label>
                                <?php endforeach; ?>
                            </div>
                        <?php endif; ?>
                        <?= $err('php_version') ?>
                    </div>
                    <?php endif; ?>
                    <div class="mb-3" data-nim-kind="proxy">
                        <label class="form-label" for="f-proxy">Upstream address</label><?= $this->hint('Your app must listen on this local port (run it as a Worker).', 'Upstream address') ?>
                        <input class="form-control<?= $cls('proxy_pass') ?>" id="f-proxy" name="proxy_pass" value="<?= e($v('proxy_pass')) ?>" placeholder="http://127.0.0.1:3000">
                        <?= $err('proxy_pass') ?>
                    </div>
                    <?php
                    $runtimes ??= [];
                    foreach (['nodejs' => 'node', 'python' => 'python'] as $ak => $rp):
                        if (empty($allowedTypes['type_' . $ak])) {
                            continue;
                        }
                        $opts = array_values(array_filter($runtimes, static fn (string $r): bool => str_starts_with($r, $rp . ':')));
                    ?>
                    <div data-nim-kind="<?= e($ak) ?>">
                        <div class="row g-2">
                            <div class="col-sm-7">
                                <label class="form-label" for="f-rt-<?= e($ak) ?>"><?= $ak === 'python' ? 'Python' : 'Node.js' ?> version</label>
                                <select class="form-select<?= $cls('app_runtime') ?>" id="f-rt-<?= e($ak) ?>" name="app_runtime">
                                    <?php if ($opts === []): ?><option value="">None installed — ask your provider</option><?php endif; ?>
                                    <?php foreach ($opts as $r): ?><option value="<?= e($r) ?>"<?= (string) $v('app_runtime') === $r ? ' selected' : '' ?>><?= e(ucfirst($rp)) ?> <?= e(substr($r, strlen($rp) + 1)) ?></option><?php endforeach; ?>
                                </select>
                                <?= $err('app_runtime') ?>
                            </div>
                            <div class="col-sm-5">
                                <label class="form-label" for="f-port-<?= e($ak) ?>">App port</label>
                                <input class="form-control<?= $cls('app_port') ?>" id="f-port-<?= e($ak) ?>" name="app_port" type="number" min="1024" max="65535" value="<?= e($v('app_port', $ak === 'python' ? '8000' : '3000')) ?>">
                                <?= $err('app_port') ?>
                            </div>
                        </div>
                        <label class="form-label mt-2" for="f-cmd-<?= e($ak) ?>">Start command <span class="text-body-secondary small">(optional)</span></label><?= $this->hint('We run your app as a background service on the chosen runtime and serve it over your domain. PORT is set for you. You can edit it later under Runtime.') ?>
                        <input class="form-control font-monospace" id="f-cmd-<?= e($ak) ?>" name="app_command" value="<?= e($v('app_command')) ?>" placeholder="<?= $ak === 'python' ? 'gunicorn app:app --bind 127.0.0.1:$PORT' : 'node server.js' ?>">
                    </div>
                    <?php endforeach; ?>
                    <div class="mb-0">
                        <label class="form-label" for="f-docsub">Document root</label><?= $this->hint('For Laravel use public_html/public.', 'Document root') ?>
                        <div class="input-group">
                            <span class="input-group-text small"><?= e($site['root'] ?? '<sites root>/<domain>') ?>/</span>
                            <input class="form-control<?= $cls('document_root_sub') ?>" id="f-docsub" name="document_root_sub" value="<?= e($old['document_root_sub'] ?? ($docSub !== '' ? $docSub : 'public_html')) ?>">
                        </div>
                        <?= $err('document_root_sub') ?>
                    </div>
                </div>
            </div>
        <?php if (!$isNew): ?></div>
        <div class="col-lg-6"><?php endif; ?>
            <?php if ($isNew): /* New sites get the defaults (account from the domain, recommended PHP profile, 256/64/60/5, 64 MB body); they are changed on the website's own pages. */ else: ?>
            <?php if ($serverPhp): /* Admins/operators set the account and owner; a hosting customer owns what they create and the account is derived from the domain. */ ?>
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-person me-1"></i>System user</div>
                <div class="card-body">
                    <input class="form-control<?= $cls('system_user') ?>" name="system_user" value="<?= e($v('system_user')) ?>" placeholder="derived from the domain" <?= $isNew ? '' : 'disabled' ?> maxlength="31" aria-label="System user">
                    <?= $err('system_user') ?>
                    <div class="form-text">PHP, cron jobs, workers and deployments run as this Linux account. It is created if it does not exist.</div>
                </div>
            </div>
            <?php endif; ?>
            <div class="card mb-3" id="requests">
                <div class="card-header"><i class="bi bi-signpost me-1"></i>Requests</div>
                <div class="card-body">
                    <label class="form-label small" for="f-body">Max request body (MB)</label>
                    <input class="form-control form-control-sm mb-3<?= $cls('client_max_body_mb') ?>" type="number" id="f-body" name="client_max_body_mb" value="<?= e($v('client_max_body_mb', 64)) ?>">
                    <?= $err('client_max_body_mb') ?>
                    <?php if (!$isNew): ?><div class="small text-body-secondary">Redirects are managed one by one on the <a href="<?= e($this->url('sites.redirects', ['id' => (int) $site['id']])) ?>">Redirects</a> page, and PHP version and php.ini settings on the <a href="<?= e($this->url('sites.php', ['id' => (int) $site['id']])) ?>">PHP settings</a> page.</div><?php endif; ?>
                </div>
            </div>
            <?php endif; ?>
            <?php if ($isNew): ?>
            <div class="card mb-3"<?= $st(4) ?>>
                <div class="card-header"><i class="bi bi-check2-circle me-1"></i>Review</div>
                <div class="card-body">
                    <dl class="row mb-0 small nim-review" data-nim-review>
                        <dt class="col-sm-4">Domain</dt><dd class="col-sm-8" data-nim-review-field="domain"></dd>
                        <dt class="col-sm-4">Type</dt><dd class="col-sm-8" data-nim-review-field="kind"></dd>
                        <dt class="col-sm-4">Owner</dt><?php if ($serverPhp): ?><dd class="col-sm-8" data-nim-review-field="owner"></dd><?php else: $meR = (array) ($this->shared('user') ?? []); ?><dd class="col-sm-8"><?= e((string) (($meR['display_name'] ?? '') !== '' ? $meR['display_name'] : ($meR['username'] ?? 'You'))) ?> (you)</dd><?php endif; ?>
                        <dt class="col-sm-4" data-nim-kind="php">PHP version</dt><dd class="col-sm-8" data-nim-kind="php" data-nim-review-field="php_version"></dd>
                        <dt class="col-sm-4" data-nim-kind="nodejs python">Runtime and port</dt><dd class="col-sm-8" data-nim-kind="nodejs python" data-nim-review-field="app_runtime"></dd>
                        <dt class="col-sm-4" data-nim-kind="proxy">Upstream</dt><dd class="col-sm-8" data-nim-kind="proxy" data-nim-review-field="proxy_pass"></dd>
                        <dt class="col-sm-4">Document root</dt><dd class="col-sm-8" data-nim-review-field="document_root_sub"></dd>
                    </dl>
                    <div class="form-check mt-3" data-nim-kind="php static">
                        <input class="form-check-input" type="checkbox" id="f-after-apps" name="after_apps" value="1"<?= !empty($old['after_apps']) ? ' checked' : '' ?>>
                        <label class="form-check-label" for="f-after-apps">Open the app installer afterwards (WordPress, Joomla, Nextcloud …)</label>
                    </div>
                    <p class="text-body-secondary small mt-3 mb-0">Nginx is validated before it is reloaded, and the website is removed again if anything fails. DNS and email follow your account's defaults.</p>
                </div>
            </div>
            <?php endif; ?>
        </div>
    </div>
    <?php if ($isNew): ?>
    <?php /* One footer: Cancel on the left; Back, Next and (on the last step) Create website on the right. nimvor.js shows what the step needs. */ ?>
    <div class="nim-wizard-foot">
        <?php if ($modal): ?><button class="btn btn-outline-secondary me-auto" type="button" data-bs-dismiss="modal">Cancel</button>
        <?php else: ?><a class="btn btn-outline-secondary me-auto" href="<?= e($this->url('websites')) ?>">Cancel</a><?php endif; ?>
        <button class="btn btn-outline-secondary" type="button" data-nim-wizard-back hidden>Back</button>
        <button class="btn btn-primary" type="button" data-nim-wizard-next>Next</button>
        <button class="btn btn-primary" type="submit" data-nim-wizard-submit hidden><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Create website</button>
    </div>
    <?php else: ?>
    <div class="d-flex gap-2 align-items-center">
        <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save and apply</button>
    </div>
    <?php endif; ?>
</form>
<?php if ($modal): ?></div><?php endif; ?>
