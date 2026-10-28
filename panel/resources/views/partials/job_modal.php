<div class="modal fade" id="nim-job" tabindex="-1" aria-labelledby="nim-job-title" aria-hidden="true">
    <div class="modal-dialog modal-lg modal-dialog-centered modal-dialog-scrollable">
        <div class="modal-content">
            <div class="modal-header">
                <h2 class="modal-title h5" id="nim-job-title">Background task</h2>
                <span class="badge text-bg-secondary ms-2" data-nim-job-status>queued</span>
                <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
            </div>
            <div class="modal-body">
                <pre class="nim-pre nim-joblog mb-0" data-nim-job-log aria-live="polite"></pre>
            </div>
            <div class="modal-footer">
                <span class="small text-body-secondary me-auto">The task continues on the server if you close this window.</span>
                <button type="button" class="btn btn-primary d-none" data-nim-job-reload>Reload page</button>
                <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Close</button>
            </div>
        </div>
    </div>
</div>
