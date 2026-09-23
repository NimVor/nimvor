<div class="modal fade" id="wcp-confirm" tabindex="-1" aria-labelledby="wcp-confirm-title" aria-hidden="true">
    <div class="modal-dialog modal-dialog-centered">
        <div class="modal-content">
            <div class="modal-header">
                <h2 class="modal-title h5" id="wcp-confirm-title">Please confirm</h2>
                <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
            </div>
            <div class="modal-body">
                <p class="mb-3" data-wcp-confirm-message></p>
                <div data-wcp-confirm-typed class="d-none">
                    <label class="form-label small" for="wcp-confirm-input">Type <code data-wcp-confirm-word></code> to confirm.</label>
                    <input type="text" class="form-control" id="wcp-confirm-input" autocomplete="off">
                </div>
            </div>
            <div class="modal-footer">
                <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
                <button type="button" class="btn btn-danger" data-wcp-confirm-ok>Confirm</button>
            </div>
        </div>
    </div>
</div>
