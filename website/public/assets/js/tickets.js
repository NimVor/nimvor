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

    // Deleting from the list asks first.
    document.querySelectorAll('form[data-bulk]').forEach(function (f) {
        f.addEventListener('submit', function (e) {
            var a = f.querySelector('select[name="action"]');
            var n = f.querySelectorAll('input[name="ids[]"]:checked').length;
            if (a && a.value === 'delete' && n > 0 && !window.confirm('Delete ' + n + ' ticket(s) and all their messages for good? This cannot be undone.')) {
                e.preventDefault();
            }
        });
    });

    // "Select all" in the ticket list.
    var all = document.querySelector('[data-select-all]');
    if (all) {
        all.addEventListener('change', function () {
            document.querySelectorAll('[data-bulk] input[name="ids[]"]').forEach(function (box) { box.checked = all.checked; });
        });
    }
})();
