<?php
/**
 * The feedback window. NimVor sends nothing by itself: Send opens a new issue on the project's GitHub page in a new
 * tab with the text filled in, so it can be read and changed before it is posted. Only the NimVor version is added.
 *
 * @var \NimVor\View\View $this
 * @var string $version
 */
?>
<div class="modal fade" id="nim-feedback" tabindex="-1" aria-labelledby="nim-feedback-title" aria-hidden="true">
    <div class="modal-dialog modal-dialog-centered">
        <form class="modal-content" data-nim-noajax data-nim-feedback="https://github.com/NimVor/nimvor/issues/new" data-version="<?= e($version) ?>">
            <div class="modal-header">
                <h2 class="modal-title h5" id="nim-feedback-title">Send feedback</h2>
                <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
            </div>
            <div class="modal-body">
                <p class="small text-body-secondary">Tell the NimVor team about a problem, a missing feature or an idea. Nothing is sent from this server: Send opens a new issue on GitHub with your text filled in, so you can read and change it first. Do not include passwords or private details.</p>
                <div class="mb-3">
                    <label class="form-label small" for="nim-feedback-kind">What is it?</label>
                    <select class="form-select form-select-sm" id="nim-feedback-kind" name="kind">
                        <option value="Bug">Something is not working</option>
                        <option value="Feature">A feature I would like</option>
                        <option value="Question">A question</option>
                    </select>
                </div>
                <div class="mb-3">
                    <label class="form-label small" for="nim-feedback-title-input">Short summary</label>
                    <input class="form-control form-control-sm" id="nim-feedback-title-input" name="title" maxlength="120" required>
                </div>
                <div>
                    <label class="form-label small" for="nim-feedback-text">Details</label>
                    <textarea class="form-control" id="nim-feedback-text" name="text" rows="5" maxlength="4000" placeholder="What happened, what you expected, and the steps to repeat it." required></textarea>
                </div>
            </div>
            <div class="modal-footer">
                <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
                <button type="submit" class="btn btn-primary"><i class="bi bi-box-arrow-up-right me-1" aria-hidden="true"></i>Send on GitHub</button>
            </div>
        </form>
    </div>
</div>
