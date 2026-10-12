<?php
/** @var \WhaCP\View\View $this */
/** @var string $old */
/** @var array<string, mixed>|null $report */
/** @var string|null $error */
$findings = (array) ($report['findings'] ?? []);
$current = (string) ($report['current'] ?? '');
$mailStale = !empty($report['mail_stale']);
$byCat = [];
foreach ($findings as $f) {
    $f = (array) $f;
    $byCat[(string) $f['category']][] = $f;
}
$meta = [
    'hosts' => ['label' => '/etc/hosts', 'icon' => 'hdd-network', 'hint' => 'Changing the host name refreshes this automatically. If it is still here, change the host name again on the Server page.', 'link' => null],
    'nginx' => ['label' => 'Nginx sites', 'icon' => 'hdd-stack', 'hint' => 'The host-name certificate site is rewritten when you re-issue it.', 'link' => ['url' => $this->url('ssl.services'), 'text' => 'Re-issue on Services SSL']],
    'mail' => ['label' => 'Mail server', 'icon' => 'envelope', 'hint' => 'Postfix greets with its own host name; re-apply the mail server to use the current one.', 'link' => ['url' => $this->url('mail'), 'text' => 'Email settings']],
    'dns' => ['label' => 'DNS zones', 'icon' => 'diagram-3', 'hint' => 'These zone records were written under the old name. Edit the record (MX target, name server, mail host) in the zone, then re-publish it.', 'link' => ['url' => $this->url('dns'), 'text' => 'DNS zones']],
];
?>
<?php $this->start(); ?>Find the old host name left in DNS zones, Nginx and mail after a rename<?php $this->end('subtitle'); ?>

<div class="wcp-crumbs mb-3"><a href="<?= e($this->url('server')) ?>">Server</a> <span>/</span> Host-name references</div>

<div class="card mb-3">
    <div class="card-body">
        <p class="small text-body-secondary">Changing the server host name updates <code>/etc/hosts</code> and the mail server, but names recorded earlier — DNS zone records and Nginx server names — keep the old value until edited. Enter the <strong>previous</strong> host name to find where it still appears. This only reads configuration; it changes nothing.</p>
        <form method="get" action="<?= e($this->url('server.hostname_references')) ?>" class="d-flex flex-wrap gap-2 align-items-end" data-wcp-busy>
            <div class="flex-grow-1 wcp-minw-0" style="max-width:28rem">
                <label class="form-label" for="old">Previous host name</label>
                <input class="form-control" id="old" name="old" value="<?= e($old) ?>" placeholder="vmi123.example-provider.net" required pattern="[A-Za-z0-9.-]+" maxlength="253">
            </div>
            <button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Scan</button>
        </form>
    </div>
</div>

<?php if ($error !== null): ?>
    <div class="alert alert-danger small"><i class="bi bi-exclamation-octagon me-1"></i><?= e($error) ?></div>
<?php elseif ($report !== null): ?>
    <?php if ($findings === [] && !$mailStale): ?>
        <div class="alert alert-success small"><i class="bi bi-check-circle me-1"></i>No references to <code><?= e($old) ?></code> were found in the managed configuration. The current host name is <code><?= e($current) ?></code>.</div>
    <?php else: ?>
        <div class="alert alert-warning small"><i class="bi bi-exclamation-triangle me-1"></i><?= count($findings) ?> reference<?= count($findings) === 1 ? '' : 's' ?> to <code><?= e($old) ?></code> found. The current host name is <code><?= e($current) ?></code>. Fix each below; nothing is changed automatically.</div>

        <?php if ($mailStale): ?>
            <div class="card mb-3 border-warning">
                <div class="card-body d-flex flex-wrap gap-2 align-items-center">
                    <div class="flex-grow-1 small"><i class="bi bi-envelope me-1"></i>The mail server still greets as <code><?= e((string) $report['mail_hostname']) ?></code>. Re-apply it to use <code><?= e($current) ?></code>, then re-publish each mail domain's DNS.</div>
                    <form method="post" action="<?= e($this->url('server.hostname_references.mail')) ?>" data-wcp-busy data-wcp-confirm="Re-apply the mail server with the host name <?= e($current) ?>?">
                        <?= $this->csrfField() ?>
                        <button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Re-apply mail server</button>
                    </form>
                </div>
            </div>
        <?php endif; ?>

        <?php foreach (['dns', 'nginx', 'mail', 'hosts'] as $cat): ?>
            <?php if (empty($byCat[$cat])) { continue; } $m = $meta[$cat]; ?>
            <div class="card mb-3">
                <div class="card-header wcp-toolbar">
                    <h2 class="wcp-toolbar-title"><i class="bi bi-<?= e($m['icon']) ?> me-1"></i><?= e($m['label']) ?> <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($byCat[$cat]) ?></span></h2>
                    <?php if ($m['link'] !== null): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($m['link']['url']) ?>"><?= e($m['link']['text']) ?></a><?php endif; ?>
                </div>
                <div class="card-body small">
                    <p class="text-body-secondary mb-2"><?= e($m['hint']) ?></p>
                    <div class="table-responsive">
                        <table class="table table-sm wcp-table mb-0">
                            <thead><tr><th>File</th><th>Line</th></tr></thead>
                            <tbody>
                                <?php foreach ($byCat[$cat] as $f): $f = (array) $f; ?>
                                    <tr><td class="font-monospace text-truncate" style="max-width:22rem"><?= e((string) $f['path']) ?></td><td class="font-monospace small"><?= e((string) $f['sample']) ?></td></tr>
                                <?php endforeach; ?>
                            </tbody>
                        </table>
                    </div>
                </div>
            </div>
        <?php endforeach; ?>
    <?php endif; ?>
<?php endif; ?>
