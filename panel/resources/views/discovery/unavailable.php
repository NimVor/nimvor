<?php /** @var \WhaCP\View\View $this */ ?>
<?php $this->start(); ?><?= e($title) ?><?php $this->end('title'); ?>
<?= $this->partial('partials/agent_error', ['error' => $error]) ?>
<div class="card wcp-empty">
    <div class="card-body text-center py-5">
        <i class="bi bi-plug display-6 text-body-secondary"></i>
        <p class="mt-3 mb-0 text-body-secondary">This page needs the NimVor agent. It will show data as soon as the agent is reachable.</p>
    </div>
</div>
