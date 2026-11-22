<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $sponsors */
/** @var list<string> $tiers */
/** @var bool $hasToken */
/** @var bool $patreonConnected */
/** @var array{github: bool, patreon: bool, manual: bool, patreon_url: string} $display */
/** @var array<string, string> $errors */
/** @var array<string, mixed> $old */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Sponsors');
?>
<div class="app-top"><div><h1>Sponsors</h1><p class="muted mb-0">Shown on the public Sponsors page: the public sponsors from GitHub Sponsors, the active patrons from Patreon, and the ones you add here. Choose below which of them are shown.</p></div></div>
<form class="panel form" method="post" action="/admin/sponsors/display" novalidate data-once>
    <?= $v->csrf() ?><h2 class="mb-0">What the Sponsors page shows</h2>
    <label class="check"><input type="checkbox" name="show_github" value="1"<?= $display['github'] ? ' checked' : '' ?>> Sponsors from GitHub Sponsors</label>
    <label class="check"><input type="checkbox" name="show_patreon" value="1"<?= $display['patreon'] ? ' checked' : '' ?>> Patrons from Patreon</label>
    <label class="check"><input type="checkbox" name="show_manual" value="1"<?= $display['manual'] ? ' checked' : '' ?>> Sponsors added by hand</label>
    <div class="field"><label for="patreon_url">Your Patreon page (for the &ldquo;Support on Patreon&rdquo; button)</label><input class="input" id="patreon_url" name="patreon_url" value="<?= e($old['patreon_url'] ?? $display['patreon_url']) ?>" maxlength="255" placeholder="https://www.patreon.com/yourname"><?= $v->error($errors, 'patreon_url') ?><small class="muted">The button is only shown while Patreon is switched on and this address is set.</small></div>
    <div><button class="btn btn-primary" type="submit">Save</button></div>
</form>
<div class="panel form">
    <h2 class="mb-0">Patreon</h2>
    <?php if ($patreonConnected): ?>
        <p class="muted mb-0">Copies the active patrons of your campaign: name, public Patreon profile link and monthly pledge (silver from $25, gold from $100). No email addresses or payment details are read. Sponsors you added by hand are never changed.</p>
        <form method="post" action="/admin/sponsors/sync-patreon" data-once><?= $v->csrf() ?><button class="btn btn-primary" type="submit">Sync from Patreon now</button></form>
    <?php else: ?>
        <p class="muted mb-0">Not connected. Create a client in the Patreon creator portal (Clients &amp; API) and set <code>PATREON_ACCESS_TOKEN</code> (the creator&rsquo;s access token, scopes <code>campaigns</code> and <code>campaigns.members</code>) in the site&rsquo;s configuration; <code>PATREON_CAMPAIGN_ID</code> is optional. Until then add sponsors by hand below.</p>
    <?php endif; ?>
</div>
<div class="panel form">
    <h2 class="mb-0">GitHub Sponsors</h2>
    <?php if ($hasToken): ?>
        <p class="muted mb-0">Copies the public, active sponsors of the organisation. Tiers follow the monthly amount (silver from $25, gold from $100). Sponsors you added by hand are never changed.</p>
        <form method="post" action="/admin/sponsors/sync" data-once><?= $v->csrf() ?><button class="btn btn-primary" type="submit">Sync from GitHub now</button></form>
    <?php else: ?>
        <p class="muted mb-0">Not connected. GitHub only shows sponsors to a signed-in maintainer: set <code>GITHUB_TOKEN</code> (a token that can read the organisation's sponsors) in the site's configuration, then come back here. Until then add sponsors by hand below.</p>
    <?php endif; ?>
</div>
<form class="panel form" method="post" action="/admin/sponsors" novalidate data-once>
    <?= $v->csrf() ?><h2 class="mb-0">Add a sponsor</h2>
    <div class="field"><label for="name">Name</label><input class="input" id="name" name="name" value="<?= e($old['name'] ?? '') ?>" maxlength="120" required><?= $v->error($errors, 'name') ?></div>
    <div class="field"><label for="url">Link</label><input class="input" id="url" name="url" value="<?= e($old['url'] ?? '') ?>" maxlength="255" placeholder="https://" required><?= $v->error($errors, 'url') ?></div>
    <div class="field"><label for="tier">Tier</label><select class="select" id="tier" name="tier"><?php foreach ($tiers as $t): ?><option value="<?= e($t) ?>"<?= ($old['tier'] ?? '') === $t ? ' selected' : '' ?>><?= e($t) ?></option><?php endforeach; ?></select><?= $v->error($errors, 'tier') ?></div>
    <div><button class="btn btn-primary" type="submit">Add sponsor</button></div>
</form>
<?php if ($sponsors !== []): ?>
<div class="table-wrap"><table class="data"><thead><tr><th>Name</th><th>Link</th><th>Tier</th><th>Shown</th><th></th></tr></thead><tbody>
    <?php foreach ($sponsors as $s): $src = (string) ($s['source'] ?? 'manual'); ?>
        <tr><td><?= e($s['name']) ?><?= $src === 'github' ? ' <span class="badge">GitHub</span>' : ($src === 'patreon' ? ' <span class="badge">Patreon</span>' : '') ?></td><td><?= e($s['url']) ?></td><td><?= e($s['tier']) ?></td>
            <td><?= (int) ($s['visible'] ?? 1) === 1 ? 'Yes' : '<span class="muted">Hidden</span>' ?></td>
            <td class="cols-actions">
                <form class="inline-form" method="post" action="/admin/sponsors/<?= (int) $s['id'] ?>/visibility"><?= $v->csrf() ?><input type="hidden" name="visible" value="<?= (int) ($s['visible'] ?? 1) === 1 ? '0' : '1' ?>"><button class="btn btn-sm" type="submit"><?= (int) ($s['visible'] ?? 1) === 1 ? 'Hide' : 'Show' ?></button></form>
                <?php if ($src === 'manual'): ?>
                    <details class="inline-edit"><summary class="btn btn-sm">Edit</summary>
                        <form method="post" action="/admin/sponsors/<?= (int) $s['id'] ?>/update" novalidate data-once><?= $v->csrf() ?>
                            <div class="field"><label>Name<input class="input" name="name" value="<?= e($s['name']) ?>" maxlength="120" required></label></div>
                            <div class="field"><label>Link<input class="input" name="url" value="<?= e($s['url']) ?>" maxlength="255" required></label></div>
                            <div class="field"><label>Tier<select class="select" name="tier"><?php foreach ($tiers as $t): ?><option value="<?= e($t) ?>"<?= $s['tier'] === $t ? ' selected' : '' ?>><?= e($t) ?></option><?php endforeach; ?></select></label></div>
                            <label class="check"><input type="checkbox" name="visible" value="1"<?= (int) ($s['visible'] ?? 1) === 1 ? ' checked' : '' ?>> Shown on the page</label>
                            <div><button class="btn btn-primary btn-sm" type="submit">Save</button></div>
                        </form>
                    </details>
                    <form class="inline-form" method="post" action="/admin/sponsors/<?= (int) $s['id'] ?>/delete" data-confirm="Remove this sponsor?"><?= $v->csrf() ?><button class="btn btn-danger btn-sm" type="submit">Remove</button></form>
                <?php else: ?><span class="muted">synced</span><?php endif; ?>
            </td></tr>
    <?php endforeach; ?>
</tbody></table></div>
<?php endif; ?>
