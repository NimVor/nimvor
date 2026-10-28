<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var list<array{path: string, user: string, realm: string}> $paths
 * @var int $max
 */
$id = (int) $site['id'];
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'access']) ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php return; endif; ?>

<p class="small text-body-secondary">Password-protect <strong><?= e($site['domain']) ?></strong>, or one folder of it: visitors then get the browser's sign-in prompt and need the user name and password you set here (HTTP basic authentication). Search engines and the public cannot see what is behind it. The password is stored only as a hash.</p>

<div class="row g-3">
    <div class="col-lg-7">
        <div class="card h-100">
            <div class="card-header">Protected addresses <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($paths) ?></span></div>
            <?php if ($paths === []): ?>
                <div class="nim-empty-state"><i class="bi bi-person-lock" aria-hidden="true"></i><div class="fw-medium">Anyone can open this website</div><div class="small">Turn site access on to ask for a user name and password.</div></div>
            <?php else: ?>
                <div class="nim-dtable table-responsive">
                    <table class="table align-middle mb-0">
                        <thead><tr><th>Address</th><th>User</th><th class="text-end"></th></tr></thead>
                        <tbody>
                        <?php foreach ($paths as $p): ?>
                            <tr>
                                <td class="font-monospace small text-break"><?= e($site['domain'] . $p['path']) ?><?php if ($p['path'] === '/'): ?> <span class="badge text-bg-secondary">whole website</span><?php endif; ?></td>
                                <td class="font-monospace small"><?= e($p['user']) ?></td>
                                <td class="text-end text-nowrap">
                                    <form class="d-inline" method="post" action="<?= e($this->url('sites.access.remove', ['id' => $id])) ?>" data-nim-busy data-nim-confirm="Turn site access off for <?= e($site['domain'] . $p['path']) ?>? Anyone will be able to open it.">
                                        <?= $this->csrfField() ?><input type="hidden" name="path" value="<?= e($p['path']) ?>">
                                        <button class="btn btn-sm btn-outline-danger">Turn off</button>
                                    </form>
                                </td>
                            </tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
            <?php endif; ?>
        </div>
    </div>
    <div class="col-lg-5">
        <div class="card h-100">
            <div class="card-header">Turn site access on or change it</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('sites.access.protect', ['id' => $id])) ?>" data-nim-busy autocomplete="off">
                    <?= $this->csrfField() ?>
                    <div class="mb-2">
                        <label class="form-label small" for="sa-path">What to protect</label>
                        <input class="form-control form-control-sm font-monospace" id="sa-path" name="path" value="/" maxlength="200" required>
                        <div class="form-text"><code>/</code> protects the whole website; <code>/admin/</code> only that folder. Using an address that is already protected changes its user and password.</div>
                    </div>
                    <div class="mb-2">
                        <label class="form-label small" for="sa-user">User name</label>
                        <input class="form-control form-control-sm" id="sa-user" name="user" maxlength="64" required autocomplete="off">
                    </div>
                    <div class="mb-2">
                        <label class="form-label small" for="sa-password">Password</label>
                        <input class="form-control form-control-sm" id="sa-password" name="password" type="password" minlength="8" maxlength="128" required autocomplete="new-password">
                        <div class="form-text">8 to 128 characters.</div>
                    </div>
                    <div class="mb-3">
                        <label class="form-label small" for="sa-realm">Message in the sign-in prompt <span class="text-body-secondary">(optional)</span></label>
                        <input class="form-control form-control-sm" id="sa-realm" name="realm" maxlength="64" placeholder="Restricted area">
                    </div>
                    <button class="btn btn-primary btn-sm"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save</button>
                    <span class="small text-body-secondary ms-2">Up to <?= (int) $max ?> protected addresses.</span>
                </form>
            </div>
        </div>
    </div>
</div>
