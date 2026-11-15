<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var array{mode: string, codes: list<string>, status: int} $cur
 * @var array<string, string> $modes
 * @var array<string, string> $all
 * @var bool $loaded
 */
$id = (int) $site['id'];
$chosen = array_flip($cur['codes']);
$isAdmin = $this->can(\NimVor\Security\Permissions::SECURITY_MANAGE);
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'countries']) ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php return; endif; ?>
<p class="small text-body-secondary">Refuse visitors from chosen countries, or serve only visitors from them. The country of an address comes from the DB-IP country database (<a href="https://db-ip.com" rel="noopener" target="_blank">IP Geolocation by DB-IP</a>), which is approximate: use it to cut unwanted traffic, not as a legal barrier. Private addresses are always served, an address you <em>allow</em> in the <a href="<?= e($this->url('sites.accesslists', ['id' => $id])) ?>">IP, browser &amp; URL lists</a> is never refused, and certificate renewal is never refused.</p>
<?php if (!$loaded): ?>
    <div class="nim-infobar mb-3"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>The country database is not loaded yet, so a restriction cannot be saved.<?= $isAdmin ? ' Load it under <a href="' . e($this->url('security.hulk')) . '">Security → Brute Force Protection → Countries Management</a>.' : ' Ask an administrator to load it.' ?></span></div>
<?php endif; ?>
<form method="post" action="<?= e($this->url('sites.countries.save', ['id' => $id])) ?>" data-nim-busy>
    <?= $this->csrfField() ?>
    <div class="nim-panel mb-3" data-nim-dt>
        <div class="px-3 py-2 border-bottom">
            <?php foreach ($modes as $k => $label): ?>
                <div class="form-check form-check-inline"><input class="form-check-input" type="radio" name="mode" id="sc-mode-<?= e($k) ?>" value="<?= e($k) ?>"<?= $cur['mode'] === $k ? ' checked' : '' ?>><label class="form-check-label" for="sc-mode-<?= e($k) ?>"><?= e($label) ?></label></div>
            <?php endforeach; ?>
        </div>
        <div class="nim-actionbar">
            <button class="btn btn-sm btn-primary">Save</button>
            <span class="small text-body-secondary"><?= count($cur['codes']) ?> selected now</span>
            <input class="form-control form-control-sm ms-auto nim-w-30" type="search" placeholder="Search countries" aria-label="Search countries" data-nim-dt-search>
        </div>
        <div class="nim-dtable table-responsive">
            <table class="table align-middle">
                <thead><tr><th class="nim-col-check"><input class="form-check-input" type="checkbox" aria-label="Select all visible countries" data-nim-select-all="countries[]"></th><th>Country</th><th>Code</th></tr></thead>
                <tbody>
                <?php foreach ($all as $code => $name): ?>
                    <tr data-nim-row>
                        <td><input class="form-check-input" type="checkbox" name="countries[]" value="<?= e($code) ?>" aria-label="<?= e($name) ?>"<?= isset($chosen[$code]) ? ' checked' : '' ?>></td>
                        <td><?= e($name) ?></td>
                        <td class="font-monospace small"><?= e($code) ?></td>
                    </tr>
                <?php endforeach; ?>
                <tr class="d-none" data-nim-dt-empty><td colspan="3" class="text-center py-4 nim-muted">No country matches.</td></tr>
                </tbody>
            </table>
        </div>
        <div class="nim-pager" data-nim-dt-pager></div>
    </div>
</form>
<ul class="small text-body-secondary mt-3">
    <li>What a refused visitor gets (403, 404 or no answer) is set on the <a href="<?= e($this->url('sites.accesslists', ['id' => $id])) ?>">IP, browser &amp; URL lists</a> page and applies to both.</li>
    <li>"Serve only" refuses every address the database does not place in a selected country, including search engine crawlers from other countries. Use it for websites with a local audience.</li>
    <li>The address ranges are those of the database at the time you save; save again after the database is updated (monthly) to refresh them.</li>
    <li>Visitors are matched by their address: behind a CDN or proxy every visitor has the proxy's address unless the real address is restored first.</li>
</ul>
