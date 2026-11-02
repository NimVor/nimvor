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
            lines = 0; updateGutter(); updateState(); resetHistory(); text.focus();
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

    // ---- Undo and redo: our own history, because replacing the text from script (loading, indenting) empties the
    // browser's, and so Ctrl+Z did nothing after those. Typing within half a second is one step.
    var hist = [], hi = -1, lastAt = 0, restoring = false;
    function snap() { return { v: text.value, s: text.selectionStart, e: text.selectionEnd }; }
    function resetHistory() { hist = [snap()]; hi = 0; lastAt = 0; }
    function record(merge) {
        if (restoring) { return; }
        var cur = snap();
        if (hist[hi] && hist[hi].v === cur.v) { hist[hi].s = cur.s; hist[hi].e = cur.e; return; }
        var now = Date.now();
        hist.length = hi + 1;
        if (merge && hi > 0 && now - lastAt < 500) { hist[hi] = cur; } else { hist.push(cur); hi++; if (hist.length > 500) { hist.shift(); hi--; } }
        lastAt = now;
    }
    function jump(to) {
        if (to < 0 || to >= hist.length) { return; }
        hi = to; restoring = true;
        var h = hist[hi];
        text.value = h.v; text.setSelectionRange(h.s, h.e);
        restoring = false;
        updateGutter(); updateState(); updatePos();
    }
    // An edit made from script, as one undoable step.
    function replace(from, to, str, selFrom, selTo) {
        text.value = text.value.slice(0, from) + str + text.value.slice(to);
        text.setSelectionRange(selFrom, selTo);
        record(false); updateGutter(); updateState(); updatePos();
    }
    // Tab indents (every selected line when several are selected), Shift+Tab removes one level.
    function indent(back) {
        var v = text.value, s = text.selectionStart, e = text.selectionEnd;
        var multi = v.slice(s, e).indexOf('\n') !== -1;
        if (!back && !multi) { replace(s, e, '\t', s + 1, s + 1); return; }
        var from = v.lastIndexOf('\n', s - 1) + 1;
        var to = e;
        if (e > s && v.charAt(e - 1) === '\n') { to = e - 1; }
        var nl = v.indexOf('\n', to);
        to = nl === -1 ? v.length : nl;
        var lines = v.slice(from, to).split('\n'), first = 0, total = 0;
        var out = lines.map(function (l, i) {
            var r = l;
            if (back) {
                var m = /^(\t| {1,4})/.exec(l);
                r = m ? l.slice(m[0].length) : l;
            } else if (l !== '') {
                r = '\t' + l;
            }
            if (i === 0) { first = r.length - l.length; }
            total += r.length - l.length;
            return r;
        });
        replace(from, to, out.join('\n'), Math.max(from, s + first), Math.max(from, e + total));
    }

    text.addEventListener('input', function () { record(true); updateGutter(); updateState(); updatePos(); });
    // Undo and redo from the browser's menu go through our history too.
    text.addEventListener('beforeinput', function (e) {
        if (e.inputType === 'historyUndo') { e.preventDefault(); jump(hi - 1); }
        if (e.inputType === 'historyRedo') { e.preventDefault(); jump(hi + 1); }
    });
    text.addEventListener('scroll', function () { gutter.scrollTop = text.scrollTop; });
    ['click', 'keyup', 'select'].forEach(function (t) { text.addEventListener(t, updatePos); });
    text.addEventListener('keydown', function (e) {
        var mod = e.ctrlKey || e.metaKey;
        var k = (e.key || '').toLowerCase();
        if (e.key === 'Tab' && !mod && !e.altKey) {
            e.preventDefault();
            indent(e.shiftKey);
        } else if (mod && !e.altKey && (k === 'z' || e.code === 'KeyZ')) {
            e.preventDefault();
            jump(e.shiftKey ? hi + 1 : hi - 1);
        } else if (mod && !e.altKey && !e.shiftKey && (k === 'y' || e.code === 'KeyY')) {
            e.preventDefault();
            jump(hi + 1);
        }
    });
    // Ctrl+S (Cmd+S) saves the file instead of the browser's "Save page"; caught before anything else sees it.
    window.addEventListener('keydown', function (e) {
        if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && ((e.key || '').toLowerCase() === 's' || e.code === 'KeyS' || e.keyCode === 83)) {
            e.preventDefault(); e.stopPropagation(); save(false);
        }
    }, true);
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
