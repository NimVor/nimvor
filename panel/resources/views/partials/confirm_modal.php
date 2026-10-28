<div class="modal fade" id="nim-confirm" tabindex="-1" aria-labelledby="nim-confirm-title" aria-hidden="true">
    <div class="modal-dialog modal-dialog-centered">
        <div class="modal-content">
            <div class="modal-header">
                <h2 class="modal-title h5" id="nim-confirm-title">Please confirm</h2>
                <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
            </div>
            <div class="modal-body">
                <p class="mb-3" data-nim-confirm-message></p>
                <div data-nim-confirm-typed class="d-none">
                    <label class="form-label small" for="nim-confirm-input">Type <code data-nim-confirm-word></code> to confirm.</label>
                    <input type="text" class="form-control" id="nim-confirm-input" autocomplete="off">
                </div>
            </div>
            <div class="modal-footer">
                <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
                <button type="button" class="btn btn-danger" data-nim-confirm-ok>Confirm</button>
            </div>
        </div>
    </div>
</div>
