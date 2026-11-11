<?php /** @var \NimVor\View\View $this */ ?>
<?= $this->partial('partials/confirm_modal') ?>
<?= $this->partial('partials/job_modal') ?>
<div class="toast-container position-fixed bottom-0 end-0 p-3" id="nim-toasts"></div>
<script src="<?= e($this->asset('assets/vendor/bootstrap/js/bootstrap.bundle.min.js')) ?>"></script>
<script src="<?= e($this->asset('assets/js/nimvor.js')) ?>"></script>
<script src="<?= e($this->asset('assets/js/themes/common.js')) ?>"></script>
<?= $this->section('scripts') ?>
</body>
</html>
