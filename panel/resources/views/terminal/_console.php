<?php
/**
 * The terminal itself (shared by the website and administrator pages).
 *
 * @var \WhaCP\View\View $this
 * @var string $styleNonce
 * @var string $openUrl
 * @var bool $unlocked
 * @var bool $needsCode
 */
?>
<?php $this->start(); ?><link rel="stylesheet" href="<?= e($this->asset('assets/vendor/xterm/xterm.css')) ?>"><?php $this->end('head'); ?>
<div class="card wcp-term" id="wcp-term" data-open-url="<?= e($openUrl) ?>" data-style-nonce="<?= e($styleNonce) ?>" data-unlocked="<?= $unlocked ? '1' : '0' ?>">
    <div class="card-header d-flex align-items-center gap-2 flex-wrap">
        <i class="bi bi-terminal" aria-hidden="true"></i>
        <span class="wcp-state is-off" id="wcp-term-state" role="status">Not connected</span>
        <span class="ms-auto d-flex gap-2">
            <button type="button" class="btn btn-sm btn-primary" id="wcp-term-connect" hidden><i class="bi bi-plug me-1"></i>Connect</button>
            <button type="button" class="btn btn-sm btn-outline-danger" id="wcp-term-close" hidden><i class="bi bi-x-circle me-1"></i>Close session</button>
        </span>
    </div>
    <form class="card-body border-bottom" id="wcp-term-unlock" action="<?= e($this->url('sites.terminal.unlock')) ?>" autocomplete="off" hidden>
        <p class="mb-2"><i class="bi bi-shield-lock me-1" aria-hidden="true"></i>Confirm it is you to open a terminal. The confirmation lasts <?= (int) (\WhaCP\Services\TerminalService::UNLOCK_SECONDS / 60) ?> minutes.</p>
        <div class="row g-2 align-items-end">
            <div class="col-sm-5">
                <label class="form-label small" for="wcp-term-pw">Your panel password</label>
                <input class="form-control" type="password" id="wcp-term-pw" name="password" required autocomplete="current-password">
            </div>
            <?php if ($needsCode): ?>
            <div class="col-sm-3">
                <label class="form-label small" for="wcp-term-code">Authenticator code</label>
                <input class="form-control" type="text" id="wcp-term-code" name="code" inputmode="numeric" required autocomplete="one-time-code">
            </div>
            <?php endif; ?>
            <div class="col-sm-auto"><button class="btn btn-primary" type="submit">Confirm and connect</button></div>
        </div>
        <div class="text-danger small mt-2" data-term-error hidden></div>
    </form>
    <div class="wcp-term-screen" id="wcp-term-screen"></div>
</div>
<?php $this->start(); ?>
<script src="<?= e($this->asset('assets/vendor/xterm/xterm.js')) ?>" defer></script>
<script src="<?= e($this->asset('assets/vendor/xterm/addon-fit.js')) ?>" defer></script>
<script src="<?= e($this->asset('assets/js/terminal.js')) ?>" defer></script>
<?php $this->end('scripts'); ?>
