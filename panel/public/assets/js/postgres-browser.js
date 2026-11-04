// NimVor PostgreSQL table browser: column visibility and the add / edit row window.
(function () {
    'use strict';
    var grid = document.getElementById('pgb-grid');
    var store = null;
    try { store = window.localStorage; } catch (e) { store = null; }
    var storeKey = grid ? 'pgb-hidden:' + location.search.replace(/[&?](page|q|fc|fo|fv|sort|dir)=[^&]*/g, '') : '';

    function setColumn(i, on) {
        document.querySelectorAll('#pgb-grid [data-c="' + i + '"]').forEach(function (el) { el.classList.toggle('d-none', !on); });
    }
    document.addEventListener('change', function (e) {
        var box = e.target.closest('[data-pgb-col]');
        if (!box) { return; }
        setColumn(box.getAttribute('data-pgb-col'), box.checked);
        var hidden = [];
        document.querySelectorAll('[data-pgb-col]').forEach(function (b) { if (!b.checked) { hidden.push(b.getAttribute('data-pgb-col')); } });
        try { if (store) { store.setItem(storeKey, JSON.stringify(hidden)); } } catch (err) { /* a private window: fine */ }
    });
    try {
        var saved = store && storeKey ? JSON.parse(store.getItem(storeKey) || '[]') : [];
        saved.forEach(function (i) {
            var box = document.querySelector('[data-pgb-col="' + i + '"]');
            if (box) { box.checked = false; setColumn(i, false); }
        });
    } catch (err) { /* ignore a damaged saved value */ }

    // The Insert tab: typing in a value box means "set it" (the same as in the edit window).
    document.addEventListener('input', function (e) {
        var t = e.target.closest ? e.target.closest('[data-pgb-text]') : null;
        if (!t) { return; }
        var sel = t.form ? t.form.elements[t.name.replace(/^value\[/, 'mode[')] : null;
        if (sel && !sel.disabled && sel.value !== 'value') { sel.value = 'value'; }
    });

    var modal = document.getElementById('pgb-row');
    if (!modal) { return; }
    var columns = [];
    try { columns = JSON.parse(modal.getAttribute('data-columns') || '[]'); } catch (err) { columns = []; }

    function el(tag, attrs, text) {
        var n = document.createElement(tag);
        Object.keys(attrs || {}).forEach(function (k) { n.setAttribute(k, attrs[k]); });
        if (text) { n.textContent = text; }
        return n;
    }

    // Each column gets a choice: what to do with it (value / NULL / default, or keep when editing) and the text.
    modal.addEventListener('show.bs.modal', function (e) {
        var b = e.relatedTarget;
        if (!b) { return; }
        var edit = b.getAttribute('data-pgb-mode') === 'edit';
        var form = modal.querySelector('[data-pgb-form]') || modal.querySelector('form');
        var holder = modal.querySelector('[data-pgb-fields]');
        var values = {}, key = {}, cut = [];
        try { values = JSON.parse(b.getAttribute('data-values') || '{}'); key = JSON.parse(b.getAttribute('data-key') || '{}'); cut = JSON.parse(b.getAttribute('data-cut') || '[]'); } catch (err) { /* empty */ }
        form.setAttribute('action', form.getAttribute(edit ? 'data-update' : 'data-insert'));
        modal.querySelector('#pgb-row-t').textContent = edit ? 'Edit row' : 'Add a row';
        holder.replaceChildren();
        form.querySelectorAll('input[name^="key["]').forEach(function (n) { n.remove(); });
        Object.keys(key).forEach(function (k) {
            var h = el('input', { type: 'hidden', name: 'key[' + k + ']' });
            h.value = key[k] === null ? '' : key[k];
            form.appendChild(h);
        });
        columns.forEach(function (c) {
            var generated = !!c.generated;
            var row = el('div', { 'class': 'row g-2 align-items-start mb-2' });
            var label = el('div', { 'class': 'col-md-3 small' });
            label.appendChild(el('div', { 'class': 'font-monospace fw-medium' }, c.name));
            label.appendChild(el('div', { 'class': 'text-body-secondary' }, c.type + (c.not_null ? ' · not null' : '')));
            var mode = el('select', { 'class': 'form-select form-select-sm', name: 'mode[' + c.name + ']', 'aria-label': 'What to do with ' + c.name });
            var opts = edit ? [['keep', 'Keep'], ['value', 'Set to'], ['null', 'NULL']] : [['default', c.default ? 'Default' : 'Default / NULL'], ['value', 'Value'], ['null', 'NULL']];
            opts.forEach(function (o) { mode.appendChild(el('option', { value: o[0] }, o[1])); });
            var text = el('textarea', { 'class': 'form-control form-control-sm font-monospace', rows: '1', name: 'value[' + c.name + ']', 'aria-label': 'Value of ' + c.name });
            if (edit && values[c.name] !== null && values[c.name] !== undefined) { text.value = values[c.name]; }
            var isCut = cut.indexOf(c.name) !== -1;
            if (generated || (edit && isCut)) {
                mode.disabled = true; text.disabled = true;
                mode.appendChild(el('option', { value: 'keep', selected: 'selected' }, generated ? 'Generated' : 'Too long to edit here'));
                mode.value = 'keep';
            }
            if (edit && values[c.name] === null) { mode.value = 'keep'; }
            // Typing in the box means "set it".
            text.addEventListener('input', function () { if (!mode.disabled && mode.value !== 'value') { mode.value = 'value'; } });
            var cm = el('div', { 'class': 'col-md-3' }); cm.appendChild(mode);
            var ct = el('div', { 'class': 'col-md-6' }); ct.appendChild(text);
            row.appendChild(label); row.appendChild(cm); row.appendChild(ct);
            holder.appendChild(row);
        });
    });
})();
