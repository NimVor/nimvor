<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var list<array{from: string, to: string, code: int}> $redirects
 * @var int $max
 */

use NimVor\Services\RedirectsService as R;

$id = (int) $site['id'];
$old ??= [];
$errors ??= [];
$open = $errors !== [];
$val = static fn (string $k, string $d = ''): string => $open ? (string) ($old[$k] ?? $d) : $d;
$err = static fn (string $k): string => isset($errors[$k]) ? '<div class="invalid-feedback d-block">' . e((string) $errors[$k]) . '</div>' : '';
$cls = static fn (string $k): string => isset($errors[$k]) ? ' is-invalid' : '';
$scheme = !empty($site['https']) ? 'https://' : 'http://';
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'redirects']) ?>

<p class="small text-body-secondary">Send visitors from an address on <strong><?= e($site['domain']) ?></strong> to another page or website. A redirect matches that exact address; to redirect a whole folder, add a redirect for each address in it.</p>

<div class="card" id="redirects">
    <div class="card-header nim-toolbar">
        <h2 class="nim-toolbar-title"><i class="bi bi-signpost-split me-1"></i>Redirects <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($redirects) ?></span></h2>
        <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#new-redirect"><i class="bi bi-plus-lg me-1"></i>Add redirect</button>
    </div>
    <?= $this->modalStart('new-redirect', 'Add redirect', $open, 'modal-lg') ?>
        <form method="post" action="<?= e($this->url('sites.redirects.add', ['id' => $id])) ?>" data-nim-busy novalidate>
            <?= $this->csrfField() ?>
            <div class="mb-3">
                <label class="form-label" for="rd-code">Type</label>
                <select class="form-select<?= $cls('code') ?>" id="rd-code" name="code">
                    <?php foreach (R::TYPES as $code => [$label, $help]): ?>
                        <option value="<?= (int) $code ?>"<?= (int) $val('code', '301') === $code ? ' selected' : '' ?>><?= e($label) ?> — <?= e($help) ?></option>
                    <?php endforeach; ?>
                </select>
                <?= $err('code') ?>
            </div>
            <div class="mb-3">
                <label class="form-label" for="rd-from">Redirect this address</label>
                <div class="input-group">
                    <span class="input-group-text font-monospace small text-break"><?= e($scheme . $site['domain']) ?></span>
                    <input class="form-control font-monospace<?= $cls('from') ?>" id="rd-from" name="from" value="<?= e($val('from')) ?>" placeholder="/old-page" maxlength="201" required autocomplete="off" spellcheck="false">
                </div>
                <?= $err('from') ?>
                <div class="form-text">The path visitors open, for example <code>/old-page</code> or <code>/blog/2019/post</code>. Use <code>/</code> to redirect the home page.</div>
            </div>
            <div class="mb-3">
                <label class="form-label" for="rd-to">Redirects to</label>
                <input class="form-control font-monospace<?= $cls('to') ?>" id="rd-to" name="to" value="<?= e($val('to')) ?>" placeholder="https://example.com/new-page" maxlength="400" required autocomplete="off" spellcheck="false">
                <?= $err('to') ?>
                <div class="form-text">A full address (<code>https://example.com/new-page</code>) or a path on this website (<code>/new-page</code>).</div>
            </div>
            <div class="d-flex gap-2 justify-content-end">
                <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
                <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Add redirect</button>
            </div>
        </form>
    <?= $this->modalEnd() ?>
    <?php if ($redirects === []): ?>
        <div class="nim-empty-state"><i class="bi bi-signpost-split" aria-hidden="true"></i><div class="fw-medium">No redirects yet</div><div class="small">Add a redirect to send visitors from an old address to a new one.</div></div>
    <?php else: ?>
        <div class="nim-dtable table-responsive">
            <table class="table align-middle mb-0">
                <thead><tr><th>Address</th><th>Redirects to</th><th>Type</th><th class="text-end">Actions</th></tr></thead>
                <tbody>
                <?php foreach ($redirects as $r): ?>
                    <tr>
                        <td class="font-monospace small text-break"><?= e($site['domain'] . $r['from']) ?></td>
                        <td class="font-monospace small text-break"><?= e($r['to']) ?></td>
                        <td class="small"><span class="badge text-bg-<?= $r['code'] === 301 ? 'primary' : 'secondary' ?>"><?= $r['code'] === 301 ? 'Permanent' : ($r['code'] === 302 ? 'Temporary' : (int) $r['code']) ?> · <?= (int) $r['code'] ?></span></td>
                        <td class="text-end text-nowrap">
                            <form class="d-inline" method="post" action="<?= e($this->url('sites.redirects.remove', ['id' => $id])) ?>" data-nim-busy data-nim-confirm="Delete the redirect from <?= e($site['domain'] . $r['from']) ?>? Visitors will open that address as usual.">
                                <?= $this->csrfField() ?><input type="hidden" name="from" value="<?= e($r['from']) ?>">
                                <button class="btn btn-sm btn-outline-danger"><i class="bi bi-trash me-1"></i>Delete</button>
                            </form>
                        </td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
        <div class="card-footer small text-body-secondary"><?= count($redirects) ?> of <?= (int) $max ?> redirects.</div>
    <?php endif; ?>
</div>
