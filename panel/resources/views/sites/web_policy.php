<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var array{headers: array<string, mixed>, cache: array<string, mixed>, headers_custom: bool, cache_custom: bool, hsts_available: bool} $state
 */

use NimVor\Services\WebPolicyService as W;

$id = (int) $site['id'];
$old ??= [];
$errors ??= [];
$h = $state['headers'];
$c = $state['cache'];
// After a rejected save the form shows what was typed.
$redo = $errors !== [] && $old !== [];
$hv = static function (string $k, mixed $default) use ($redo, $old): mixed {
    return $redo && array_key_exists($k, $old) ? $old[$k] : $default;
};
$err = static fn (string $k): ?string => isset($errors[$k]) ? (string) $errors[$k] : null;
$fb = static fn (?string $m): string => $m === null ? '' : '<div class="invalid-feedback d-block">' . e($m) . '</div>';
$flag = static fn (string $k, bool $default): bool => $redo ? (string) ($old[$k] ?? '0') === '1' : $default;
$hstsOn = $redo ? ($old['hsts_enabled'] ?? '0') === '1' : !empty($h['hsts']['enabled']);
$hstsAge = (int) $hv('hsts_max_age', (int) $h['hsts']['max_age']);
$staticTtl = (int) $hv('static_ttl', (int) $c['static_ttl']);
$presetTtl = isset(W::STATIC_TTLS[$staticTtl]);
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'headers']) ?>

<p class="small text-body-secondary">Choose the security headers <strong><?= e($site['domain']) ?></strong> sends to browsers and how browsers may cache it. NimVor's own protections (blocked sensitive files, PHP restrictions, request limits, the web firewall and site passwords) stay on and are not affected by anything here.</p>

<div class="nim-subtabs" role="tablist">
    <button class="active" type="button" role="tab" data-bs-toggle="tab" data-bs-target="#headers" aria-controls="headers" aria-selected="true"><i class="bi bi-shield-check me-1"></i>Security headers<?php if ($state['headers_custom']): ?> <span class="badge rounded-pill text-bg-light border fw-normal">custom</span><?php endif; ?></button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#cache" aria-controls="cache" aria-selected="false"><i class="bi bi-speedometer2 me-1"></i>Cache rules<?php if ($state['cache_custom']): ?> <span class="badge rounded-pill text-bg-light border fw-normal">custom</span><?php endif; ?></button>
</div>
<div class="tab-content">

<div class="tab-pane fade show active" id="headers" role="tabpanel">
    <?php if (!$state['headers_custom']): ?>
        <div class="alert alert-info small">These are the recommended settings. Until you save, this website sends only <code>X-Content-Type-Options: nosniff</code><?= !empty($site['security_headers']) ? ' and the older “recommended security headers” from the SSL page' : '' ?>. Saving here replaces that older switch, so no header is sent twice.</div>
    <?php endif; ?>
    <form class="card mb-3" method="post" action="<?= e($this->url('sites.web_policy.headers', ['id' => $id])) ?>" data-nim-busy>
        <?= $this->csrfField() ?>
        <div class="card-header">Security headers</div>
        <div class="card-body">
            <div class="mb-4">
                <label class="form-label fw-medium" for="wp-click">Framing protection (clickjacking)</label>
                <select class="form-select<?= $err('clickjacking') ? ' is-invalid' : '' ?>" id="wp-click" name="clickjacking">
                    <?php foreach (W::CLICKJACKING as $k => [$label]): ?><option value="<?= e($k) ?>"<?= $hv('clickjacking', $h['clickjacking']) === $k ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                </select>
                <?= $fb($err('clickjacking')) ?>
                <div class="form-text">Stops other websites from showing your pages inside a frame to trick visitors into clicking. Sends <code>X-Frame-Options</code> and <code>frame-ancestors</code> together.</div>
                <label class="form-label small mt-2" for="wp-fa">Sites allowed to embed this website <span class="text-body-secondary">(only for “this website and chosen sites”, one https:// address per line)</span></label>
                <textarea class="form-control form-control-sm font-monospace<?= $err('frame_ancestors') ? ' is-invalid' : '' ?>" id="wp-fa" name="frame_ancestors" rows="2" maxlength="1000" placeholder="https://partner.example.com"><?= e((string) $hv('frame_ancestors', implode("\n", (array) $h['frame_ancestors']))) ?></textarea>
                <?= $fb($err('frame_ancestors')) ?>
            </div>

            <div class="mb-4">
                <label class="form-label fw-medium" for="wp-csp">Content Security Policy</label>
                <textarea class="form-control form-control-sm font-monospace<?= $err('csp') ? ' is-invalid' : '' ?>" id="wp-csp" name="csp" rows="4" maxlength="<?= W::MAX_CSP ?>" placeholder="default-src 'self'; img-src 'self' https: data:"><?= e((string) $hv('csp', (string) $h['csp'])) ?></textarea>
                <?= $fb($err('csp')) ?>
                <div class="form-text">Tells browsers where scripts, styles, images and other content may come from. Leave empty to send none. A wrong policy can break your site, so start in report-only mode and check your browser's console. <code>frame-ancestors</code> is set by the framing option above.</div>
                <div class="form-check form-switch mt-2"><input type="hidden" name="csp_report_only" value="0"><input class="form-check-input" type="checkbox" role="switch" name="csp_report_only" value="1" id="wp-cspro"<?= $flag('csp_report_only', !empty($h['csp_report_only'])) ? ' checked' : '' ?>><label class="form-check-label" for="wp-cspro">Report-only: browsers log violations but block nothing</label></div>
            </div>

            <div class="mb-4">
                <label class="form-label fw-medium" for="wp-ref">Referrer policy</label>
                <select class="form-select<?= $err('referrer') ? ' is-invalid' : '' ?>" id="wp-ref" name="referrer">
                    <?php foreach (W::REFERRER as $k => [$label, $help]): ?><option value="<?= e($k) ?>"<?= $hv('referrer', $h['referrer']) === $k ? ' selected' : '' ?>><?= e($label) ?> — <?= e($help) ?></option><?php endforeach; ?>
                </select>
                <?= $fb($err('referrer')) ?>
                <div class="form-text">How much of the page address is sent to other websites when visitors follow a link.</div>
            </div>

            <div class="mb-4">
                <label class="form-label fw-medium" for="wp-perm">Permissions policy</label>
                <select class="form-select<?= $err('permissions') ? ' is-invalid' : '' ?>" id="wp-perm" name="permissions">
                    <?php foreach (W::PERMISSIONS as $k => [$label, $help]): ?><option value="<?= e($k) ?>"<?= $hv('permissions', $h['permissions']) === $k ? ' selected' : '' ?>><?= e($label) ?> — <?= e($help) ?></option><?php endforeach; ?>
                </select>
                <?= $fb($err('permissions')) ?>
                <label class="form-label small mt-2" for="wp-permc">Custom directives <span class="text-body-secondary">(only for “Custom”)</span></label>
                <input class="form-control form-control-sm font-monospace<?= $err('permissions_custom') ? ' is-invalid' : '' ?>" id="wp-permc" name="permissions_custom" maxlength="1500" value="<?= e((string) $hv('permissions_custom', (string) $h['permissions_custom'])) ?>" placeholder='camera=(), geolocation=(self), microphone=(self "https://meet.example.com")'>
                <?= $fb($err('permissions_custom')) ?>
            </div>

            <div class="mb-3">
                <div class="fw-medium mb-1">HSTS (Strict-Transport-Security)</div>
                <?php if (!$state['hsts_available']): ?>
                    <div class="alert alert-secondary small mb-2">HSTS needs working HTTPS with plain HTTP redirected to it. <a href="<?= e($this->url('sites.ssl', ['id' => $id])) ?>">Set that up under SSL</a> first.</div>
                <?php endif; ?>
                <div class="form-check form-switch mb-2"><input type="hidden" name="hsts_enabled" value="0"><input class="form-check-input" type="checkbox" role="switch" name="hsts_enabled" value="1" id="wp-hsts"<?= $hstsOn && $state['hsts_available'] ? ' checked' : '' ?><?= $state['hsts_available'] ? '' : ' disabled' ?>><label class="form-check-label" for="wp-hsts">Make browsers use HTTPS only for this website</label></div>
                <?= $fb($err('hsts_enabled')) ?>
                <div class="row g-2">
                    <div class="col-sm-5">
                        <label class="form-label small" for="wp-hsts-age">Remember for</label>
                        <select class="form-select form-select-sm<?= $err('hsts_max_age') ? ' is-invalid' : '' ?>" id="wp-hsts-age" name="hsts_max_age"<?= $state['hsts_available'] ? '' : ' disabled' ?>>
                            <?php foreach (W::HSTS_AGES as $k => $label): ?><option value="<?= (int) $k ?>"<?= $hstsAge === $k ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                        </select>
                        <?= $fb($err('hsts_max_age')) ?>
                    </div>
                    <div class="col-sm-7 pt-sm-4">
                        <div class="form-check"><input type="hidden" name="hsts_include_subdomains" value="0"><input class="form-check-input" type="checkbox" name="hsts_include_subdomains" value="1" id="wp-hsts-sub"<?= $flag('hsts_include_subdomains', !empty($h['hsts']['include_subdomains'])) ? ' checked' : '' ?><?= $state['hsts_available'] ? '' : ' disabled' ?>><label class="form-check-label small" for="wp-hsts-sub">Include subdomains</label></div>
                        <div class="form-check"><input type="hidden" name="hsts_preload" value="0"><input class="form-check-input" type="checkbox" name="hsts_preload" value="1" id="wp-hsts-pre"<?= $flag('hsts_preload', !empty($h['hsts']['preload'])) ? ' checked' : '' ?><?= $state['hsts_available'] ? '' : ' disabled' ?>><label class="form-check-label small" for="wp-hsts-pre">Preload (needs include subdomains and one year)</label></div>
                        <?= $fb($err('hsts_preload')) ?>
                    </div>
                </div>
                <div class="form-text">Browsers then refuse plain HTTP for this website until the time runs out, so every subdomain you include must also work over HTTPS. This cannot be undone quickly: browsers keep the setting for the full time.</div>
            </div>

            <div class="small text-body-secondary border-top pt-2"><i class="bi bi-lock me-1" aria-hidden="true"></i><code>X-Content-Type-Options: nosniff</code> is always sent by the server and cannot be turned off.</div>
        </div>
        <div class="card-footer d-flex flex-wrap gap-2">
            <button class="btn btn-primary btn-sm"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save and apply</button>
            <?php if ($state['headers_custom']): ?>
                <button class="btn btn-outline-secondary btn-sm" type="submit" form="wp-reset-headers">Restore defaults</button>
            <?php endif; ?>
        </div>
    </form>
    <form id="wp-reset-headers" method="post" action="<?= e($this->url('sites.web_policy.reset', ['id' => $id])) ?>" data-nim-busy data-nim-confirm="Restore the default security headers? Your saved choices are removed and the website returns to its original settings."><?= $this->csrfField() ?><input type="hidden" name="section" value="headers"></form>
</div>

<div class="tab-pane fade" id="cache" role="tabpanel">
    <form class="card mb-3" method="post" action="<?= e($this->url('sites.web_policy.cache', ['id' => $id])) ?>" data-nim-busy>
        <?= $this->csrfField() ?>
        <div class="card-header">Cache rules</div>
        <div class="card-body">
            <div class="mb-4">
                <label class="form-label fw-medium" for="wp-static">Static files (images, styles, scripts, fonts)</label>
                <select class="form-select<?= $err('static_ttl') ? ' is-invalid' : '' ?>" id="wp-static" name="static_ttl">
                    <?php foreach (W::STATIC_TTLS as $k => $label): ?><option value="<?= (int) $k ?>"<?= $presetTtl && $staticTtl === $k ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                </select>
                <?= $fb($err('static_ttl')) ?>
                <label class="form-label small mt-2" for="wp-static-c">Or a custom lifetime in seconds <span class="text-body-secondary">(<?= W::MIN_STATIC_TTL ?> to <?= W::MAX_STATIC_TTL ?>; overrides the choice above)</span></label>
                <input class="form-control form-control-sm" id="wp-static-c" name="static_ttl_custom" type="number" min="<?= W::MIN_STATIC_TTL ?>" max="<?= W::MAX_STATIC_TTL ?>" value="<?= $presetTtl ? '' : e((string) $staticTtl) ?>">
                <div class="form-text">How long visitors' browsers keep these files before asking again (sends <code>Cache-Control: max-age</code> and <code>Expires</code>). Longer is faster, but a changed file reaches visitors later. Only files of these types are affected, never pages behind sign-in.</div>
            </div>

            <div class="mb-4">
                <label class="form-label fw-medium" for="wp-dyn">Pages generated by the application (HTML and dynamic responses)</label>
                <select class="form-select<?= $err('dynamic_mode') ? ' is-invalid' : '' ?>" id="wp-dyn" name="dynamic_mode">
                    <?php foreach (W::DYNAMIC_MODES as $k => [$label, $help]): ?><option value="<?= e($k) ?>"<?= $hv('dynamic_mode', $c['dynamic_mode']) === $k ? ' selected' : '' ?>><?= e($label) ?> — <?= e($help) ?></option><?php endforeach; ?>
                </select>
                <?= $fb($err('dynamic_mode')) ?>
                <label class="form-label small mt-2" for="wp-dyn-ttl">Public cache lifetime in seconds <span class="text-body-secondary">(only for “short public cache”; <?= W::MIN_DYNAMIC_TTL ?> to <?= W::MAX_DYNAMIC_TTL ?>)</span></label>
                <input class="form-control form-control-sm<?= $err('dynamic_ttl') ? ' is-invalid' : '' ?>" id="wp-dyn-ttl" name="dynamic_ttl" type="number" min="<?= W::MIN_DYNAMIC_TTL ?>" max="<?= W::MAX_DYNAMIC_TTL ?>" value="<?= e((string) $hv('dynamic_ttl', (int) $c['dynamic_ttl'])) ?>">
                <?= $fb($err('dynamic_ttl')) ?>
                <div class="form-text">If the application sends its own <code>Cache-Control</code>, it is left alone.</div>
            </div>

            <div class="mb-3">
                <label class="form-label fw-medium" for="wp-bypass">Extra paths that are never cached publicly <span class="text-body-secondary">(one per line)</span></label>
                <textarea class="form-control form-control-sm font-monospace<?= $err('bypass_paths') ? ' is-invalid' : '' ?>" id="wp-bypass" name="bypass_paths" rows="3" maxlength="3000" placeholder="/members&#10;/private-area"><?= e((string) $hv('bypass_paths', implode("\n", (array) $c['bypass_paths']))) ?></textarea>
                <?= $fb($err('bypass_paths')) ?>
                <div class="form-text">Up to <?= W::MAX_BYPASS ?> paths, each a folder such as <code>/members</code>.</div>
            </div>

            <div class="alert alert-secondary small mb-0">
                <div class="fw-medium mb-1"><i class="bi bi-lock me-1" aria-hidden="true"></i>Never cached publicly, whatever you choose</div>
                Whenever a cache rule applies, these always get <code>Cache-Control: private, no-store</code>: sign-in, registration and password pages, administration areas (<code>/wp-admin</code>, <code>/administrator</code>, <code>/admin</code>…), accounts, carts and checkouts, API endpoints, requests with an <code>Authorization</code> header or a session or sign-in cookie, any response that sets a cookie, and every request that is not a plain GET or HEAD.
            </div>
        </div>
        <div class="card-footer d-flex flex-wrap gap-2">
            <button class="btn btn-primary btn-sm"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save and apply</button>
            <?php if ($state['cache_custom']): ?>
                <button class="btn btn-outline-secondary btn-sm" type="submit" form="wp-reset-cache">Restore defaults</button>
            <?php endif; ?>
        </div>
    </form>
    <form id="wp-reset-cache" method="post" action="<?= e($this->url('sites.web_policy.reset', ['id' => $id])) ?>" data-nim-busy data-nim-confirm="Restore the default cache rules? Your saved choices are removed."><?= $this->csrfField() ?><input type="hidden" name="section" value="cache"></form>
</div>

</div>
