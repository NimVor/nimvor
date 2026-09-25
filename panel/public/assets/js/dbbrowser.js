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
    var state = { tables: [], table: null, tab: 'sql', structure: {}, page: { offset: 0, limit: 50, order: '', desc: false, filter: null }, sql: '', rows: null, selected: new Set() };

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
        $('[data-db-tables-count]').textContent = state.tables.length === 0 ? 'No tables yet.' : shown + ' of ' + state.tables.length + ' table(s)';
    }
    tableList.addEventListener('click', function (e) { var li = e.target.closest('[data-table]'); if (li) { selectTable(li.dataset.table); } });
    tableList.addEventListener('keydown', function (e) { var li = e.target.closest('[data-table]'); if (li && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); selectTable(li.dataset.table); } });
    $('[data-db-table-filter]').addEventListener('input', renderTables);

    function selectTable(name) {
        state.table = name;
        state.page = { offset: 0, limit: state.page.limit, order: '', desc: false, filter: null };
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
        actions.append(btn('Insert row', 'bi-plus-lg', 'btn-outline-primary', function () { switchTab('insert'); }), delBtn, btn('Refresh', 'bi-arrow-clockwise', 'btn-outline-secondary', browse), btn('Export CSV', 'bi-filetype-csv', 'btn-outline-secondary', function () { csv(cols, d.rows, state.table + '.csv'); }));
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

    // ---- structure --------------------------------------------------------
    function showStructure() {
        var name = state.table; loading(pane);
        structure(name).then(function (st) {
            if (state.table !== name || state.tab !== 'structure') { return; }
            pane.replaceChildren();
            var body = el('div', 'p-3');
            body.appendChild(el('h2', 'h6', 'Columns'));
            body.appendChild(simpleTable(['Name', 'Type', 'Null', 'Key', 'Default', 'Extra', 'Comment'], (st.columns || []).map(function (c) { return [c.name, c.type, c.nullable ? 'yes' : 'no', c.key, c.default, c.extra, c.comment]; })));
            body.appendChild(el('h2', 'h6 mt-3', 'Indexes'));
            body.appendChild((st.indexes || []).length ? simpleTable(['Name', 'Columns', 'Unique', 'Type'], st.indexes.map(function (i) { return [i.name, i.columns, i.unique ? 'yes' : 'no', i.type]; })) : el('p', 'text-body-secondary small', 'No indexes.'));
            var ch = el('div', 'd-flex align-items-center gap-2 mt-3'); ch.appendChild(el('h2', 'h6 mb-0', 'CREATE statement'));
            var pre = el('pre', 'wcp-db-create', st.create || '');
            ch.appendChild(btn('Copy', 'bi-clipboard', 'btn-outline-secondary', function () { navigator.clipboard.writeText(st.create || '').then(function () { WhaCP.toast('Copied.', 'success'); }); }));
            body.append(ch, pre);
            var ops = el('div', 'd-flex flex-wrap gap-2 mt-3');
            ops.append(
                btn('Empty table', 'bi-eraser', 'btn-outline-danger', function () { danger('TRUNCATE TABLE ' + ident(name), 'Empty the table', 'Delete every row of ' + name + '? This cannot be undone.', 'Empty'); }),
                btn('Drop table', 'bi-trash', 'btn-outline-danger', function () { danger('DROP TABLE ' + ident(name), 'Drop the table', 'Delete the table ' + name + ' and all of its data? This cannot be undone.', 'Drop', name); })
            );
            body.appendChild(ops);
            pane.appendChild(body);
        }).catch(function (e) { pane.replaceChildren(el('div', 'alert alert-danger m-3', (e.details && e.details.details) || e.message)); });
    }
    function danger(sql, title, msg, ok, word) {
        confirmDialog(title, msg, ok, word).then(function (yes) {
            if (!yes) { return; }
            call('query', { sql: sql }).then(function (r) {
                if (r.error) { WhaCP.toast(r.error, 'danger'); return; }
                WhaCP.toast('Done.', 'success'); delete state.structure[state.table];
                loadTables().then(function () { if (state.table) { switchTab('browse'); } });
            }).catch(fail);
        });
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
        ta.addEventListener('input', function () { state.sql = ta.value; });
        var bar = el('div', 'd-flex flex-wrap align-items-center gap-2');
        var run = btn('Run', 'bi-play-fill', 'btn-primary', function () { exec(); });
        bar.append(run, el('span', 'small text-body-secondary', 'Ctrl+Enter runs. Several statements may be separated by semicolons. Queries can reach this database only.'));
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
        box.append(ta, bar, out);
        pane.appendChild(box);
        ta.addEventListener('keydown', function (e) { if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { e.preventDefault(); exec(); } });
        ta.focus();

        function exec() {
            var sql = ta.value.trim(); if (!sql) { return; }
            var go = function () {
                run.disabled = true; out.replaceChildren(el('div', 'text-body-secondary small', 'Running…'));
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
        var tb = el('tbody');
        res.rows.forEach(function (row) { var r = el('tr'); row.forEach(function (v, j) { var td = el('td', res.columns[j].numeric ? 'text-end' : ''); td.appendChild(cellNode(v)); r.appendChild(td); }); tb.appendChild(r); });
        table.appendChild(tb); wrap.appendChild(table); card.appendChild(wrap);
        return card;
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
