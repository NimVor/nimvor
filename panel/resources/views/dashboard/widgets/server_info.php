<?php
/**
 * The Server card: what the agent reports about the machine, and the server tools for administrators.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$info = (array) ($d['info'] ?? []);
$os = (array) ($info['os'] ?? []);
$sys = (array) ($info['system'] ?? []);
?>
<div class="d-flex justify-content-end mb-2"><?= $this->partial('partials/server_tools') ?></div>
<dl class="nim-dl">
    <dt>Hostname</dt><dd><?= e($sys['hostname'] ?? '—') ?></dd>
    <dt>OS</dt><dd><?= e($os['pretty_name'] ?? '—') ?>
        <?php if (empty($os['supported'])): ?><span class="badge text-bg-warning ms-1">unsupported</span><?php elseif (!empty($os['preview'])): ?><span class="badge text-bg-info ms-1" title="Tested, but not yet recommended for production">preview</span><?php endif; ?></dd>
    <dt>Kernel</dt><dd><?= e($sys['kernel'] ?? '—') ?></dd>
    <dt>CPU</dt><dd><?= e(($sys['cpu']['cores'] ?? '?') . ' × ' . ($sys['cpu']['model'] ?? 'unknown')) ?></dd>
    <dt>Uptime</dt><dd data-uptime><?= e(format_duration((float) ($sys['uptime_seconds'] ?? 0))) ?></dd>
    <dt>Timezone</dt><dd><?= e($sys['timezone'] ?? '—') ?></dd>
    <dt>Agent</dt><dd><?= e($info['agent_version'] ?? '—') ?></dd>
</dl>
