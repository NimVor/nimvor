<?php
/**
 * The Change Logs window, opened from the NimVor version in the top bar. Its content (search, filter and the timeline of
 * releases) is fetched the first time it opens.
 *
 * @var \NimVor\View\View $this
 * @var string $url
 */
?>
<div class="modal fade" id="nim-changelog" tabindex="-1" aria-labelledby="nim-changelog-title" aria-hidden="true" data-url="<?= e($url) ?>">
    <div class="modal-dialog modal-lg modal-dialog-scrollable">
        <div class="modal-content">
            <div class="modal-header">
                <h2 class="modal-title h5" id="nim-changelog-title">Change Logs</h2>
                <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
            </div>
            <div class="modal-body" data-changelog-body><span class="spinner-border spinner-border-sm" aria-hidden="true"></span> Loading…</div>
        </div>
    </div>
</div>
