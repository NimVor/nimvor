<?php
/**
 * The terminal itself (shared by the website and administrator pages).
 *
 * @var \NimVor\View\View $this
 * @var string $styleNonce
 * @var string $openUrl
 * @var bool $unlocked
 * @var bool $needsCode
 * @var string|null $account the website's Linux account (null: a root shell)
 */
$account ??= null;
?>
<?php $this->start(); ?><link rel="stylesheet" href="<?= e($this->asset('assets/vendor/xterm/xterm.css')) ?>"><?php $this->end('head'); ?>
<div class="card nim-term" id="nim-term" data-open-url="<?= e($openUrl) ?>" data-style-nonce="<?= e($styleNonce) ?>" data-unlocked="<?= $unlocked ? '1' : '0' ?>" data-auth="<?= $account !== null ? 'linux' : 'panel' ?>">
    <div class="card-header d-flex align-items-center gap-2 flex-wrap">
        <i class="bi bi-terminal" aria-hidden="true"></i>
        <span class="nim-state is-off" id="nim-term-state" role="status">Not connected</span>
        <span class="ms-auto d-flex gap-2">
            <button type="button" class="btn btn-sm btn-primary" id="nim-term-connect" hidden><i class="bi bi-plug me-1"></i>Connect</button>
            <button type="button" class="btn btn-sm btn-outline-danger" id="nim-term-close" hidden><i class="bi bi-x-circle me-1"></i>Close session</button>
        </span>
    </div>
    <?php if ($account !== null): ?>
    <?php /* The password goes from the browser to the server agent inside the
       WebSocket only; the input has no name, so it is never submitted to
       the panel as a form field. */ ?>
    <form class="card-body border-bottom" id="nim-term-login" method="post" autocomplete="on" hidden>
        <p class="mb-2"><i class="bi bi-person-lock me-1" aria-hidden="true"></i>Sign in as the Linux account <code><?= e($account) ?></code> with its password (the same as for SSH and SFTP).</p>
        <input type="text" autocomplete="username" value="<?= e($account) ?>" hidden readonly aria-hidden="true">
        <div class="row g-2 align-items-end">
            <div class="col-sm-5">
                <label class="form-label small" for="nim-term-lpw">Password for <?= e($account) ?></label>
                <input class="form-control" type="password" id="nim-term-lpw" required autocomplete="current-password" maxlength="512">
            </div>
            <div class="col-sm-auto"><button class="btn btn-primary" type="submit">Sign in</button></div>
        </div>
        <div class="text-danger small mt-2" data-term-error hidden></div>
        <div class="form-text">No password yet, or forgotten? Set it on the website's FTP / SFTP page (SSH password login).</div>
    </form>
    <?php endif; ?>
    <?php if ($account === null): ?>
    <form class="card-body border-bottom" id="nim-term-unlock" action="<?= e($this->url('sites.terminal.unlock')) ?>" autocomplete="off" hidden>
        <p class="mb-2"><i class="bi bi-shield-lock me-1" aria-hidden="true"></i>Confirm it is you to open a terminal. The confirmation lasts <?= (int) (\NimVor\Services\TerminalService::UNLOCK_SECONDS / 60) ?> minutes.</p>
        <div class="row g-2 align-items-end">
            <div class="col-sm-5">
                <label class="form-label small" for="nim-term-pw">Your panel password</label>
                <input class="form-control" type="password" id="nim-term-pw" name="password" required autocomplete="current-password">
            </div>
            <?php if ($needsCode): ?>
            <div class="col-sm-3">
                <label class="form-label small" for="nim-term-code">Authenticator code</label>
                <input class="form-control" type="text" id="nim-term-code" name="code" inputmode="numeric" required autocomplete="one-time-code">
            </div>
            <?php endif; ?>
            <div class="col-sm-auto"><button class="btn btn-primary" type="submit">Confirm and connect</button></div>
        </div>
        <div class="text-danger small mt-2" data-term-error hidden></div>
    </form>
    <?php endif; ?>
    <div class="nim-term-screen" id="nim-term-screen"></div>
</div>
<?php $this->start(); ?>
<script src="<?= e($this->asset('assets/vendor/xterm/xterm.js')) ?>" defer></script>
<script src="<?= e($this->asset('assets/vendor/xterm/addon-fit.js')) ?>" defer></script>
<script src="<?= e($this->asset('assets/js/terminal.js')) ?>" defer></script>
<?php $this->end('scripts'); ?>
