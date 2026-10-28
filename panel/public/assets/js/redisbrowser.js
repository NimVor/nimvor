// NimVor Redis key browser. Keys and values travel base64-encoded so binary
// data round-trips exactly; everything is rendered as text, never HTML.
(function () {
    'use strict';
    var root = document.getElementById('nim-rb');
    if (!root) { return; }
    var api = root.dataset.api, fixedPrefix = root.dataset.fixed === '1';
    var $ = function (s) { return root.querySelector(s); };
    var list = $('[data-rb-keys]'), detail = $('[data-rb-detail]'), more = $('[data-rb-cmd="more"]'), count = $('[data-rb-count]');
    var state = { db: 0, cursor: '0', pattern: '*', prefix: root.dataset.prefix || '', current: null, keys: [] };

    var enc = new TextEncoder();
    function toB64(str) { var bytes = enc.encode(str), s = ''; bytes.forEach(function (b) { s += String.fromCharCode(b); }); return btoa(s); }
    function el(tag, cls, text) { var e = document.createElement(tag); if (cls) { e.className = cls; } if (text !== undefined && text !== null) { e.textContent = text; } return e; }
    function show(bin) { return bin.binary ? '[binary] ' + bin.b64 : bin.text; }
    function ttlText(t) { if (t < 0) { return 'no expiry'; } if (t < 120) { return t + ' s'; } if (t < 7200) { return Math.round(t / 60) + ' min'; } if (t < 172800) { return Math.round(t / 3600) + ' h'; } return Math.round(t / 86400) + ' days'; }
    var TYPE_CLASS = { string: 'text-bg-primary', hash: 'text-bg-success', list: 'text-bg-warning', set: 'text-bg-info', zset: 'text-bg-danger', stream: 'text-bg-secondary' };

    function call(action, params) {
        var body = Object.assign({ action: action, db: state.db }, params || {});
        if (!fixedPrefix) { body.prefix = state.prefix; }
        return NimVor.fetchJson(api, { method: 'POST', body: JSON.stringify(body) }).then(function (r) { return r.data; });
    }
    function fail(err) { var d = err.details || {}; NimVor.toast((d.message || err.message) + (d.details ? ' — ' + d.details : ''), 'danger'); }

    // ---- dialog -------------------------------------------------------
    var dlg = document.getElementById('rb-dialog');
    function dialog(title, build, ok, danger) {
        return new Promise(function (resolve) {
            dlg.querySelector('.modal-title').textContent = title;
            var body = dlg.querySelector('[data-rb-dialog-body]'); body.replaceChildren();
            var collect = build(body);
            var btn = dlg.querySelector('[data-rb-dialog-ok]'); btn.textContent = ok || 'Save'; btn.className = 'btn ' + (danger ? 'btn-danger' : 'btn-primary');
            var m = bootstrap.Modal.getOrCreateInstance(dlg), done = false;
            dlg.querySelector('form').onsubmit = function (e) { e.preventDefault(); var v = collect(); if (v === false) { return; } done = true; m.hide(); resolve(v); };
            dlg.addEventListener('hidden.bs.modal', function () { if (!done) { resolve(null); } }, { once: true });
            dlg.addEventListener('shown.bs.modal', function () { var f = body.querySelector('input, textarea, select'); if (f) { f.focus(); } }, { once: true });
            m.show();
        });
    }
    function input(body, label, value, opts) {
        opts = opts || {};
        var id = 'rb-' + Math.random().toString(36).slice(2), w = el('div', 'mb-3');
        var l = el('label', 'form-label small', label); l.htmlFor = id; w.appendChild(l);
        var i = el(opts.area ? 'textarea' : (opts.select ? 'select' : 'input'), (opts.select ? 'form-select' : 'form-control') + ' font-monospace');
        i.id = id;
        if (opts.select) { opts.select.forEach(function (o) { var op = el('option', '', o); op.value = o; i.appendChild(op); }); }
        if (opts.area) { i.rows = 8; i.spellcheck = false; }
        if (opts.type) { i.type = opts.type; }
        i.value = value === undefined ? '' : value;
        w.appendChild(i);
        if (opts.help) { w.appendChild(el('div', 'form-text', opts.help)); }
        body.appendChild(w);
        return i;
    }

    // ---- key list -----------------------------------------------------
    function renderKey(k) {
        var li = el('li', 'list-group-item list-group-item-action nim-rb-key');
        li.setAttribute('role', 'option'); li.tabIndex = 0; li.dataset.key = k.key.b64;
        if (state.current && state.current === k.key.b64) { li.classList.add('active'); }
        li.appendChild(el('span', 'badge ' + (TYPE_CLASS[k.type] || 'text-bg-light') + ' me-2', k.type));
        li.appendChild(el('span', 'font-monospace text-truncate flex-grow-1', show(k.key)));
        if (k.ttl >= 0) { li.appendChild(el('span', 'small text-body-secondary ms-2', ttlText(k.ttl))); }
        return li;
    }
    function scan(reset) {
        if (reset) { state.cursor = '0'; state.keys = []; list.replaceChildren(); }
        return call('keys', { pattern: state.pattern, cursor: state.cursor }).then(function (r) {
            state.cursor = r.cursor;
            r.keys.sort(function (a, b) { return show(a.key) < show(b.key) ? -1 : 1; }).forEach(function (k) { state.keys.push(k); list.appendChild(renderKey(k)); });
            more.classList.toggle('d-none', r.cursor === '0');
            count.textContent = state.keys.length + ' key(s) shown · ' + r.db_size + ' in database ' + state.db;
            if (!state.keys.length) { list.appendChild(el('li', 'list-group-item text-body-secondary small', 'No keys match.')); }
        }).catch(fail);
    }

    // ---- detail -------------------------------------------------------
    function edit(params) { return call('edit', Object.assign({ key: state.current }, params)).then(function () { open(state.current); }).catch(fail); }
    function btn(label, icon, cls, fn) { var b = el('button', 'btn btn-sm ' + cls); b.type = 'button'; if (icon) { var i = el('i', 'bi ' + icon + (label ? ' me-1' : '')); b.appendChild(i); } if (label) { b.appendChild(document.createTextNode(label)); } b.addEventListener('click', fn); return b; }
    function table(headers, rowsData) {
        var wrap = el('div', 'table-responsive'), t = el('table', 'table table-sm table-hover align-middle mb-0 nim-rb-table');
        var thead = el('thead'), tr = el('tr'); headers.forEach(function (h) { tr.appendChild(el('th', 'small', h)); }); thead.appendChild(tr); t.appendChild(thead);
        var tb = el('tbody'); rowsData.forEach(function (r) { tb.appendChild(r); }); t.appendChild(tb); wrap.appendChild(t); return wrap;
    }
    function cellText(bin) { var td = el('td', 'font-monospace small nim-rb-cell'); td.textContent = show(bin); if (bin.binary) { td.classList.add('text-body-secondary'); } return td; }

    function open(keyB64) {
        state.current = keyB64;
        list.querySelectorAll('.nim-rb-key').forEach(function (li) { li.classList.toggle('active', li.dataset.key === keyB64); });
        detail.replaceChildren(el('div', 'p-4 text-body-secondary', 'Loading…'));
        return call('get', { key: keyB64 }).then(renderDetail).catch(function (err) { detail.replaceChildren(el('div', 'p-4 text-danger', (err.details && err.details.message) || err.message)); });
    }
    function renderDetail(kv) {
        detail.replaceChildren();
        var head = el('div', 'nim-rb-head');
        head.appendChild(el('span', 'badge ' + (TYPE_CLASS[kv.type] || 'text-bg-light'), kv.type));
        head.appendChild(el('h2', 'h6 mb-0 font-monospace text-break flex-grow-1', show(kv.key)));
        head.appendChild(el('span', 'small text-body-secondary', (kv.type === 'string' ? kv.length + ' bytes' : kv.length + ' elements') + ' · ' + ttlText(kv.ttl)));
        var tools = el('div', 'btn-group btn-group-sm');
        tools.appendChild(btn('TTL', 'bi-hourglass-split', 'btn-outline-secondary', function () {
            dialog('Expiry', function (b) { var i = input(b, 'Seconds until the key expires (0 = never)', kv.ttl > 0 ? kv.ttl : 0, { type: 'number', help: 'Up to 10 years.' }); return function () { var v = parseInt(i.value, 10); return v >= 0 ? v : false; }; })
                .then(function (v) { if (v !== null) { edit({ op: 'expire', ttl: v }); } });
        }));
        tools.appendChild(btn('Rename', 'bi-input-cursor-text', 'btn-outline-secondary', function () {
            dialog('Rename key', function (b) { var i = input(b, 'New name', kv.key.binary ? '' : kv.key.text, { help: fixedPrefix ? 'Must keep the prefix ' + root.dataset.prefix : '' }); return function () { return i.value ? i.value : false; }; })
                .then(function (v) { if (v) { call('edit', { op: 'rename', key: state.current, new_key: toB64(v) }).then(function () { state.current = toB64(v); scan(true); open(state.current); }).catch(fail); } });
        }));
        tools.appendChild(btn('Delete', 'bi-trash', 'btn-outline-danger', function () {
            dialog('Delete key', function (b) { b.appendChild(el('p', '', 'Delete this key and its value?')); b.appendChild(el('code', 'text-break', show(kv.key))); return function () { return true; }; }, 'Delete', true)
                .then(function (ok) { if (ok) { call('delete', { keys: [state.current] }).then(function () { NimVor.toast('Deleted.', 'success'); state.current = null; detail.replaceChildren(el('div', 'p-4 text-body-secondary', 'Key deleted.')); scan(true); }).catch(fail); } });
        }));
        head.appendChild(tools);
        detail.appendChild(head);
        if (kv.truncated) { detail.appendChild(el('div', 'alert alert-info small m-2 py-1', kv.type === 'string' ? 'Only the first 1 MiB is shown; editing is disabled.' : 'Only the first elements are shown.')); }
        var body = el('div', 'nim-rb-content');
        detail.appendChild(body);

        if (kv.type === 'string') {
            var ta = el('textarea', 'form-control font-monospace nim-rb-text'); ta.spellcheck = false; ta.value = show(kv.value);
            ta.readOnly = kv.value.binary || kv.truncated; ta.setAttribute('aria-label', 'Value');
            body.appendChild(ta);
            if (!ta.readOnly) {
                var bar = el('div', 'd-flex gap-2 mt-2');
                bar.appendChild(btn('Save value', 'bi-save', 'btn-primary', function () { edit({ op: 'set', value: toB64(ta.value) }).then(function () { NimVor.toast('Saved.', 'success'); }); }));
                var pretty = btn('Format JSON', 'bi-braces', 'btn-outline-secondary', function () { try { ta.value = JSON.stringify(JSON.parse(ta.value), null, 2); } catch (e) { NimVor.toast('Not valid JSON.', 'secondary'); } });
                bar.appendChild(pretty);
                body.appendChild(bar);
            } else if (kv.value.binary) {
                body.appendChild(el('div', 'form-text', 'Binary value shown as base64; it cannot be edited here.'));
            }
            return;
        }
        var rowsData = [], addBtn = null;
        (kv.elements || []).forEach(function (e) {
            var tr = el('tr'), act = el('td', 'text-end text-nowrap');
            if (kv.type === 'hash') {
                tr.appendChild(cellText(e.field)); tr.appendChild(cellText(e.value));
                act.appendChild(btn('', 'bi-pencil', 'btn-link', function () {
                    dialog('Edit field', function (b) { var v = input(b, 'Value of ' + show(e.field), show(e.value), { area: true }); return function () { return v.value; }; })
                        .then(function (v) { if (v !== null) { edit({ op: 'hset', field: e.field.b64, value: toB64(v) }); } });
                }));
                act.appendChild(btn('', 'bi-x-lg', 'btn-link text-danger', function () { edit({ op: 'hdel', field: e.field.b64 }); }));
            } else if (kv.type === 'list') {
                tr.appendChild(el('td', 'small text-body-secondary', String(e.index))); tr.appendChild(cellText(e.value));
                act.appendChild(btn('', 'bi-pencil', 'btn-link', function () {
                    dialog('Edit element ' + e.index, function (b) { var v = input(b, 'Value', show(e.value), { area: true }); return function () { return v.value; }; })
                        .then(function (v) { if (v !== null) { edit({ op: 'lset', index: e.index, value: toB64(v) }); } });
                }));
                act.appendChild(btn('', 'bi-x-lg', 'btn-link text-danger', function () { edit({ op: 'lrem', value: e.value.b64 }); }));
            } else if (kv.type === 'set') {
                tr.appendChild(cellText(e.value));
                act.appendChild(btn('', 'bi-x-lg', 'btn-link text-danger', function () { edit({ op: 'srem', value: e.value.b64 }); }));
            } else if (kv.type === 'zset') {
                tr.appendChild(el('td', 'small font-monospace', e.score)); tr.appendChild(cellText(e.value));
                act.appendChild(btn('', 'bi-x-lg', 'btn-link text-danger', function () { edit({ op: 'zrem', value: e.value.b64 }); }));
            } else if (kv.type === 'stream') {
                tr.appendChild(cellText(e.field)); tr.appendChild(cellText(e.value));
            }
            if (e.value.binary || (e.field && e.field.binary)) { act.replaceChildren(); }
            tr.appendChild(act); rowsData.push(tr);
        });
        var headers = { hash: ['Field', 'Value', ''], list: ['#', 'Value', ''], set: ['Member', ''], zset: ['Score', 'Member', ''], stream: ['ID', 'Fields', ''] }[kv.type] || ['Value'];
        if (!headers || !kv.elements) { body.appendChild(el('p', 'text-body-secondary', 'This key type (' + kv.type + ') cannot be shown.')); return; }
        if (kv.type === 'hash') { addBtn = btn('Add field', 'bi-plus-lg', 'btn-outline-primary', function () { dialog('Add field', function (b) { var f = input(b, 'Field', ''); var v = input(b, 'Value', '', { area: true }); return function () { return f.value ? { f: f.value, v: v.value } : false; }; }).then(function (x) { if (x) { edit({ op: 'hset', field: toB64(x.f), value: toB64(x.v) }); } }); }); }
        if (kv.type === 'list') { addBtn = btn('Push', 'bi-plus-lg', 'btn-outline-primary', function () { dialog('Push element', function (b) { var side = input(b, 'Where', 'end (right)', { select: ['end (right)', 'start (left)'] }); var v = input(b, 'Value', '', { area: true }); return function () { return { side: side.value, v: v.value }; }; }).then(function (x) { if (x) { edit({ op: x.side.indexOf('left') >= 0 ? 'lpush' : 'rpush', value: toB64(x.v) }); } }); }); }
        if (kv.type === 'set') { addBtn = btn('Add member', 'bi-plus-lg', 'btn-outline-primary', function () { dialog('Add member', function (b) { var v = input(b, 'Member', ''); return function () { return v.value; }; }).then(function (v) { if (v !== null) { edit({ op: 'sadd', value: toB64(v) }); } }); }); }
        if (kv.type === 'zset') { addBtn = btn('Add member', 'bi-plus-lg', 'btn-outline-primary', function () { dialog('Add or update member', function (b) { var s = input(b, 'Score', '0', { type: 'number' }); var v = input(b, 'Member', ''); return function () { return s.value !== '' ? { s: s.value, v: v.value } : false; }; }).then(function (x) { if (x) { edit({ op: 'zadd', score: String(x.s), value: toB64(x.v) }); } }); }); }
        if (addBtn) { var bar2 = el('div', 'mb-2'); bar2.appendChild(addBtn); body.appendChild(bar2); }
        body.appendChild(rowsData.length ? table(headers, rowsData) : el('p', 'text-body-secondary', 'Empty.'));
    }

    // ---- events -------------------------------------------------------
    list.addEventListener('click', function (e) { var li = e.target.closest('[data-key]'); if (li) { open(li.dataset.key); } });
    list.addEventListener('keydown', function (e) {
        var li = e.target.closest('[data-key]'); if (!li) { return; }
        if (e.key === 'Enter') { open(li.dataset.key); }
        if (e.key === 'ArrowDown' && li.nextElementSibling) { e.preventDefault(); li.nextElementSibling.focus(); }
        if (e.key === 'ArrowUp' && li.previousElementSibling) { e.preventDefault(); li.previousElementSibling.focus(); }
    });
    $('[data-rb-search]').addEventListener('submit', function (e) {
        e.preventDefault();
        state.pattern = $('[data-rb-pattern]').value.trim() || '*';
        var p = $('[data-rb-prefix]'); if (p) { state.prefix = p.value; }
        scan(true);
    });
    $('[data-rb-db]').addEventListener('change', function (e) { state.db = parseInt(e.target.value, 10); state.current = null; detail.replaceChildren(el('div', 'p-4 text-body-secondary', 'Select a key to see its value.')); scan(true); });
    more.addEventListener('click', function () { scan(false); });
    $('[data-rb-cmd="newkey"]').addEventListener('click', function () {
        var pre = fixedPrefix ? root.dataset.prefix : state.prefix;
        dialog('New key', function (b) {
            var name = input(b, 'Name', pre, { help: fixedPrefix ? 'Must start with ' + pre : '' });
            var type = input(b, 'Type', 'string', { select: ['string', 'hash', 'list', 'set', 'zset'] });
            var field = input(b, 'Field (hash) or score (sorted set)', '');
            var value = input(b, 'Value', '', { area: true });
            var ttl = input(b, 'Expire after seconds (0 = never)', '0', { type: 'number' });
            return function () { return name.value ? { name: name.value, type: type.value, field: field.value, value: value.value, ttl: parseInt(ttl.value, 10) || 0 } : false; };
        }, 'Create').then(function (x) {
            if (!x) { return; }
            var p = { op: 'create', key: toB64(x.name), type: x.type, value: toB64(x.value), ttl: x.ttl };
            if (x.type === 'hash') { p.field = toB64(x.field); }
            if (x.type === 'zset') { p.score = x.field || '0'; }
            call('edit', p).then(function () { NimVor.toast('Key created.', 'success'); scan(true).then(function () { open(p.key); }); }).catch(fail);
        });
    });
    scan(true);
})();
