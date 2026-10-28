<?php /** @var array{title: string, user: string, password: string}|null $secret */ ?>
<?php if (!empty($secret)): ?>
    <div class="alert alert-warning" role="alert">
        <div class="fw-semibold mb-2"><i class="bi bi-key me-1"></i><?= e($secret['title']) ?> — copy the password now</div>
        <div class="row g-2 align-items-center small">
            <div class="col-sm-3 text-body-secondary">User</div>
            <div class="col-sm-9"><code><?= e($secret['user']) ?></code></div>
            <div class="col-sm-3 text-body-secondary">Password</div>
            <div class="col-sm-9 d-flex gap-2 align-items-center">
                <code class="user-select-all" data-nim-secret><?= e($secret['password']) ?></code>
                <button type="button" class="btn btn-sm btn-outline-secondary" data-nim-copy="[data-nim-secret]"><i class="bi bi-clipboard"></i> Copy</button>
            </div>
        </div>
        <div class="small mt-2">This password is shown only once and is not stored unless you chose to keep it encrypted.</div>
    </div>
<?php endif; ?>
