// WhaCP database browser. Every request runs in the agent as the
// database's own browser account (privileges on this database only).
// Values are rendered as text, never HTML. A cell is null, a string, or
// {hex|text, size, truncated} for binary data and shortened text.
(function () {
    'use strict';
    var root = document.getElementById('wcp-db');
    if (!root) { return; }
    var api = root.dataset.api;
    var $ = function (s) { return root.querySelector(s); };
    var pane = $('[data-db-pane]'), tableList = $('[data-db-tables]'), current = $('[data-db-current]');
    var state = { tables: [], table: null, tab: 'sql', structure: {}, page: { offset: 0, limit: 50, order: '', desc: false, filter: null, search: '' }, sql: '', rows: null, selected: new Set() };

    function el(tag, cls, text) { var e = document.createElement(tag); if (cls) { e.className = cls; } if (text !== undefined && text !== null) { e.textContent = text; } return e; }
    function btn(label, icon, cls, fn) { var b = el('button', 'btn btn-sm ' + cls); b.type = 'button'; if (icon) { b.appendChild(el('i', 'bi ' + icon + (label ? ' me-1' : ''))); } if (label) { b.appendChild(document.createTextNode(label)); } if (fn) { b.addEventListener('click', fn); } return b; }
    function call(op, params) {
        return WhaCP.fetchJson(api, { method: 'POST', body: JSON.stringify(Object.assign({ op: op }, params || {})) }).then(function (r) { return r.data; });
    }
    function fail(err) { var d = err.details || {}; WhaCP.toast((d.message || err.message) + (d.details ? ' — ' + d.details : ''), 'danger'); }
    function ident(name) { return '`' + String(name).replace(/`/g, '``') + '`'; }
    function fmtBytes(n) { if (n < 1024) { return n + ' B'; } var u = ['KiB', 'MiB', 'GiB', 'TiB'], i = -1; do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1); return n.toFixed(n < 10 ? 1 : 0) + ' ' + u[i]; }
    function loading(target) { target.replaceChildren(el('div', 'p-4 text-body-secondary', 'Loading…')); }

    // Cell rendering: NULL, binary as 0x…, shortened text with an ellipsis.
    function cellNode(v) {
        if (v === null || v === undefined) { return el('span', 'wcp-db-null', 'NULL'); }
        if (typeof v === 'string') { return document.createTextNode(v); }
        if (v.hex !== undefined) { return el('span', 'wcp-db-bin', '0x' + v.hex + (v.truncated ? '… (' + fmtBytes(v.size) + ')' : '')); }
        var s = el('span', '', v.text); s.appendChild(el('span', 'wcp-db-more', '… (' + fmtBytes(v.size) + ')')); return s;
    }
    function cellText(v) {
        if (v === null || v === undefined) { return ''; }
        if (typeof v === 'string') { return v; }
        return v.hex !== undefined ? '0x' + v.hex : v.text;
    }

    // ---- dialog ---------------------------------------------------------
    var dlg = document.getElementById('db-dialog');
    function confirmDialog(title, message, ok, word) {
        return new Promise(function (resolve) {
            dlg.querySelector('.modal-title').textContent = title;
            var body = dlg.querySelector('[data-db-dialog-body]'); body.replaceChildren(el('p', 'mb-2', message));
            var input = null, okBtn = dlg.querySelector('[data-db-dialog-ok]');
            okBtn.textContent = ok; okBtn.disabled = false;
            if (word) {
                body.appendChild(el('label', 'form-label small', 'Type ' + word + ' to confirm'));
                input = el('input', 'form-control font-monospace'); input.autocomplete = 'off';
                body.appendChild(input); okBtn.disabled = true;
                input.addEventListener('input', function () { okBtn.disabled = input.value !== word; });
            }
            var modal = bootstrap.Modal.getOrCreateInstance(dlg), done = false;
            var form = dlg.querySelector('[data-db-dialog-form]');
            form.onsubmit = function (e) { e.preventDefault(); if (okBtn.disabled) { return; } done = true; modal.hide(); resolve(true); };
            dlg.addEventListener('hidden.bs.modal', function h() { dlg.removeEventListener('hidden.bs.modal', h); if (!done) { resolve(false); } });
            dlg.addEventListener('shown.bs.modal', function s() { dlg.removeEventListener('shown.bs.modal', s); (input || okBtn).focus(); });
            modal.show();
        });
    }

    // ---- tables ---------------------------------------------------------
    function loadTables() {
        return call('tables').then(function (d) {
            state.tables = d.tables || [];
            state.info = d;
            var srv = document.querySelector('[data-db-server]'); if (srv) { srv.textContent = d.server || ''; }
            renderTables();
            if (state.table && !state.tables.some(function (t) { return t.name === state.table; })) { selectTable(null); }
        }).catch(fail);
    }
    function renderTables() {
        var q = ($('[data-db-table-filter]').value || '').toLowerCase();
        tableList.replaceChildren();
        var shown = 0;
        state.tables.forEach(function (t) {
            if (q && t.name.toLowerCase().indexOf(q) < 0) { return; }
            shown++;
            var li = el('li', 'list-group-item list-group-item-action wcp-db-table' + (t.name === state.table ? ' active' : ''));
            li.tabIndex = 0; li.setAttribute('role', 'button'); li.dataset.table = t.name;
            li.appendChild(el('i', 'bi ' + (t.view ? 'bi-eye' : 'bi-table') + ' me-2 opacity-75'));
            li.appendChild(el('span', 'text-truncate flex-grow-1', t.name));
            li.appendChild(el('span', 'small text-body-secondary ms-2', t.view ? 'view' : '~' + Number(t.rows).toLocaleString()));
            li.title = t.view ? 'View' : (t.engine + ' · ' + fmtBytes(t.bytes) + (t.comment ? ' · ' + t.comment : ''));
            tableList.appendChild(li);
        });
        var total = state.tables.reduce(function (n, t) { return n + (t.bytes || 0); }, 0);
        $('[data-db-tables-count]').textContent = (state.tables.length === 0 ? 'No tables yet.' : shown + ' of ' + state.tables.length + ' table(s) · ' + fmtBytes(total)) + (state.info && state.info.charset ? ' · ' + state.info.charset + ' / ' + state.info.collation : '');
    }
    tableList.addEventListener('click', function (e) { var li = e.target.closest('[data-table]'); if (li) { selectTable(li.dataset.table); } });
    tableList.addEventListener('keydown', function (e) { var li = e.target.closest('[data-table]'); if (li && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); selectTable(li.dataset.table); } });
    $('[data-db-table-filter]').addEventListener('input', renderTables);
    $('[data-db-new-table]').addEventListener('click', newTable);

    function selectTable(name) {
        state.table = name;
        state.page = { offset: 0, limit: state.page.limit, order: '', desc: false, filter: null, search: '' };
        state.selected = new Set();
        current.textContent = name || '';
        root.querySelectorAll('[data-db-tab]').forEach(function (b) { if (b.dataset.dbTab !== 'sql') { b.disabled = !name; } });
        renderTables();
        if (name) { state.sql = 'SELECT * FROM ' + ident(name) + ' LIMIT 50'; switchTab('browse'); } else { switchTab('sql'); }
    }

    function structure(name) {
        if (state.structure[name]) { return Promise.resolve(state.structure[name]); }
        return call('structure', { table: name }).then(function (s) { state.structure[name] = s; return s; });
    }

    // ---- tabs -----------------------------------------------------------
    function switchTab(tab) {
        state.tab = tab;
        root.querySelectorAll('[data-db-tab]').forEach(function (b) { var on = b.dataset.dbTab === tab; b.classList.toggle('btn-primary', on); b.classList.toggle('btn-outline-secondary', !on); b.setAttribute('aria-selected', on ? 'true' : 'false'); });
        if (tab === 'browse') { browse(); } else if (tab === 'structure') { showStructure(); } else if (tab === 'insert') { editRow(null); } else { showSql(); }
    }
    root.querySelectorAll('[data-db-tab]').forEach(function (b) { b.addEventListener('click', function () { if (!b.disabled) { switchTab(b.dataset.dbTab); } }); });

    // ---- browse ---------------------------------------------------------
    function browse() {
        var name = state.table; loading(pane);
        var p = state.page;
        var params = { table: name, offset: p.offset, limit: p.limit };
        if (p.order) { params.order = p.order; params.desc = p.desc; }
        if (p.filter) { params.filter = p.filter; }
        if (p.search) { params.search = p.search; }
        Promise.all([call('rows', params), structure(name)]).then(function (res) {
            if (state.table !== name || state.tab !== 'browse') { return; }
            state.rows = res[0]; renderGrid(res[0], res[1]);
        }).catch(function (e) { pane.replaceChildren(el('div', 'alert alert-danger m-3', (e.details && e.details.details) || e.message)); });
    }

    function renderGrid(d, st) {
        var p = state.page, cols = d.columns || [], canEdit = (d.key || []).length > 0;
        state.selected = new Set();
        pane.replaceChildren();
        // Toolbar: filter, pager, actions.
        var bar = el('div', 'wcp-db-bar');
        var f = el('form', 'input-group input-group-sm wcp-db-filter');
        var fc = el('select', 'form-select'); fc.setAttribute('aria-label', 'Filter column');
        cols.forEach(function (c) { var o = el('option', '', c.name); o.value = c.name; fc.appendChild(o); });
        var fo = el('select', 'form-select'); fo.setAttribute('aria-label', 'Operator');
        ['=', '!=', '<', '>', '<=', '>=', 'like', 'not like', 'null', 'not null'].forEach(function (o) { var opt = el('option', '', o === 'null' ? 'IS NULL' : o === 'not null' ? 'IS NOT NULL' : o.toUpperCase()); opt.value = o; fo.appendChild(opt); });
        var fv = el('input', 'form-control'); fv.placeholder = 'value (% wildcard with LIKE)'; fv.setAttribute('aria-label', 'Filter value');
        if (p.filter) { fc.value = p.filter.column; fo.value = p.filter.op; fv.value = p.filter.value; }
        fo.addEventListener('change', function () { fv.disabled = fo.value === 'null' || fo.value === 'not null'; }); fv.disabled = fo.value === 'null' || fo.value === 'not null';
        var fb = el('button', 'btn btn-outline-secondary', 'Filter'); fb.type = 'submit';
        f.append(fc, fo, fv, fb);
        if (p.filter) { f.appendChild(btn('', 'bi-x-lg', 'btn-outline-secondary', function () { p.filter = null; p.offset = 0; browse(); })); }
        f.addEventListener('submit', function (e) { e.preventDefault(); p.filter = { column: fc.value, op: fo.value, value: fv.value }; p.offset = 0; browse(); });
        bar.appendChild(f);
        var sf = el('form', 'wcp-search'), si = el('input', 'form-control form-control-sm'); si.type = 'search'; si.placeholder = 'Search all columns'; si.setAttribute('aria-label', 'Search all columns'); si.value = p.search || '';
        sf.append(si, el('i', 'bi bi-search'));
        sf.addEventListener('submit', function (e) { e.preventDefault(); p.search = si.value.trim(); p.offset = 0; browse(); });
        si.addEventListener('search', function () { if (si.value === '' && p.search) { p.search = ''; p.offset = 0; browse(); } });
        bar.appendChild(sf);

        var pager = el('div', 'd-flex align-items-center gap-1 ms-auto');
        var from = d.total === 0 ? 0 : p.offset + 1, to = Math.min(p.offset + (d.rows || []).length, d.total);
        var go = function (off) { return function () { p.offset = Math.max(0, off); browse(); }; };
        var first = btn('', 'bi-chevron-double-left', 'btn-outline-secondary', go(0)), prev = btn('', 'bi-chevron-left', 'btn-outline-secondary', go(p.offset - p.limit));
        var next = btn('', 'bi-chevron-right', 'btn-outline-secondary', go(p.offset + p.limit)), last = btn('', 'bi-chevron-double-right', 'btn-outline-secondary', go(Math.floor((d.total - 1) / p.limit) * p.limit));
        first.disabled = prev.disabled = p.offset === 0; next.disabled = last.disabled = to >= d.total;
        [first, prev, next, last].forEach(function (b, i) { b.setAttribute('aria-label', ['First page', 'Previous page', 'Next page', 'Last page'][i]); });
        var size = el('select', 'form-select form-select-sm w-auto'); size.setAttribute('aria-label', 'Rows per page');
        [25, 50, 100, 250, 500].forEach(function (n) { var o = el('option', '', n + ' / page'); o.value = n; if (n === p.limit) { o.selected = true; } size.appendChild(o); });
        size.addEventListener('change', function () { p.limit = parseInt(size.value, 10); p.offset = 0; browse(); });
        pager.append(el('span', 'small text-body-secondary me-1', from + '–' + to + ' of ' + Number(d.total).toLocaleString()), first, prev, next, last, size);
        bar.appendChild(pager);
        pane.appendChild(bar);

        var actions = el('div', 'wcp-db-bar py-1');
        var delBtn = btn('Delete selected', 'bi-trash', 'btn-outline-danger', function () { deleteRows(); });
        delBtn.disabled = true; delBtn.dataset.dbDelete = '1';
        actions.append(btn('Insert row', 'bi-plus-lg', 'btn-outline-primary', function () { switchTab('insert'); }), delBtn, btn('Refresh', 'bi-arrow-clockwise', 'btn-outline-secondary', browse),
            btn('Export CSV', 'bi-filetype-csv', 'btn-outline-secondary', function () { exportCsv(); }),
            btn('Import CSV', 'bi-upload', 'btn-outline-secondary', function () { importCsv(st); }));
        if (!canEdit) { actions.appendChild(el('span', 'small text-body-secondary ms-2', 'No primary or unique key: rows can be changed from the SQL tab only.')); }
        pane.appendChild(actions);

        var wrap = el('div', 'wcp-db-grid-wrap'), table = el('table', 'table table-sm table-hover wcp-db-grid mb-0');
        var thead = el('thead'), hr = el('tr');
        if (canEdit) {
            var th0 = el('th', 'wcp-db-check'), all = el('input', 'form-check-input'); all.type = 'checkbox'; all.setAttribute('aria-label', 'Select all rows');
            all.addEventListener('change', function () { table.querySelectorAll('tbody input[type=checkbox]').forEach(function (c) { c.checked = all.checked; c.dispatchEvent(new Event('change')); }); });
            th0.appendChild(all); hr.appendChild(th0); hr.appendChild(el('th', 'wcp-db-check'));
        }
        var types = {}; (st.columns || []).forEach(function (c) { types[c.name] = c; });
        cols.forEach(function (c) {
            var th = el('th', c.numeric ? 'text-end' : ''), b = el('button', 'btn btn-link btn-sm p-0 text-reset fw-semibold text-decoration-none', c.name);
            b.type = 'button'; b.title = (types[c.name] ? types[c.name].type : '') + ' — sort';
            if (p.order === c.name) { b.appendChild(el('i', 'bi ms-1 ' + (p.desc ? 'bi-caret-down-fill' : 'bi-caret-up-fill'))); th.setAttribute('aria-sort', p.desc ? 'descending' : 'ascending'); }
            b.addEventListener('click', function () { if (p.order === c.name) { if (p.desc) { p.order = ''; p.desc = false; } else { p.desc = true; } } else { p.order = c.name; p.desc = false; } p.offset = 0; browse(); });
            th.appendChild(b); hr.appendChild(th);
        });
        thead.appendChild(hr); table.appendChild(thead);
        var tb = el('tbody');
        (d.rows || []).forEach(function (row, i) {
            var tr = el('tr');
            if (canEdit) {
                var td0 = el('td', 'wcp-db-check'), cb = el('input', 'form-check-input'); cb.type = 'checkbox'; cb.setAttribute('aria-label', 'Select row ' + (p.offset + i + 1));
                cb.addEventListener('change', function () { if (cb.checked) { state.selected.add(i); } else { state.selected.delete(i); } tr.classList.toggle('table-active', cb.checked); pane.querySelector('[data-db-delete]').disabled = state.selected.size === 0; });
                td0.appendChild(cb); tr.appendChild(td0);
                var td1 = el('td', 'wcp-db-check'); var eb = btn('', 'bi-pencil', 'btn-link p-0', function () { editRow(d.keys[i]); }); eb.setAttribute('aria-label', 'Edit row ' + (p.offset + i + 1)); td1.appendChild(eb); tr.appendChild(td1);
                tr.addEventListener('dblclick', function () { editRow(d.keys[i]); });
            }
            row.forEach(function (v, j) { var td = el('td', cols[j].numeric ? 'text-end' : ''); td.appendChild(cellNode(v)); tr.appendChild(td); });
            tb.appendChild(tr);
        });
        table.appendChild(tb); wrap.appendChild(table);
        if ((d.rows || []).length === 0) { wrap.appendChild(el('div', 'p-4 text-body-secondary', p.filter ? 'No rows match the filter.' : 'The table is empty.')); }
        pane.appendChild(wrap);
    }

    function deleteRows() {
        var d = state.rows, keys = []; state.selected.forEach(function (i) { keys.push(d.keys[i]); });
        if (keys.length === 0) { return; }
        confirmDialog('Delete rows', 'Delete ' + keys.length + ' row(s) from ' + state.table + '? This cannot be undone.', 'Delete').then(function (ok) {
            if (!ok) { return; }
            call('delete', { table: state.table, keys: keys }).then(function (r) { WhaCP.toast(r.deleted + ' row(s) deleted.', 'success'); browse(); }).catch(fail);
        });
    }

    // ---- insert / edit ----------------------------------------------------
    function editRow(key) {
        var name = state.table; loading(pane);
        Promise.all([structure(name), key ? call('row', { table: name, key: key }) : Promise.resolve(null)]).then(function (res) {
            if (state.table !== name) { return; }
            renderEditor(res[0], res[1], key);
        }).catch(function (e) { pane.replaceChildren(el('div', 'alert alert-danger m-3', (e.details && e.details.details) || e.message)); });
    }

    function renderEditor(st, row, key) {
        var insert = !key;
        pane.replaceChildren();
        var form = el('form', 'wcp-db-editor');
        var head = el('div', 'd-flex align-items-center gap-2 mb-3');
        head.appendChild(el('h2', 'h6 mb-0', insert ? 'Insert a row into ' + state.table : 'Edit row of ' + state.table));
        if (!insert) { head.appendChild(el('code', 'small', Object.keys(key).map(function (k) { return k + ' = ' + cellText(key[k]); }).join(', '))); }
        form.appendChild(head);
        var values = {}; if (row) { row.columns.forEach(function (c, i) { values[c.name] = { v: row.rows[0][i], binary: c.binary }; }); }
        var fields = [];
        (st.columns || []).forEach(function (c) {
            var cur = values[c.name] || { v: null, binary: /blob|binary/i.test(c.type) };
            var binary = cur.binary || /^(tiny|medium|long)?blob|^(var)?binary|^bit/i.test(c.type);
            var g = el('div', 'row g-2 align-items-start mb-2');
            var lab = el('label', 'col-md-3 col-form-label col-form-label-sm text-md-end');
            lab.appendChild(el('span', 'fw-semibold font-monospace', c.name));
            lab.appendChild(el('div', 'small text-body-secondary', c.type + (c.key === 'PRI' ? ' · primary' : '') + (c.extra ? ' · ' + c.extra : '')));
            var id = 'dbf-' + fields.length; lab.htmlFor = id;
            var col = el('div', 'col-md-9');
            var long = /text|blob|json/i.test(c.type) || (typeof cur.v === 'string' && (cur.v.length > 80 || cur.v.indexOf('\n') >= 0));
            var input = el(long ? 'textarea' : 'input', 'form-control form-control-sm font-monospace'); input.id = id;
            if (long) { input.rows = 3; }
            var readonly = cur.v && typeof cur.v === 'object' && cur.v.truncated;
            if (binary) { input.placeholder = 'hexadecimal, e.g. 00ff10'; input.value = cur.v && typeof cur.v === 'object' && cur.v.hex !== undefined ? cur.v.hex : (typeof cur.v === 'string' ? Array.from(new TextEncoder().encode(cur.v)).map(function (b) { return ('0' + b.toString(16)).slice(-2); }).join('') : ''); }
            else { input.value = cur.v === null ? '' : cellText(cur.v); }
            if (readonly) { input.disabled = true; }
            col.appendChild(input);
            var opts = el('div', 'd-flex gap-3 small mt-1');
            var nullBox = null, defBox = null;
            if (c.nullable) {
                nullBox = el('input', 'form-check-input'); nullBox.type = 'checkbox'; nullBox.id = id + '-null'; nullBox.checked = !insert && cur.v === null;
                var nl = el('label', 'form-check-label', 'NULL'); nl.htmlFor = nullBox.id; var nw = el('div', 'form-check'); nw.append(nullBox, nl); opts.appendChild(nw);
            }
            if (insert) {
                defBox = el('input', 'form-check-input'); defBox.type = 'checkbox'; defBox.id = id + '-def';
                var hasDef = c.default !== null && c.default !== undefined;
                defBox.checked = /auto_increment|DEFAULT_GENERATED|VIRTUAL|STORED/i.test(c.extra) || hasDef || c.nullable;
                var dl = el('label', 'form-check-label', /auto_increment/i.test(c.extra) ? 'Automatic' : 'Default' + (hasDef ? ' (' + c.default + ')' : c.nullable ? ' (NULL)' : '')); dl.htmlFor = defBox.id;
                var dw = el('div', 'form-check'); dw.append(defBox, dl); opts.appendChild(dw);
            }
            if (binary) { opts.appendChild(el('span', 'text-body-secondary', 'binary — edit as hexadecimal')); }
            if (readonly) { opts.appendChild(el('span', 'text-warning', 'value too large to edit here (' + fmtBytes(cur.v.size) + ')')); }
            if (c.comment) { opts.appendChild(el('span', 'text-body-secondary', c.comment)); }
            col.appendChild(opts);
            var sync = function () { input.disabled = readonly || (nullBox && nullBox.checked) || (defBox && defBox.checked); if (nullBox && defBox && defBox.checked) { nullBox.checked = false; } };
            if (nullBox) { nullBox.addEventListener('change', function () { if (nullBox.checked && defBox) { defBox.checked = false; } sync(); }); }
            if (defBox) { defBox.addEventListener('change', sync); }
            sync();
            g.append(lab, col); form.appendChild(g);
            fields.push({ c: c, input: input, nullBox: nullBox, defBox: defBox, binary: binary, readonly: readonly, orig: input.value, origNull: nullBox ? nullBox.checked : false });
        });
        var foot = el('div', 'd-flex gap-2 mt-3');
        var save = el('button', 'btn btn-sm btn-primary', insert ? 'Insert' : 'Save changes'); save.type = 'submit';
        foot.append(save, btn('Cancel', '', 'btn-outline-secondary', function () { switchTab('browse'); }));
        form.appendChild(foot);
        form.addEventListener('submit', function (e) {
            e.preventDefault();
            var out = {}, bad = null;
            fields.forEach(function (f) {
                if (f.readonly) { return; }
                var isNull = !!(f.nullBox && f.nullBox.checked);
                if (!insert && f.input.value === f.orig && isNull === f.origNull) { return; }
                if (f.defBox && f.defBox.checked) { return; }
                if (isNull) { out[f.c.name] = null; return; }
                if (f.binary) {
                    var h = f.input.value.replace(/^0x/i, '').replace(/\s+/g, '');
                    if (!/^([0-9a-fA-F]{2})*$/.test(h)) { bad = f; return; }
                    out[f.c.name] = { hex: h.toLowerCase() };
                } else { out[f.c.name] = f.input.value; }
            });
            if (bad) { bad.input.classList.add('is-invalid'); bad.input.focus(); WhaCP.toast(bad.c.name + ': enter an even number of hexadecimal digits.', 'danger'); return; }
            if (!insert && Object.keys(out).length === 0) { WhaCP.toast('Nothing changed.', 'secondary'); return; }
            if (insert && Object.keys(out).length === 0) { (st.columns || []).some(function (c) { out[c.name] = { default: true }; return true; }); }
            save.disabled = true;
            call('save', insert ? { table: state.table, insert: true, values: out } : { table: state.table, key: key, values: out }).then(function (r) {
                WhaCP.toast(insert ? 'Row inserted' + (r.insert_id ? ' (id ' + r.insert_id + ')' : '') + '.' : (r.affected_rows ? 'Row saved.' : 'Saved; no values changed.'), 'success');
                switchTab('browse');
            }).catch(function (err) { save.disabled = false; fail(err); });
        });
        pane.appendChild(form);
        var firstInput = form.querySelector('input.form-control:not(:disabled), textarea:not(:disabled)'); if (firstInput) { firstInput.focus(); }
    }

    // ---- forms ----------------------------------------------------------------
    // One dialog for structure changes and imports. submit(dry) returns a
    // promise; with preview, "Show SQL" asks the agent for the statement
    // without running it.
    var formEl = document.getElementById('db-form');
    function formDialog(title, body, okLabel, submit, preview) {
        var modal = bootstrap.Modal.getOrCreateInstance(formEl);
        var form = formEl.querySelector('[data-db-form]'), ok = formEl.querySelector('[data-db-form-ok]'), pv = formEl.querySelector('[data-db-form-preview]'), sqlOut = formEl.querySelector('[data-db-form-sql]');
        formEl.querySelector('.modal-title').textContent = title;
        formEl.querySelector('[data-db-form-body]').replaceChildren(body);
        ok.textContent = okLabel; ok.disabled = false; sqlOut.textContent = '';
        pv.classList.toggle('d-none', !preview);
        var guard = function (fn) { try { return fn(); } catch (err) { WhaCP.toast(err.message, 'danger'); return null; } };
        pv.onclick = function () { var pr = guard(function () { return submit(true); }); if (pr) { pr.then(function (r) { sqlOut.textContent = r.sql || ''; }).catch(fail); } };
        form.onsubmit = function (e) {
            e.preventDefault();
            var pr = guard(function () { return submit(false); });
            if (!pr) { return; }
            ok.disabled = true;
            pr.then(function () { modal.hide(); }).catch(fail).then(function () { ok.disabled = false; });
        };
        modal.show();
        formEl.addEventListener('shown.bs.modal', function f() { formEl.removeEventListener('shown.bs.modal', f); var first = formEl.querySelector('[data-db-form-body] input, [data-db-form-body] select'); if (first) { first.focus(); } });
    }
    function field(label, input, cls, help) {
        var id = 'dbf-' + Math.random().toString(36).slice(2, 9);
        var wrap = el('div', cls || 'col-sm-6'), lab = el('label', 'form-label small mb-1', label);
        lab.htmlFor = id; input.id = id; wrap.append(lab, input);
        if (help) { wrap.appendChild(el('div', 'form-text', help)); }
        return wrap;
    }
    function input(value, placeholder) { var i = el('input', 'form-control form-control-sm'); i.value = value === undefined || value === null ? '' : value; if (placeholder) { i.placeholder = placeholder; } return i; }
    function select(options, value) {
        var sel = el('select', 'form-select form-select-sm');
        options.forEach(function (o) { var opt = el('option', '', Array.isArray(o) ? o[1] : o); opt.value = Array.isArray(o) ? o[0] : o; sel.appendChild(opt); });
        if (value !== undefined) { sel.value = value; }
        return sel;
    }
    function check(label, on) {
        var wrap = el('div', 'form-check col-auto'), c = el('input', 'form-check-input'), id = 'dbc-' + Math.random().toString(36).slice(2, 9);
        c.type = 'checkbox'; c.checked = !!on; c.id = id;
        var l = el('label', 'form-check-label small', label); l.htmlFor = id; wrap.append(c, l);
        return { node: wrap, box: c };
    }
    function multiSelect(options, chosen) {
        var sel = select(options); sel.multiple = true; sel.size = Math.min(6, Math.max(3, options.length));
        Array.from(sel.options).forEach(function (o) { o.selected = (chosen || []).indexOf(o.value) >= 0; });
        return sel;
    }
    function chosen(sel) { return Array.from(sel.selectedOptions).map(function (o) { return o.value; }); }
    function alter(params) {
        return function (dry) {
            var p = Object.assign({ table: state.table }, params(), { dry_run: dry });
            p.alter_op = p.op; delete p.op;
            return call('alter', p).then(function (r) {
                if (!dry) {
                    WhaCP.toast('Done: ' + (r.sql || '').split('\n')[0], 'success');
                    if (r.result && r.result.rows) { WhaCP.toast(r.result.rows.map(function (row) { return row.map(cellText).join(' · '); }).join('\n'), 'info'); }
                    delete state.structure[p.table];
                    return loadTables().then(function () { if (state.tab === 'structure' && state.table) { showStructure(); } });
                }
                return r;
            });
        };
    }

    var TYPES = ['INT', 'BIGINT', 'TINYINT', 'SMALLINT', 'MEDIUMINT', 'DECIMAL', 'FLOAT', 'DOUBLE', 'BIT', 'VARCHAR', 'CHAR', 'TEXT', 'TINYTEXT', 'MEDIUMTEXT', 'LONGTEXT', 'DATE', 'DATETIME', 'TIMESTAMP', 'TIME', 'YEAR', 'ENUM', 'SET', 'JSON', 'BINARY', 'VARBINARY', 'BLOB', 'TINYBLOB', 'MEDIUMBLOB', 'LONGBLOB'];
    var LENGTH_HINT = { VARCHAR: 'e.g. 255 (required)', CHAR: 'e.g. 2', VARBINARY: 'required', BINARY: 'bytes', DECIMAL: 'precision,scale e.g. 10,2', BIT: '1–64', DATETIME: 'fractional seconds 0–6', TIMESTAMP: 'fractional seconds 0–6', TIME: 'fractional seconds 0–6' };
    // The fields of one column; read() returns the agent's column spec.
    function columnForm(c) {
        c = c || { base_type: 'VARCHAR', length: '255', nullable: true, values: [] };
        // MariaDB reports DEFAULT NULL as the word NULL and quotes string defaults.
        var none = c.default === undefined || c.default === null || c.default === 'NULL';
        var def = none ? (c.nullable && c.name && c.default !== null ? 'null' : 'none') : (/^current_timestamp/i.test(String(c.default)) ? 'current_timestamp' : 'value');
        var raw = c.default === null || c.default === undefined ? '' : String(c.default).replace(/^'(.*)'$/s, '$1').replace(/''/g, "'");
        var grid = el('div', 'row g-2');
        var name = input(c.name, 'column_name'), type = select(TYPES, c.base_type || 'VARCHAR'), len = input(c.length), vals = el('textarea', 'form-control form-control-sm font-monospace');
        vals.rows = 3; vals.value = (c.values || []).join('\n');
        var dk = select([['none', 'None'], ['null', 'NULL'], ['value', 'A value'], ['current_timestamp', 'CURRENT_TIMESTAMP']], def), dv = input(def === 'value' ? raw : '');
        var comment = input(c.comment);
        var uns = check('Unsigned', c.unsigned), nul = check('Allow NULL', c.nullable), ai = check('Auto increment', /auto_increment/i.test(c.extra || '')), onup = check('On update CURRENT_TIMESTAMP', /on update/i.test(c.extra || ''));
        var lenF = field('Length / values', len, 'col-sm-4'), valsF = field('Values (one per line)', vals, 'col-12'), dvF = field('Default value', dv, 'col-sm-4');
        grid.append(field('Name', name, 'col-sm-4'), field('Type', type, 'col-sm-4'), lenF, valsF, field('Default', dk, 'col-sm-4'), dvF, field('Comment', comment, 'col-sm-4'));
        var flags = el('div', 'row g-3 mt-1 px-1'); flags.append(nul.node, uns.node, ai.node, onup.node); grid.appendChild(flags);
        var sync = function () {
            var t = type.value, enumish = t === 'ENUM' || t === 'SET';
            lenF.classList.toggle('d-none', enumish || !LENGTH_HINT[t]); valsF.classList.toggle('d-none', !enumish);
            len.placeholder = LENGTH_HINT[t] || '';
            uns.node.classList.toggle('d-none', !/INT$|DECIMAL|FLOAT|DOUBLE/.test(t));
            ai.node.classList.toggle('d-none', !/INT$/.test(t));
            onup.node.classList.toggle('d-none', t !== 'DATETIME' && t !== 'TIMESTAMP');
            dvF.classList.toggle('d-none', dk.value !== 'value');
        };
        type.addEventListener('change', sync); dk.addEventListener('change', sync); sync();
        return {
            node: grid,
            read: function () {
                var t = type.value, hide = function (n) { return n.classList.contains('d-none'); };
                if (!name.value.trim()) { throw new Error('Enter a column name.'); }
                return {
                    name: name.value.trim(), type: t, length: hide(lenF) ? '' : len.value.trim(),
                    values: hide(valsF) ? [] : vals.value.split('\n').filter(function (v) { return v !== ''; }),
                    unsigned: !hide(uns.node) && uns.box.checked, nullable: nul.box.checked,
                    default_kind: dk.value, 'default': dv.value, auto_increment: !hide(ai.node) && ai.box.checked,
                    on_update_current_timestamp: !hide(onup.node) && onup.box.checked, comment: comment.value
                };
            }
        };
    }
    function positionSelect(st, current) {
        var opts = [['', 'Keep / at the end'], ['^', 'First']];
        (st.columns || []).forEach(function (c) { if (c.name !== current) { opts.push([c.name, 'After ' + c.name]); } });
        return select(opts, '');
    }
    function editColumn(st, c) {
        var f = columnForm(c), pos = positionSelect(st, c && c.name), wrap = el('div');
        wrap.appendChild(f.node);
        var pr = el('div', 'row g-2 mt-1'); pr.appendChild(field('Position', pos, 'col-sm-6')); wrap.appendChild(pr);
        if (c) { wrap.appendChild(el('div', 'form-text mt-2', 'Changing the type converts existing values; values that do not fit may be cut or refused.')); }
        formDialog(c ? 'Change column ' + c.name : 'Add a column to ' + state.table, wrap, c ? 'Save column' : 'Add column',
            alter(function () { return c ? { op: 'modify_column', name: c.name, column: f.read(), after: pos.value } : { op: 'add_column', column: f.read(), after: pos.value }; }), true);
    }
    function addIndex(st) {
        var names = (st.columns || []).map(function (c) { return c.name; });
        var kind = select([['index', 'Index'], ['unique', 'Unique'], ['fulltext', 'Full text'], ['primary', 'Primary key']]), name = input('', 'optional'), cols = multiSelect(names);
        var g = el('div', 'row g-2'); g.append(field('Kind', kind, 'col-sm-4'), field('Name', name, 'col-sm-8'), field('Columns (in order of selection)', cols, 'col-12', 'Hold Ctrl or Cmd to choose several.'));
        formDialog('Add an index to ' + state.table, g, 'Add index', alter(function () {
            if (!chosen(cols).length) { throw new Error('Choose at least one column.'); }
            return { op: 'add_index', kind: kind.value, name: name.value.trim(), index_columns: chosen(cols) };
        }), true);
    }
    function addForeignKey(st) {
        var names = (st.columns || []).map(function (c) { return c.name; });
        var name = input('', 'optional, e.g. fk_orders_user'), cols = multiSelect(names), ref = select(state.tables.filter(function (t) { return !t.view; }).map(function (t) { return t.name; }));
        var refCols = input('', 'e.g. id'), rules = [['restrict', 'RESTRICT'], ['cascade', 'CASCADE'], ['set null', 'SET NULL'], ['no action', 'NO ACTION']];
        var del = select(rules), upd = select(rules);
        var g = el('div', 'row g-2');
        g.append(field('Name', name, 'col-12'), field('Columns', cols, 'col-sm-6'), field('Referenced table', ref, 'col-sm-6'), field('Referenced columns', refCols, 'col-sm-6', 'Comma-separated, same order and number as the columns.'), field('On delete', del, 'col-sm-3'), field('On update', upd, 'col-sm-3'));
        g.appendChild(el('div', 'form-text col-12', 'Both tables must use InnoDB, and the referenced columns need an index.'));
        formDialog('Add a foreign key to ' + state.table, g, 'Add foreign key', alter(function () {
            return { op: 'add_foreign_key', name: name.value.trim(), index_columns: chosen(cols), ref_table: ref.value, ref_columns: refCols.value.split(',').map(function (x) { return x.trim(); }).filter(Boolean), on_delete: del.value, on_update: upd.value };
        }), true);
    }
    function newTable() {
        var tname = input('', 'table_name'), engine = select([['InnoDB', 'InnoDB'], ['Aria', 'Aria'], ['MyISAM', 'MyISAM'], ['MEMORY', 'MEMORY']]);
        var list = el('div', 'd-flex flex-column gap-3'), forms = [];
        var addCol = function (c) { var f = columnForm(c), card = el('div', 'border rounded p-2'); card.appendChild(f.node); forms.push(f); list.appendChild(card); };
        addCol({ name: 'id', base_type: 'INT', unsigned: true, extra: 'auto_increment', nullable: false, values: [] });
        var g = el('div', 'row g-2 mb-3'); g.append(field('Table name', tname, 'col-sm-8'), field('Engine', engine, 'col-sm-4'));
        var wrap = el('div'); wrap.append(g, el('div', 'small fw-semibold mb-2', 'Columns'), list, btn('Another column', 'bi-plus-lg', 'btn-outline-secondary mt-2', function () { addCol(); }), el('div', 'form-text', 'The first column becomes the primary key.'));
        formDialog('New table', wrap, 'Create table', function (dry) {
            if (!tname.value.trim()) { throw new Error('Enter a table name.'); }
            var cols = forms.map(function (f) { return f.read(); });
            var p = { table: tname.value.trim(), alter_op: 'create_table', columns: cols, primary: [cols[0].name], engine: engine.value, dry_run: dry };
            return call('alter', p).then(function (r) {
                if (dry) { return r; }
                WhaCP.toast('Table ' + p.table + ' created.', 'success');
                return loadTables().then(function () { selectTable(p.table); });
            });
        }, true);
    }
    function renameTable() {
        var n = input(state.table);
        formDialog('Rename ' + state.table, field('New name', n, 'col-12'), 'Rename', function (dry) {
            var to = n.value.trim();
            return alter(function () { return { op: 'rename_table', new_name: to }; })(dry).then(function (r) { if (!dry) { selectTable(to); } return r; });
        }, true);
    }

    // ---- structure --------------------------------------------------------
    function showStructure() {
        var name = state.table; loading(pane);
        structure(name).then(function (st) {
            if (state.table !== name || state.tab !== 'structure') { return; }
            pane.replaceChildren();
            var info = state.tables.filter(function (t) { return t.name === name; })[0] || {}, view = !!info.view, s = st.status || {};
            var body = el('div', 'p-3');
            var h = function (text, actions) { var d = el('div', 'd-flex align-items-center gap-2 mt-3 mb-2'); d.appendChild(el('h2', 'h6 mb-0', text)); (actions || []).forEach(function (a) { d.appendChild(a); }); return d; };
            var rowOps = function (cells, ops) { var td = el('td', 'text-end text-nowrap'); ops.forEach(function (o) { td.appendChild(o); }); cells.push(td); return cells; };

            // Status.
            var facts = [['Engine', s.engine], ['Rows (about)', s.rows !== '' && s.rows !== undefined ? Number(s.rows).toLocaleString() : ''], ['Data', s.data_bytes ? fmtBytes(+s.data_bytes) : ''], ['Indexes', s.index_bytes ? fmtBytes(+s.index_bytes) : ''], ['Free', s.free_bytes ? fmtBytes(+s.free_bytes) : ''], ['Row format', s.row_format], ['Next auto-increment', s.auto_increment], ['Collation', s.collation], ['Created', s.created], ['Updated', s.updated], ['Comment', s.comment]];
            var dl = el('dl', 'row small mb-0 wcp-db-facts');
            facts.forEach(function (f) { if (f[1]) { dl.append(el('dt', 'col-sm-3 col-lg-2 fw-normal text-body-secondary', f[0]), el('dd', 'col-sm-9 col-lg-4 mb-1', String(f[1]))); } });
            body.append(h(view ? 'View' : 'Table'), dl);

            // Columns.
            body.appendChild(h('Columns', view ? [] : [btn('Add column', 'bi-plus-lg', 'btn-sm btn-outline-primary ms-auto', function () { editColumn(st, null); })]));
            body.appendChild(opsTable(['Name', 'Type', 'Null', 'Key', 'Default', 'Extra', 'Comment', ''], (st.columns || []).map(function (c) {
                var cells = [c.name, c.type, c.nullable ? 'yes' : 'no', c.key, c.default, c.extra, c.comment];
                if (view) { cells.push(''); return cells; }
                var e = btn('', 'bi-pencil', 'btn-link p-0 me-2', function () { editColumn(st, c); }); e.setAttribute('aria-label', 'Change column ' + c.name);
                var d = btn('', 'bi-trash', 'btn-link p-0 text-danger', function () { dangerAlter({ op: 'drop_column', name: c.name }, 'Drop column', 'Delete the column ' + c.name + ' and its values from every row? This cannot be undone.', 'Drop'); }); d.setAttribute('aria-label', 'Drop column ' + c.name);
                return rowOps(cells, [e, d]);
            })));

            if (!view) {
                body.appendChild(h('Indexes', [btn('Add index', 'bi-plus-lg', 'btn-sm btn-outline-primary ms-auto', function () { addIndex(st); })]));
                body.appendChild((st.indexes || []).length ? opsTable(['Name', 'Columns', 'Unique', 'Type', ''], st.indexes.map(function (i) {
                    var d = btn('', 'bi-trash', 'btn-link p-0 text-danger', function () { dangerAlter({ op: 'drop_index', name: i.name }, 'Drop index', 'Drop the index ' + i.name + '?' + (i.name === 'PRIMARY' ? ' Rows can then only be edited from the SQL tab unless another unique key exists.' : ''), 'Drop'); }); d.setAttribute('aria-label', 'Drop index ' + i.name);
                    return rowOps([i.name, i.columns, i.unique ? 'yes' : 'no', i.type], [d]);
                })) : el('p', 'text-body-secondary small', 'No indexes.'));

                body.appendChild(h('Foreign keys', [btn('Add foreign key', 'bi-plus-lg', 'btn-sm btn-outline-primary ms-auto', function () { addForeignKey(st); })]));
                body.appendChild((st.foreign_keys || []).length ? opsTable(['Name', 'Columns', 'References', 'On delete', 'On update', ''], st.foreign_keys.map(function (k) {
                    var d = btn('', 'bi-trash', 'btn-link p-0 text-danger', function () { dangerAlter({ op: 'drop_foreign_key', name: k.name }, 'Drop foreign key', 'Drop the foreign key ' + k.name + '? The column and its values stay.', 'Drop'); }); d.setAttribute('aria-label', 'Drop foreign key ' + k.name);
                    return rowOps([k.name, k.columns, k.ref_table + ' (' + k.ref_columns + ')', k.on_delete, k.on_update], [d]);
                })) : el('p', 'text-body-secondary small', 'No foreign keys.'));
            }

            var ch = h('CREATE statement', [btn('Copy', 'bi-clipboard', 'btn-sm btn-outline-secondary', function () { navigator.clipboard.writeText(st.create || '').then(function () { WhaCP.toast('Copied.', 'success'); }); })]);
            body.append(ch, el('pre', 'wcp-db-create', st.create || ''));

            var ops = el('div', 'd-flex flex-wrap gap-2 mt-3');
            if (view) {
                ops.append(btn('Drop view', 'bi-trash', 'btn-outline-danger', function () { dangerAlter({ op: 'drop_view' }, 'Drop the view', 'Delete the view ' + name + '? Its tables stay.', 'Drop', name); }));
            } else {
                ops.append(
                    btn('Rename', 'bi-input-cursor-text', 'btn-outline-secondary', renameTable),
                    btn('Optimize', 'bi-lightning', 'btn-outline-secondary', function () { alter(function () { return { op: 'optimize_table' }; })(false).catch(fail); }),
                    btn('Analyze', 'bi-graph-up', 'btn-outline-secondary', function () { alter(function () { return { op: 'analyze_table' }; })(false).catch(fail); }),
                    btn('Check', 'bi-check2-square', 'btn-outline-secondary', function () { alter(function () { return { op: 'check_table' }; })(false).catch(fail); }),
                    btn('Repair', 'bi-wrench', 'btn-outline-secondary', function () { alter(function () { return { op: 'repair_table' }; })(false).catch(fail); }),
                    btn('Empty table', 'bi-eraser', 'btn-outline-danger', function () { dangerAlter({ op: 'truncate_table' }, 'Empty the table', 'Delete every row of ' + name + '? This cannot be undone.', 'Empty'); }),
                    btn('Drop table', 'bi-trash', 'btn-outline-danger', function () { dangerAlter({ op: 'drop_table' }, 'Drop the table', 'Delete the table ' + name + ' and all of its data? This cannot be undone.', 'Drop', name); })
                );
            }
            body.appendChild(ops);
            pane.appendChild(body);
        }).catch(function (e) { pane.replaceChildren(el('div', 'alert alert-danger m-3', (e.details && e.details.details) || e.message)); });
    }
    function dangerAlter(params, title, msg, ok, word) {
        confirmDialog(title, msg, ok, word).then(function (yes) {
            if (!yes) { return; }
            var dropping = params.op === 'drop_table' || params.op === 'drop_view';
            alter(function () { return params; })(false).then(function () { if (dropping) { selectTable(null); } }).catch(fail);
        });
    }
    function opsTable(head, rows) {
        var t = simpleTable(head, rows.map(function (r) { return r.map(function (c) { return c instanceof Node ? '' : c; }); }));
        // Put action cells (DOM nodes) in place of the placeholders.
        var trs = t.querySelectorAll('tbody tr');
        rows.forEach(function (r, i) { r.forEach(function (c, j) { if (c instanceof Node) { trs[i].children[j].replaceWith(c); } }); });
        return t;
    }

    // ---- CSV export / import ------------------------------------------------
    function exportCsv() {
        var p = state.page, params = { table: state.table };
        if (p.filter) { params.filter = p.filter; }
        if (p.search) { params.search = p.search; }
        WhaCP.toast('Exporting ' + state.table + '…', 'info');
        call('export_csv', params).then(function (r) {
            download(r.csv, state.table + '.csv', 'text/csv;charset=utf-8');
            if (r.truncated) { WhaCP.toast('Only the first ' + Number(r.rows).toLocaleString() + ' rows were exported; use the database export for the whole table.', 'warning'); }
        }).catch(fail);
    }
    function importCsv(st) {
        var file = el('input', 'form-control form-control-sm'); file.type = 'file'; file.accept = '.csv,.tsv,.txt,text/csv';
        var delim = select([[',', 'Comma'], [';', 'Semicolon'], ['tab', 'Tab']]), header = check('The first row holds column names', true), mode = select([['insert', 'Stop at duplicates (nothing is imported)'], ['ignore', 'Skip rows that duplicate a unique key'], ['replace', 'Replace rows with the same unique key']]);
        var mapBox = el('div', 'row g-2 mt-1'), text = '', cols = (st.columns || []).map(function (c) { return c.name; }), maps = [];
        var g = el('div', 'row g-2'); g.append(field('CSV file (up to 16 MB)', file, 'col-12', 'NULL is written \\N (as in the export). Everything is imported in one transaction: all rows or none.'), field('Separator', delim, 'col-sm-4'), field('Duplicates', mode, 'col-sm-8'));
        var hn = el('div', 'col-12'); hn.appendChild(header.node); g.appendChild(hn);
        var wrap = el('div'); wrap.append(g, el('div', 'small fw-semibold mt-3', 'Columns'), mapBox);
        var parseHead = function () {
            mapBox.replaceChildren(); maps = [];
            if (!text) { mapBox.appendChild(el('div', 'text-body-secondary small', 'Choose a file.')); return; }
            var d = delim.value === 'tab' ? '\t' : delim.value;
            var first = text.replace(/^﻿/, '').split(/\r?\n/, 1)[0].split(d);
            first.forEach(function (h, i) {
                var label = header.box.checked ? h.replace(/^"|"$/g, '') : 'Column ' + (i + 1);
                var guess = cols.indexOf(label) >= 0 ? label : (header.box.checked ? '' : (cols[i] || ''));
                var sel = select([['', '— skip —']].concat(cols.map(function (c) { return [c, c]; })), guess);
                maps.push(sel); mapBox.appendChild(field(label || '(empty)', sel, 'col-sm-6 col-lg-4'));
            });
        };
        file.addEventListener('change', function () {
            var f = file.files[0]; if (!f) { return; }
            if (f.size > 16 * 1024 * 1024) { WhaCP.toast('The file is larger than 16 MB.', 'danger'); file.value = ''; return; }
            f.text().then(function (t) { text = t; if (/\.tsv$/i.test(f.name)) { delim.value = 'tab'; } else if (t.split('\n', 1)[0].split(';').length > t.split('\n', 1)[0].split(',').length) { delim.value = ';'; } parseHead(); });
        });
        delim.addEventListener('change', parseHead); header.box.addEventListener('change', parseHead); parseHead();
        formDialog('Import CSV into ' + state.table, wrap, 'Import', function () {
            if (!text) { throw new Error('Choose a CSV file.'); }
            return call('import_csv', { table: state.table, csv: text, delimiter: delim.value, header: header.box.checked, columns: maps.map(function (m) { return m.value; }), mode: mode.value }).then(function (r) {
                WhaCP.toast(Number(r.rows).toLocaleString() + ' row(s) read, ' + Number(r.affected_rows).toLocaleString() + ' changed.', 'success');
                state.structure = {}; loadTables(); if (state.tab === 'browse') { browse(); }
            });
        }, false);
    }
    function download(text, filename, type) {
        var a = document.createElement('a');
        a.href = URL.createObjectURL(new Blob([text], { type: type }));
        a.download = filename; document.body.appendChild(a); a.click(); a.remove();
        setTimeout(function () { URL.revokeObjectURL(a.href); }, 1000);
    }

    function simpleTable(head, rows) {
        var wrap = el('div', 'table-responsive'), t = el('table', 'table table-sm wcp-db-grid'), tr = el('tr');
        head.forEach(function (h) { tr.appendChild(el('th', '', h)); });
        var th = el('thead'); th.appendChild(tr); t.appendChild(th);
        var tb = el('tbody');
        rows.forEach(function (r) { var row = el('tr'); r.forEach(function (v) { var td = el('td'); td.appendChild(cellNode(v === undefined ? '' : v)); row.appendChild(td); }); tb.appendChild(row); });
        t.appendChild(tb); wrap.appendChild(t); return wrap;
    }

    // ---- SQL console --------------------------------------------------------
    // Statements that deserve a confirmation: DROP, TRUNCATE, ALTER, and
    // DELETE or UPDATE without WHERE (a rough check on each statement).
    function destructive(sql) {
        return sql.split(';').some(function (st) {
            st = st.replace(/--[^\n]*|#[^\n]*|\/\*[\s\S]*?\*\//g, ' ').trim();
            return /^(DROP|TRUNCATE|ALTER)\b/i.test(st) || (/^(DELETE|UPDATE)\b/i.test(st) && !/\bWHERE\b/i.test(st));
        });
    }
    function showSql() {
        pane.replaceChildren();
        var box = el('div', 'p-3 d-flex flex-column gap-2 wcp-db-sql');
        var ta = el('textarea', 'form-control font-monospace wcp-db-sqltext'); ta.rows = 8; ta.spellcheck = false; ta.value = state.sql; ta.setAttribute('aria-label', 'SQL statements');
        var hl = el('pre', 'wcp-sql-hl'); hl.setAttribute('aria-hidden', 'true');
        var editor = el('div', 'wcp-sql-edit'); editor.append(hl, ta);
        var paint = function () { highlight(hl, ta.value); hl.scrollTop = ta.scrollTop; hl.scrollLeft = ta.scrollLeft; };
        ta.addEventListener('input', function () { state.sql = ta.value; paint(); });
        ta.addEventListener('scroll', function () { hl.scrollTop = ta.scrollTop; hl.scrollLeft = ta.scrollLeft; });
        paint();
        var bar = el('div', 'd-flex flex-wrap align-items-center gap-2');
        var run = btn('Run', 'bi-play-fill', 'btn-primary', function () { exec(); });
        bar.append(run, el('span', 'small text-body-secondary', 'Ctrl+Enter runs. Several statements may be separated by semicolons. Queries can reach this database only.'));
        var hist = history(), hs = el('select', 'form-select form-select-sm w-auto'); hs.setAttribute('aria-label', 'Query history');
        var fillHist = function () {
            hs.replaceChildren(); hist = history();
            var o = el('option', '', hist.length ? 'History (' + hist.length + ')…' : 'No history yet'); o.value = ''; hs.appendChild(o);
            hist.forEach(function (q, i) { var op = el('option', '', q.replace(/\s+/g, ' ').slice(0, 90)); op.value = String(i); hs.appendChild(op); });
            if (hist.length) { var c = el('option', '', 'Clear history'); c.value = 'clear'; hs.appendChild(c); }
        };
        hs.addEventListener('change', function () {
            if (hs.value === 'clear') { saveHistory([]); } else if (hs.value !== '') { ta.value = hist[+hs.value]; state.sql = ta.value; paint(); ta.focus(); }
            fillHist();
        });
        fillHist(); bar.appendChild(hs);
        if (state.table) {
            var snip = el('select', 'form-select form-select-sm w-auto ms-auto'); snip.setAttribute('aria-label', 'Insert a statement template');
            [['', 'Templates…'], ['select', 'SELECT'], ['insert', 'INSERT'], ['update', 'UPDATE'], ['delete', 'DELETE'], ['count', 'COUNT(*)']].forEach(function (o) { var opt = el('option', '', o[1]); opt.value = o[0]; snip.appendChild(opt); });
            snip.addEventListener('change', function () {
                var t = ident(state.table), st = state.structure[state.table], cols = st ? st.columns.map(function (c) { return ident(c.name); }) : ['…'];
                var s = { select: 'SELECT ' + cols.join(', ') + '\nFROM ' + t + '\nWHERE 1\nLIMIT 50', insert: 'INSERT INTO ' + t + ' (' + cols.join(', ') + ')\nVALUES (' + cols.map(function () { return '?'; }).join(', ') + ')', update: 'UPDATE ' + t + '\nSET ' + cols[0] + ' = ?\nWHERE …\nLIMIT 1', 'delete': 'DELETE FROM ' + t + '\nWHERE …\nLIMIT 1', count: 'SELECT COUNT(*) FROM ' + t }[snip.value];
                if (s) { ta.value = s; state.sql = s; ta.focus(); }
                snip.value = '';
            });
            bar.appendChild(snip);
            structure(state.table).catch(function () {});
        }
        var out = el('div', 'd-flex flex-column gap-3 mt-2'); out.setAttribute('aria-live', 'polite');
        box.append(editor, bar, out);
        pane.appendChild(box);
        ta.addEventListener('keydown', function (e) { if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { e.preventDefault(); exec(); } });
        ta.focus();

        function exec() {
            var sql = ta.value.trim(); if (!sql) { return; }
            var go = function () {
                run.disabled = true; out.replaceChildren(el('div', 'text-body-secondary small', 'Running…'));
                remember(sql); fillHist();
                call('query', { sql: sql }).then(function (r) {
                    out.replaceChildren();
                    (r.results || []).forEach(function (res, i) { out.appendChild(resultNode(res, i + 1)); });
                    if (r.error) { out.appendChild(el('div', 'alert alert-danger mb-0 small', 'Statement ' + r.error_statement + ': ' + r.error)); }
                    out.appendChild(el('div', 'small text-body-secondary', (r.results || []).length + ' statement(s) · ' + r.ms + ' ms'));
                    if (/\b(CREATE|DROP|ALTER|RENAME|TRUNCATE)\b/i.test(sql)) { state.structure = {}; loadTables(); }
                }).catch(function (e) { out.replaceChildren(); fail(e); }).then(function () { run.disabled = false; });
            };
            if (destructive(sql)) {
                confirmDialog('Run this SQL?', 'It drops, empties or alters tables, or deletes rows without a WHERE clause. This cannot be undone.', 'Run').then(function (ok) { if (ok) { go(); } });
            } else { go(); }
        }
    }
    function resultNode(res, n) {
        var card = el('div', 'wcp-db-result');
        if (!res.columns) {
            var t = 'Statement ' + n + ': OK, ' + res.affected_rows + ' row(s) affected' + (res.insert_id ? ', insert id ' + res.insert_id : '') + (res.info ? ' — ' + res.info : '') + (res.warnings ? ' · ' + res.warnings + ' warning(s)' : '');
            card.appendChild(el('div', 'small text-success', t)); return card;
        }
        var head = el('div', 'd-flex align-items-center gap-2 mb-1');
        head.appendChild(el('span', 'small text-body-secondary', 'Statement ' + n + ': ' + res.rows.length + ' row(s)' + (res.truncated ? ' (only the first ' + res.rows.length + ' are shown)' : '')));
        if (res.rows.length) { head.appendChild(btn('CSV', 'bi-download', 'btn-outline-secondary ms-auto', function () { csv(res.columns, res.rows, 'query-' + n + '.csv'); })); }
        card.appendChild(head);
        var wrap = el('div', 'wcp-db-grid-wrap wcp-db-grid-result'), table = el('table', 'table table-sm table-hover wcp-db-grid mb-0'), tr = el('tr');
        res.columns.forEach(function (c) { tr.appendChild(el('th', c.numeric ? 'text-end' : '', c.name)); });
        var th = el('thead'); th.appendChild(tr); table.appendChild(th);
        var tb = el('tbody'), per = 100, page = 0, pages = Math.ceil(res.rows.length / per);
        var draw = function () {
            tb.replaceChildren();
            res.rows.slice(page * per, page * per + per).forEach(function (row) { var r = el('tr'); row.forEach(function (v, j) { var td = el('td', res.columns[j].numeric ? 'text-end' : ''); td.appendChild(cellNode(v)); r.appendChild(td); }); tb.appendChild(r); });
            if (pager) { pos.textContent = (page * per + 1) + '–' + Math.min(res.rows.length, page * per + per) + ' of ' + res.rows.length; pb.disabled = page === 0; nb.disabled = page >= pages - 1; }
        };
        var pager = null, pos, pb, nb;
        if (pages > 1) {
            pager = el('div', 'd-flex align-items-center gap-1 mt-1');
            pos = el('span', 'small text-body-secondary me-1');
            pb = btn('', 'bi-chevron-left', 'btn-outline-secondary', function () { page--; draw(); }); pb.setAttribute('aria-label', 'Previous rows');
            nb = btn('', 'bi-chevron-right', 'btn-outline-secondary', function () { page++; draw(); }); nb.setAttribute('aria-label', 'Next rows');
            pager.append(pos, pb, nb);
        }
        draw();
        table.appendChild(tb); wrap.appendChild(table); card.appendChild(wrap);
        if (pager) { card.appendChild(pager); }
        return card;
    }

    // Query history: per database, in this browser only (last 30).
    var histKey = 'wcp-sql-history:' + root.dataset.database;
    function history() { try { var h = JSON.parse(localStorage.getItem(histKey) || '[]'); return Array.isArray(h) ? h.filter(function (x) { return typeof x === 'string'; }) : []; } catch (e) { return []; } }
    function saveHistory(h) { try { localStorage.setItem(histKey, JSON.stringify(h)); } catch (e) { /* storage unavailable */ } }
    function remember(sql) { if (sql.length > 20000) { return; } var h = history().filter(function (q) { return q !== sql; }); h.unshift(sql); saveHistory(h.slice(0, 30)); }

    // SQL highlighting: the text is split into tokens and drawn (as text
    // nodes) behind the transparent textarea.
    var KW = /^(SELECT|FROM|WHERE|AND|OR|NOT|NULL|IS|IN|LIKE|BETWEEN|AS|ON|JOIN|LEFT|RIGHT|INNER|OUTER|CROSS|GROUP|BY|ORDER|HAVING|LIMIT|OFFSET|INSERT|INTO|VALUES|UPDATE|SET|DELETE|CREATE|TABLE|VIEW|INDEX|UNIQUE|PRIMARY|KEY|FOREIGN|REFERENCES|DROP|ALTER|ADD|COLUMN|MODIFY|CHANGE|RENAME|TO|TRUNCATE|DISTINCT|UNION|ALL|CASE|WHEN|THEN|ELSE|END|IF|EXISTS|DEFAULT|AUTO_INCREMENT|ASC|DESC|SHOW|DESCRIBE|EXPLAIN|WITH|REPLACE|IGNORE|DUPLICATE|INTERVAL|TRUE|FALSE|BEGIN|COMMIT|ROLLBACK|START|TRANSACTION|ENGINE|CHARSET|COLLATE|USING|PROCEDURE|FUNCTION|TRIGGER|RETURNS|CALL|DECLARE|INT|INTEGER|BIGINT|TINYINT|SMALLINT|VARCHAR|CHAR|TEXT|LONGTEXT|DATETIME|TIMESTAMP|DATE|DECIMAL|FLOAT|DOUBLE|BLOB|JSON|ENUM|UNSIGNED)$/i;
    var TOKENS = /(--[^\n]*|#[^\n]*|\/\*[\s\S]*?(?:\*\/|$))|('(?:[^'\\]|\\.|'')*'?|"(?:[^"\\]|\\.|"")*"?)|(`(?:[^`]|``)*`?)|(\b\d+(?:\.\d+)?\b)|([A-Za-z_][A-Za-z0-9_]*)/g;
    function highlight(pre, text) {
        var frag = document.createDocumentFragment(), last = 0, m;
        TOKENS.lastIndex = 0;
        while ((m = TOKENS.exec(text)) !== null) {
            if (m.index > last) { frag.appendChild(document.createTextNode(text.slice(last, m.index))); }
            var cls = m[1] ? 'tk-com' : m[2] ? 'tk-str' : m[3] ? 'tk-id' : m[4] ? 'tk-num' : (KW.test(m[5]) ? 'tk-kw' : '');
            frag.appendChild(cls ? el('span', cls, m[0]) : document.createTextNode(m[0]));
            last = TOKENS.lastIndex;
            if (m[0] === '') { TOKENS.lastIndex++; }
        }
        frag.appendChild(document.createTextNode(text.slice(last) + '\n'));
        pre.replaceChildren(frag);
    }

    // CSV of the rows shown (values as displayed; NULL as empty).
    function csv(cols, rows, filename) {
        var q = function (s) { s = String(s); if (/^[=+\-@\t\r]/.test(s)) { s = "'" + s; } return /[",\n\r]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s; };
        var lines = [cols.map(function (c) { return q(c.name); }).join(',')];
        (rows || []).forEach(function (r) { lines.push(r.map(function (v) { return q(cellText(v)); }).join(',')); });
        var a = document.createElement('a');
        a.href = URL.createObjectURL(new Blob(['﻿' + lines.join('\r\n')], { type: 'text/csv;charset=utf-8' }));
        a.download = filename; document.body.appendChild(a); a.click(); a.remove();
        setTimeout(function () { URL.revokeObjectURL(a.href); }, 1000);
    }

    loadTables();
    switchTab('sql');
})();
