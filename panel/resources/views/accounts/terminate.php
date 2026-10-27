<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $accounts */
/** @var string $word */
/** @var list<array{username: string, ok: bool, message: string}>|null $results */
$selected = array_column($accounts, 'id');
?>
<?php $this->start(); ?>Terminate accounts<?php $this->end('title'); ?>
<?php $this->start(); ?>Remove hosting accounts with their websites, email, databases and DNS zones<?php $this->end('subtitle'); ?>


<?php if ($results !== null): ?>
    <div class="card" id="terminate-results">
        <div class="card-header">Results</div>
        <ul class="list-group list-group-flush">
            <?php foreach ($results as $r): ?>
                <li class="list-group-item d-flex gap-2 align-items-start">
                    <i class="bi bi-<?= $r['ok'] ? 'check-circle-fill text-success' : 'x-circle-fill text-danger' ?> mt-1" aria-hidden="true"></i>
                    <div><span class="fw-semibold"><?= e($r['username']) ?></span> — <?= e($r['message']) ?></div>
                </li>
            <?php endforeach; ?>
        </ul>
    </div>
    <?php return; ?>
<?php endif; ?>

<div class="alert alert-danger">
    <strong>This cannot be undone.</strong> For each account below, its websites (with their files and system accounts), mailboxes and the mail in them, databases and DNS zones are deleted, then the account itself. Backups stored elsewhere are not touched. Take a backup first if you may need anything.
</div>

<form method="post" action="<?= e($this->url('accounts.terminate')) ?>" data-wcp-busy id="terminate-confirm">
    <?= $this->csrfField() ?>
    <input type="hidden" name="count" value="<?= count($accounts) ?>">
    <div class="card mb-3">
        <div class="wcp-dtable table-responsive">
            <table class="table align-middle mb-0">
                <thead><tr><th>Account</th><th>Websites</th><th class="text-end">Mailboxes</th><th class="text-end">Databases</th><th class="text-end">DNS zones</th></tr></thead>
                <tbody>
                <?php foreach ($accounts as $a): $left = array_diff($a['customers'], $selected); ?>
                    <tr>
                        <td>
                            <input type="hidden" name="ids[]" value="<?= (int) $a['id'] ?>">
                            <span class="fw-semibold"><?= e($a['username']) ?></span> <span class="small text-body-secondary"><?= e($a['role']) ?></span>
                            <?php if ($a['scope'] !== 'own'): ?><div class="small wcp-bad">Not a hosting account: it will be skipped.</div><?php endif; ?>
                            <?php if ($left !== []): ?><div class="small wcp-bad">Has <?= count($left) ?> customer(s) not selected: it will be skipped.</div><?php endif; ?>
                        </td>
                        <td class="small"><?= $a['websites'] === [] ? '—' : e(implode(', ', $a['websites'])) ?></td>
                        <td class="text-end"><?= (int) $a['mailboxes'] ?></td>
                        <td class="text-end"><?= (int) $a['databases'] ?></td>
                        <td class="text-end"><?= (int) $a['zones'] ?></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    </div>
    <div class="row g-2 align-items-end">
        <div class="col-md-5">
            <label class="form-label" for="t-confirm">Type <code><?= e($word) ?></code> to terminate <?= count($accounts) ?> account(s)</label>
            <input class="form-control" id="t-confirm" name="confirm" autocomplete="off" spellcheck="false" required pattern="<?= e($word) ?>">
        </div>
        <div class="col-md-auto">
            <button class="btn btn-danger" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Terminate <?= count($accounts) ?> account(s)</button>
            <a class="btn btn-outline-secondary" href="<?= e($this->url('users')) ?>">Cancel</a>
        </div>
    </div>
</form>
