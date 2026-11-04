// NimVor PostgreSQL create / change dialogs: one modal, filled from a <template data-pgd-tpl="..."> on the page.
// A button opens a dialog with data-pgd-open="template" and data-pgd-set='{"field": "value"}' (sets the named fields
// and the data-pgd-fill texts). "Show SQL" posts the form to the template's data-preview address (a dry run on the
// agent); submitting posts the form normally and the server answers with a message, like the other PostgreSQL forms.
(function () {
    'use strict';
    var dlg = document.getElementById('pg-dialog');
    if (!dlg || !window.bootstrap) { return; }
    var form = dlg.querySelector('[data-pgd-form]');
    var body = dlg.querySelector('[data-pgd-body]');
    var title = dlg.querySelector('[data-pgd-title]');
    var opField = dlg.querySelector('[data-pgd-op]');
    var ok = dlg.querySelector('[data-pgd-ok]');
    var previewBtn = dlg.querySelector('[data-pgd-preview]');
    var sqlOut = dlg.querySelector('[data-pgd-sql]');
    var errOut = dlg.querySelector('[data-pgd-err]');
    var modal = bootstrap.Modal.getOrCreateInstance(dlg);
    var current = null;
    var values = {};

    function setField(name, value) {
        var els = form.querySelectorAll('[name="' + name.replace(/"/g, '') + '"]');
        Array.prototype.forEach.call(els, function (el) {
            if (el.type === 'checkbox') { el.checked = !!value; } else if (el.type !== 'radio') { el.value = value === null || value === undefined ? '' : value; }
        });
    }

    function syncConfirm() {
        var box = body.querySelector('[data-pgd-confirm]');
        if (!box) { ok.disabled = false; return; }
        var word = String(values[box.getAttribute('data-pgd-confirm')] || '');
        ok.disabled = word === '' || box.value.trim() !== word;
    }

    function open(name, set) {
        var tpl = document.querySelector('template[data-pgd-tpl="' + name + '"]');
        if (!tpl) { return; }
        current = tpl;
        values = set || {};
        body.replaceChildren(tpl.content.cloneNode(true));
        title.textContent = tpl.getAttribute('data-title') || '';
        ok.textContent = tpl.getAttribute('data-ok') || 'Save';
        ok.classList.toggle('btn-danger', tpl.hasAttribute('data-danger'));
        ok.classList.toggle('btn-primary', !tpl.hasAttribute('data-danger'));
        ok.disabled = false;
        form.action = tpl.getAttribute('data-action') || '';
        opField.value = tpl.getAttribute('data-op') || '';
        previewBtn.classList.toggle('d-none', !tpl.getAttribute('data-preview'));
        sqlOut.textContent = '';
        errOut.textContent = '';
        Object.keys(values).forEach(function (k) { setField(k, values[k]); });
        Array.prototype.forEach.call(body.querySelectorAll('[data-pgd-fill]'), function (el) { el.textContent = String(values[el.getAttribute('data-pgd-fill')] || ''); });
        var box = body.querySelector('[data-pgd-confirm]');
        if (box) { box.addEventListener('input', syncConfirm); }
        syncConfirm();
        modal.show();
    }

    dlg.addEventListener('shown.bs.modal', function () {
        var first = body.querySelector('input:not([type="hidden"]):not([type="checkbox"]), select, textarea');
        if (first) { first.focus(); }
    });

    document.addEventListener('click', function (e) {
        var btn = e.target.closest('[data-pgd-open]');
        if (!btn) { return; }
        e.preventDefault();
        var set = {};
        try { set = JSON.parse(btn.getAttribute('data-pgd-set') || '{}'); } catch (err) { set = {}; }
        open(btn.getAttribute('data-pgd-open'), set);
    });

    var previewing = false; // not previewBtn.disabled: a button that disables itself loses focus, and Escape then no longer reaches the dialog
    previewBtn.addEventListener('click', function () {
        if (!current || previewing) { return; }
        sqlOut.textContent = '';
        errOut.textContent = '';
        previewing = true;
        fetch(current.getAttribute('data-preview'), { method: 'POST', body: new FormData(form), credentials: 'same-origin', headers: { 'Accept': 'application/json', 'X-Requested-With': 'XMLHttpRequest' } })
            .then(function (res) { return res.json().then(function (b) { return { ok: res.ok && b.success !== false, body: b }; }); })
            .then(function (r) {
                if (r.ok) { sqlOut.textContent = (r.body.data && r.body.data.sql) || '(nothing to run)'; } else { errOut.textContent = (r.body.error && r.body.error.message) || 'The SQL could not be prepared.'; }
            })
            .catch(function () { errOut.textContent = 'The SQL could not be prepared.'; })
            .then(function () { previewing = false; });
    });
})();
