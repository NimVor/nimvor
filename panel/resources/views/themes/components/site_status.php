<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $site */
?>
<?php if (!empty($site['account_suspended'])): ?><span class="nt-chip is-bad">Suspended</span>
<?php elseif ($site['status'] === 'error'): ?><span class="nt-chip is-bad">Needs attention</span>
<?php elseif ($site['enabled']): ?><span class="nt-chip is-ok">Active</span>
<?php else: ?><span class="nt-chip is-off">Disabled</span><?php endif; ?>
