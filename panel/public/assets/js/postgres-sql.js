// NimVor PostgreSQL SQL editor page: runs statements through the panel, shows results and history.
(function () {
    'use strict';
    var root = document.getElementById('pgsql');
    if (!root || !window.NimSqlEditor) { return; }
    var cfg = { run: root.dataset.run, cancel: root.dataset.cancel, db: root.dataset.db, name: root.dataset.name, canWrite: root.dataset.canWrite === '1' };
    var schema = {}, history = [];
    try { schema = JSON.parse(root.dataset.schema || '{}'); history = JSON.parse(root.dataset.history || '[]'); } catch (e) { /* empty */ }

    var $ = function (id) { return document.getElementById(id); };
    var PAGE = 50;
    var running = null, results = [], active = 0, page = 0;
    var recentKey = 'pgsql-recent:' + cfg.db;

    var editor = NimSqlEditor.create($('pgsql-editor'), { schema: schema, placeholder: 'SELECT * FROM … ;', onRun: function () { start(''); } });

    function mode() { var w = $('pgsql-write'); return w && w.checked ? 'write' : 'read'; }
    function el(tag, cls, text) { var n = document.createElement(tag); if (cls) { n.className = cls; } if (text !== undefined) { n.textContent = text; } return n; }
    function hexId() { var b = new Uint8Array(8); crypto.getRandomValues(b); return Array.prototype.map.call(b, function (x) { return ('0' + x.toString(16)).slice(-2); }).join(''); }

    // A statement that changes data or structure asks first (a convenience: what the session may do is decided by PostgreSQL).
    function destructive(sql) {
        var s = sql.replace(/--[^\n]*/g, ' ').replace(/\/\*[\s\S]*?\*\//g, ' ').replace(/'(?:[^']|'')*'/g, "''");
        return /\b(drop|truncate|alter|grant|revoke|delete|update)\b/i.test(s) && !/^\s*explain\b(?!\s+analyze)/i.test(s);
    }

    function setStatus(text, cls) { var s = $('pgsql-status'); s.textContent = text; s.className = 'small mb-2 ' + (cls || 'text-body-secondary'); }
    function showError(e) {
        var box = $('pgsql-error');
        if (!e) { box.classList.add('d-none'); box.textContent = ''; return; }
        box.replaceChildren();
        box.appendChild(el('div', 'fw-medium', e.message));
        if (e.detail) { box.appendChild(el('div', '', e.detail)); }
        if (e.hint) { box.appendChild(el('div', 'fst-italic', 'Hint: ' + e.hint)); }
        if (e.code) { box.appendChild(el('div', 'text-body-secondary', 'SQLSTATE ' + e.code + (e.position ? ' · at character ' + e.position : ''))); }
        box.classList.remove('d-none');
    }

    function renderTabs() {
        var tabs = $('pgsql-tabs');
        tabs.replaceChildren();
        results.forEach(function (r, i) {
            var li = el('li', 'nav-item'), a = el('button', 'nav-link py-1' + (i === active ? ' active' : ''), results.length > 1 ? 'Result ' + (i + 1) + ' · ' + (r.command || '') : (r.command || 'Result'));
            a.type = 'button';
            a.addEventListener('click', function () { active = i; page = 0; renderGrid(); renderTabs(); });
            li.appendChild(a); tabs.appendChild(li);
        });
    }

    function renderGrid() {
        var box = $('pgsql-results'), r = results[active];
        if (!r) { box.classList.add('d-none'); return; }
        box.classList.remove('d-none');
        var head = $('pgsql-grid').tHead, body = $('pgsql-grid').tBodies[0];
        head.replaceChildren(); body.replaceChildren();
        var cols = r.columns || [];
        if (cols.length === 0) {
            var tr0 = el('tr'), td0 = el('td', 'p-3 text-body-secondary', (r.command || 'Done') + (r.rows_affected ? ' · ' + r.rows_affected + ' row(s) affected' : ''));
            tr0.appendChild(td0); body.appendChild(tr0);
            $('pgsql-pageinfo').textContent = '';
            return;
        }
        var hr = el('tr');
        cols.forEach(function (c) {
            var th = el('th', 'text-nowrap', c.name);
            if (c.type) { th.appendChild(el('div', 'small fw-normal text-body-secondary', c.type)); }
            hr.appendChild(th);
        });
        head.appendChild(hr);
        var cut = {};
        (r.truncated || []).forEach(function (t) { cut[t[0] + ':' + t[1]] = true; });
        var from = page * PAGE, to = Math.min(r.rows.length, from + PAGE);
        for (var i = from; i < to; i++) {
            var tr = el('tr');
            r.rows[i].forEach(function (v, j) {
                var td = el('td', 'font-monospace');
                td.style.maxWidth = '26rem';
                if (v === null) { td.appendChild(el('span', 'badge text-bg-light border text-body-secondary', 'NULL')); }
                else {
                    var s = el('span', 'd-inline-block text-truncate align-bottom', v);
                    s.style.maxWidth = '26rem'; s.title = v.slice(0, 300);
                    td.appendChild(s);
                    if (cut[i + ':' + j]) { td.appendChild(el('span', 'badge text-bg-warning ms-1', 'cut')); }
                }
                tr.appendChild(td);
            });
            body.appendChild(tr);
        }
        if (r.rows.length === 0) { var te = el('tr'), tde = el('td', 'p-3 text-body-secondary', 'No rows.'); tde.colSpan = cols.length; te.appendChild(tde); body.appendChild(te); }
        $('pgsql-pageinfo').textContent = r.rows.length === 0 ? '' : (from + 1) + '–' + to + ' of ' + r.rows.length + (r.rows_capped ? '+ (limit reached)' : '');
    }

    function csvCell(v) { if (v === null) { return ''; } return /[",\r\n]/.test(v) ? '"' + v.replace(/"/g, '""') + '"' : v; }
    function exportCsv() {
        var r = results[active];
        if (!r || !r.columns || !r.columns.length) { return; }
        var lines = [r.columns.map(function (c) { return csvCell(c.name); }).join(',')];
        r.rows.forEach(function (row) { lines.push(row.map(csvCell).join(',')); });
        var a = document.createElement('a');
        a.href = URL.createObjectURL(new Blob([lines.join('\r\n') + '\r\n'], { type: 'text/csv;charset=utf-8' }));
        a.download = cfg.name.replace(/[^A-Za-z0-9._-]+/g, '_') + '.csv';
        document.body.appendChild(a); a.click(); a.remove();
        setTimeout(function () { URL.revokeObjectURL(a.href); }, 5000);
    }

    function renderHistory() {
        var h = $('pgsql-history');
        h.replaceChildren();
        if (!history.length) { h.appendChild(el('div', 'list-group-item text-body-secondary', 'Nothing yet.')); return; }
        history.forEach(function (x) {
            var row = el('div', 'list-group-item');
            row.appendChild(el('div', 'font-monospace text-break', x.fingerprint));
            row.appendChild(el('div', 'text-body-secondary', x.created_at.replace('T', ' ').slice(0, 19) + ' · ' + x.mode + ' · ' + x.duration_ms + ' ms · ' + x.rows_returned + ' row(s) · ' + x.status));
            h.appendChild(row);
        });
    }
    function recents() { try { return JSON.parse(sessionStorage.getItem(recentKey) || '[]'); } catch (e) { return []; } }
    function remember(sql) {
        var list = recents().filter(function (x) { return x !== sql; });
        list.unshift(sql);
        try { sessionStorage.setItem(recentKey, JSON.stringify(list.slice(0, 15))); } catch (e) { /* a private window: fine */ }
        renderRecent();
    }
    function renderRecent() {
        var box = $('pgsql-recent'), list = recents();
        box.replaceChildren();
        if (!list.length) { box.appendChild(el('div', 'list-group-item text-body-secondary', 'Nothing yet.')); return; }
        list.forEach(function (q) {
            var b = el('button', 'list-group-item list-group-item-action font-monospace text-truncate', q.replace(/\s+/g, ' ').slice(0, 160));
            b.type = 'button'; b.title = 'Put it back in the editor';
            b.addEventListener('click', function () { editor.setText(q); editor.focus(); });
            box.appendChild(b);
        });
    }

    function busy(on) {
        root.querySelector('[data-pgsql="run"]').disabled = on;
        root.querySelector('[data-pgsql="explain"]').disabled = on;
        root.querySelector('[data-pgsql="analyze"]').disabled = on;
        root.querySelector('[data-pgsql="cancel"]').classList.toggle('d-none', !on);
    }

    function start(explain) {
        if (running) { return; }
        var sql = editor.getQuery();
        if (!sql.trim()) { setStatus('Write a statement first.', 'text-warning'); return; }
        var m = mode();
        if (m === 'write' && explain !== 'plan' && destructive(sql) && !window.confirm('This statement can change or delete data or structure in ' + cfg.name + '. Run it?')) { return; }
        if (explain === 'analyze' && !window.confirm('EXPLAIN ANALYZE really runs the statement' + (m === 'write' ? ', including any changes it makes' : '') + '. Continue?')) { return; }
        var id = hexId();
        running = id;
        busy(true); showError(null);
        setStatus('Running…');
        NimVor.fetchJson(cfg.run, { method: 'POST', body: JSON.stringify({
            db: cfg.db, query: sql, mode: m, explain: explain, script: $('pgsql-script').checked,
            limit: parseInt($('pgsql-limit').value, 10), timeout: parseInt($('pgsql-timeout').value, 10), query_id: id
        }) }).then(function (res) {
            var d = res.data, r = d.result || {};
            history = d.history || history; renderHistory(); remember(sql);
            if (r.error) {
                results = []; renderTabs(); renderGrid(); showError(r.error);
                setStatus('Failed after ' + (r.duration_ms || 0) + ' ms.', 'text-danger');
                if (r.error.position && !explain && editor.getQuery() === editor.getText()) {
                    try { var pos = Math.min(r.error.position - 1, editor.view.state.doc.length); editor.view.dispatch({ selection: { anchor: pos }, scrollIntoView: true }); } catch (e) { /* ignore */ }
                }
            } else {
                results = r.results || []; active = 0; page = 0; renderTabs(); renderGrid();
                var n = results.reduce(function (a, x) { return a + x.rows.length; }, 0);
                setStatus('Done in ' + (r.duration_ms || 0) + ' ms · ' + n + ' row(s) returned' + (results.some(function (x) { return x.rows_capped; }) ? ' · row limit reached' : '') + (r.mode === 'write' ? ' · read and write' : ''));
            }
        }).catch(function (err) {
            setStatus(err.message, 'text-danger');
        }).then(function () { running = null; busy(false); });
    }

    root.addEventListener('click', function (e) {
        var b = e.target.closest('[data-pgsql]');
        if (!b) { return; }
        switch (b.getAttribute('data-pgsql')) {
            case 'run': start(''); break;
            case 'explain': start('plan'); break;
            case 'analyze': start('analyze'); break;
            case 'format': if (!editor.format()) { setStatus('The statement could not be formatted (check its syntax).', 'text-warning'); } break;
            case 'csv': exportCsv(); break;
            case 'prev': if (page > 0) { page--; renderGrid(); } break;
            case 'next': if (results[active] && (page + 1) * PAGE < results[active].rows.length) { page++; renderGrid(); } break;
            case 'cancel':
                if (running) {
                    NimVor.fetchJson(cfg.cancel, { method: 'POST', body: JSON.stringify({ db: cfg.db, query_id: running }) }).then(function (res) { setStatus(res.data.cancelled ? 'Cancelling…' : 'It had already finished.'); }).catch(function (err) { setStatus(err.message, 'text-danger'); });
                }
                break;
        }
    });
    window.addEventListener('beforeunload', function (e) { if (running) { e.preventDefault(); e.returnValue = ''; } });
    renderHistory(); renderRecent();
})();
