// Ticket pages. No inline scripts (the Content-Security-Policy only allows files).
(function () {
    'use strict';

    // Choosing a canned reply puts its text in the answer box (added to what is already typed).
    var canned = document.querySelector('[data-canned]');
    var body = document.getElementById('body');
    if (canned && body) {
        canned.addEventListener('change', function () {
            var opt = canned.options[canned.selectedIndex];
            var text = opt ? opt.getAttribute('data-body') : '';
            if (text) {
                body.value = body.value === '' ? text : body.value.replace(/\s+$/, '') + '\n\n' + text;
                body.focus();
            }
            canned.selectedIndex = 0;
        });
    }

    // "Select all" in the ticket list.
    var all = document.querySelector('[data-select-all]');
    if (all) {
        all.addEventListener('change', function () {
            document.querySelectorAll('[data-bulk] input[name="ids[]"]').forEach(function (box) { box.checked = all.checked; });
        });
    }
})();
