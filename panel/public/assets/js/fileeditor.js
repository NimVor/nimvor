// NimVor full-page file editor. Saves are refused by the agent when the file
// changed since it was opened (optimistic concurrency).
(function () {
    'use strict';
    var root = document.getElementById('nim-fe');
    if (!root) { return; }
    var api = root.dataset.api, path = root.dataset.path;
    var text = root.querySelector('[data-fe-text]'), gutter = root.querySelector('[data-fe-gutter]');
    var info = document.querySelector('[data-fe-info]'), stateEl = root.querySelector('[data-fe-state]'), pos = root.querySelector('[data-fe-pos]');
    var saveBtn = document.querySelector('[data-fe-save]'), saveClose = document.querySelector('[data-fe-saveclose]');
    var mtime = null, saved = '', lines = 0;

    function call(op, params) {
        return NimVor.fetchJson(api, { method: 'POST', body: JSON.stringify(Object.assign({ op: op, path: path }, params || {})) }).then(function (r) { return r.data; });
    }
    function fail(err) { var d = err.details || {}; NimVor.toast((d.message || err.message) + (d.details ? ' — ' + d.details : ''), 'danger'); }
    function dirty() { return text.value !== saved; }
    function updateState() {
        var d = dirty();
        stateEl.textContent = d ? 'Unsaved changes' : 'Saved';
        stateEl.className = 'ms-3 ' + (d ? 'text-warning-emphasis' : 'text-success');
        saveBtn.disabled = saveClose.disabled = !d || text.disabled;
    }
    function updateGutter() {
        var n = text.value.split('\n').length;
        if (n !== lines) {
            lines = n;
            var out = [];
            for (var i = 1; i <= n; i++) { out.push(i); }
            gutter.textContent = out.join('\n');
        }
        gutter.scrollTop = text.scrollTop;
    }
    function updatePos() {
        var before = text.value.slice(0, text.selectionStart).split('\n');
        pos.textContent = 'Ln ' + before.length + ', Col ' + (before[before.length - 1].length + 1);
    }
    function fmt(n) { var u = ['B', 'KB', 'MB']; var i = 0; while (n >= 1024 && i < 2) { n /= 1024; i++; } return (i ? n.toFixed(1) : n) + ' ' + u[i]; }

    function load() {
        text.disabled = true;
        return call('read').then(function (f) {
            if (f.binary || f.too_big) {
                text.value = f.too_big ? 'This file is larger than 2 MB and cannot be edited here. Download it instead.' : 'This is a binary file and cannot be edited as text. Download it instead.';
                var a = document.createElement('a'); a.href = root.dataset.download; a.className = 'btn btn-sm btn-outline-secondary ms-2'; a.textContent = 'Download';
                stateEl.replaceChildren(a);
                return;
            }
            text.value = saved = f.content;
            mtime = f.mtime;
            text.disabled = false;
            info.textContent = fmt(f.size) + ' · modified ' + new Date(f.mtime).toLocaleString();
            lines = 0; updateGutter(); updateState(); text.focus();
        }).catch(function (err) { text.value = ''; fail(err); });
    }
    function save(close) {
        if (text.disabled) { return; }
        var content = text.value;
        saveBtn.disabled = saveClose.disabled = true;
        call('write', { content: content, expected_mtime: mtime }).then(function (r) {
            mtime = r.mtime; saved = content;
            info.textContent = fmt(r.size) + ' · saved ' + new Date(r.mtime).toLocaleTimeString();
            updateState();
            NimVor.toast('Saved.', 'success');
            if (close) { window.location.href = root.dataset.back; }
        }).catch(function (err) { fail(err); updateState(); });
    }

    text.addEventListener('input', function () { updateGutter(); updateState(); updatePos(); });
    text.addEventListener('scroll', function () { gutter.scrollTop = text.scrollTop; });
    ['click', 'keyup', 'select'].forEach(function (t) { text.addEventListener(t, updatePos); });
    text.addEventListener('keydown', function (e) {
        if (e.key === 'Tab' && !e.ctrlKey && !e.altKey && !e.metaKey) {
            e.preventDefault();
            var s = text.selectionStart, en = text.selectionEnd;
            text.setRangeText('\t', s, en, 'end');
            text.dispatchEvent(new Event('input'));
        }
    });
    document.addEventListener('keydown', function (e) {
        if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') { e.preventDefault(); save(false); }
    });
    saveBtn.addEventListener('click', function () { save(false); });
    saveClose.addEventListener('click', function () { save(true); });
    document.querySelector('[data-fe-reload]').addEventListener('click', function () {
        if (!dirty() || window.confirm('Discard your changes and reload the file from the server?')) { load(); }
    });
    root.querySelector('[data-fe-wrap]').addEventListener('change', function (e) {
        text.wrap = e.target.checked ? 'soft' : 'off';
        gutter.classList.toggle('d-none', e.target.checked);
    });
    window.addEventListener('beforeunload', function (e) { if (dirty()) { e.preventDefault(); e.returnValue = ''; } });
    load();
})();
