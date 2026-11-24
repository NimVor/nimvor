<?php
/**
 * The API tokens tab of the profile: the tokens, and the form that creates one with the permissions it may use.
 *
 * @var \NimVor\View\View $this
 * @var list<array<string, mixed>> $tokens
 * @var array<string, list<array{key: string, label: string}>> $scopeGroups
 * @var array<string, string> $errors
 */
$icons = ['Hosting' => 'bi-globe2', 'Server' => 'bi-hdd-rack', 'Security' => 'bi-shield-check', 'Administration' => 'bi-people'];
?>
<div class="card mb-3">
    <div class="card-header"><i class="bi bi-braces me-1"></i>API tokens</div>
    <div class="card-body">
        <?php if (!empty($newToken)): ?>
            <div class="alert alert-warning">
                <div class="fw-semibold mb-1">Copy your new token now — it is not shown again:</div>
                <div class="d-flex gap-2 align-items-center"><code class="user-select-all text-break" data-nim-new-token><?= e($newToken) ?></code>
                    <button type="button" class="btn btn-sm btn-outline-secondary" data-nim-copy="[data-nim-new-token]"><i class="bi bi-clipboard"></i></button></div>
            </div>
        <?php endif; ?>
        <p class="small text-body-secondary mb-3">Tokens are for administrators and resellers. A token acts as you, but only for the permissions you give it below, and never for more than you may do yourself. Send it as <code>Authorization: Bearer nimvor_…</code> to <code>/api/v1/…</code>; the reference is at <a href="<?= e($this->url('api.openapi')) ?>">/api/v1/openapi.yaml</a>.</p>
        <?php if ($tokens !== []): ?>
            <div class="table-responsive"><table class="table table-sm align-middle mb-0">
                <thead><tr><th>Name</th><th>Access</th><th>Permissions</th><th>Expires</th><th>Last used</th><th></th></tr></thead>
                <tbody>
                <?php foreach ($tokens as $t): $scopes = $t['scopes'] === null ? null : (array) json_decode((string) $t['scopes'], true); ?>
                    <tr>
                        <td><?= e($t['name']) ?><div class="small text-body-secondary"><code><?= e($t['prefix']) ?>…</code></div></td>
                        <td><?= $t['ability'] === 'write' ? 'read & write' : 'read-only' ?></td>
                        <td class="small"><?php if ($scopes === null): ?>everything you may do<?php else: ?><details><summary><?= count($scopes) ?> permissions</summary><div class="text-body-secondary"><?php foreach ($scopes as $sc): ?><code class="d-block"><?= e((string) $sc) ?></code><?php endforeach; ?></div></details><?php endif; ?></td>
                        <td class="small"><?= $t['expires_at'] === null ? 'never' : e(substr((string) $t['expires_at'], 0, 10)) ?></td>
                        <td class="small"><?= $t['last_used_at'] === null ? 'never' : e(time_ago($t['last_used_at'])) . ' from ' . e((string) $t['last_used_ip']) ?></td>
                        <td class="text-end"><form method="post" action="<?= e($this->url('tokens.revoke', ['id' => (int) $t['id']])) ?>" data-nim-confirm="Revoke the token <?= e($t['name']) ?>? Tools using it stop working."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger">Revoke</button></form></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table></div>
        <?php else: ?>
            <p class="small text-body-secondary mb-0">No tokens yet.</p>
        <?php endif; ?>
    </div>
</div>

<form class="card" method="post" action="<?= e($this->url('tokens.store')) ?>">
    <div class="card-header"><i class="bi bi-plus-circle me-1"></i>Create a token</div>
    <div class="card-body">
        <?= $this->csrfField() ?>
        <div class="row g-3 mb-3">
            <div class="col-md-5"><label class="form-label small" for="t-name">Name</label><input class="form-control form-control-sm<?= isset($errors['name']) ? ' is-invalid' : '' ?>" id="t-name" name="name" maxlength="60" placeholder="billing system" required><?php if (isset($errors['name'])): ?><div class="invalid-feedback"><?= e($errors['name']) ?></div><?php endif; ?></div>
            <div class="col-md-4"><label class="form-label small" for="t-ab">Access</label><select class="form-select form-select-sm" id="t-ab" name="ability"><option value="read">Read-only (GET requests)</option><option value="write">Read and write</option></select></div>
            <div class="col-md-3"><label class="form-label small" for="t-exp">Expires</label><select class="form-select form-select-sm" id="t-exp" name="expires_days"><option value="30">in 30 days</option><option value="90" selected>in 90 days</option><option value="365">in 1 year</option><option value="0">never</option></select></div>
        </div>

        <div class="fw-semibold mb-1">Permissions</div>
        <p class="small text-body-secondary">Choose what the token may do. A token with no permission of an area gets <code>403 scope_missing</code> there. Managing something includes viewing it.</p>
        <div class="form-check mb-3 p-3 border rounded bg-body-tertiary">
            <input class="form-check-input ms-0 me-2" type="checkbox" name="all_scopes" value="1" id="t-all" data-nim-scope-all="#t-groups">
            <label class="form-check-label fw-semibold" for="t-all">Everything</label>
            <span class="small text-danger ms-2"><i class="bi bi-exclamation-triangle"></i> Every permission you have, now and in the future.</span>
        </div>
        <?php if (isset($errors['scopes'])): ?><div class="text-danger small mb-2" role="alert"><?= e($errors['scopes']) ?></div><?php endif; ?>
        <div id="t-groups" class="row g-3">
            <?php $n = 0; foreach ($scopeGroups as $group => $items): $n++; $gid = 'tg-' . $n; ?>
                <div class="col-lg-6">
                    <div class="border rounded h-100" id="<?= e($gid) ?>">
                        <div class="d-flex align-items-center px-3 py-2 border-bottom bg-body-tertiary">
                            <span class="fw-semibold"><i class="bi <?= e($icons[$group] ?? 'bi-key') ?> me-1"></i><?= e($group) ?></span>
                            <span class="ms-auto small"><button type="button" class="btn btn-link btn-sm p-0" data-nim-check-all="#<?= e($gid) ?>">all</button> · <button type="button" class="btn btn-link btn-sm p-0" data-nim-check-none="#<?= e($gid) ?>">none</button></span>
                        </div>
                        <div class="p-2">
                            <?php foreach ($items as $i => $it): $cid = $gid . '-' . $i; ?>
                                <div class="form-check py-1">
                                    <input class="form-check-input" type="checkbox" name="scopes[]" value="<?= e($it['key']) ?>" id="<?= e($cid) ?>">
                                    <label class="form-check-label" for="<?= e($cid) ?>"><?= e($it['label']) ?> <code class="small text-body-secondary"><?= e($it['key']) ?></code></label>
                                </div>
                            <?php endforeach; ?>
                        </div>
                    </div>
                </div>
            <?php endforeach; ?>
        </div>
    </div>
    <div class="card-footer"><button class="btn btn-primary btn-sm">Create token</button></div>
</form>
