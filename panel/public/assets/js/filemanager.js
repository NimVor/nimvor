// NimVor file manager (full page). Paths are relative to the website root;
// the agent enforces confinement. Names are only ever set as text, never
// as HTML.
(function () {
    'use strict';
    var root = document.getElementById('nim-fm');
    if (!root) { return; }
    var api = root.dataset.api, uploadUrl = root.dataset.upload, downloadUrl = root.dataset.download, editUrl = root.dataset.edit;
    var CHUNK = 1024 * 1024;
    var ARCHIVE = /\.(zip|tar|tar\.gz|tgz)$/i;
    var TEXT_HINT = /\.(php|html?|css|js|json|txt|md|xml|ya?ml|ini|conf|env|htaccess|sh|py|rb|ts|vue|twig|blade\.php|sql|csv|log)$/i;

    var state = {
        cwd: root.dataset.start || '', entries: [], selected: new Set(), anchor: null,
        sort: { key: 'name', dir: 1 }, hidden: false, back: [], forward: [], search: null
    };
    try { state.hidden = localStorage.getItem('nim-fm-hidden') === '1'; } catch (e) { /* storage unavailable */ }

    var $ = function (sel) { return root.querySelector(sel); };
    var rows = $('[data-fm-rows]'), tree = $('[data-fm-tree]'), pathInput = $('[data-fm-path]'), status = $('[data-fm-status]');
    var menu = $('[data-fm-menu]'), banner = $('[data-fm-banner]'), list = $('[data-fm-drop]');
    var hiddenToggle = document.querySelector('[data-fm-hidden]');
    if (hiddenToggle) { hiddenToggle.checked = state.hidden; }

    // ---- helpers ------------------------------------------------------
    function join(a, b) { return a ? a + '/' + b : b; }
    function parent(p) { var i = p.lastIndexOf('/'); return i < 0 ? '' : p.slice(0, i); }
    function base(p) { return p.split('/').pop(); }
    function fmtSize(n) { var u = ['B', 'KB', 'MB', 'GB', 'TB']; var i = 0; while (n >= 1024 && i < 4) { n /= 1024; i++; } return (i ? n.toFixed(1) : n) + ' ' + u[i]; }
    function el(tag, cls, text) { var e = document.createElement(tag); if (cls) { e.className = cls; } if (text !== undefined && text !== null) { e.textContent = text; } return e; }
    function icon(name) { return el('i', 'bi ' + name); }
    function typeOf(e) {
        if (e.type === 'dir') { return 'Folder'; }
        if (e.type === 'link') { return 'Link'; }
        var m = /\.([A-Za-z0-9]{1,8})$/.exec(e.name);
        return m ? m[1].toLowerCase() + ' file' : 'File';
    }
    function iconFor(e) {
        if (e.type === 'dir') { return 'bi-folder-fill text-warning'; }
        if (e.type === 'link') { return 'bi-link-45deg'; }
        var n = e.name.toLowerCase();
        if (ARCHIVE.test(n)) { return 'bi-file-zip'; }
        if (/\.(png|jpe?g|gif|svg|webp|ico)$/.test(n)) { return 'bi-file-image'; }
        if (/\.(php|js|ts|css|html?|py|rb|sh|json|xml|ya?ml)$/.test(n)) { return 'bi-file-code'; }
        if (/\.(txt|md|log|ini|conf|env)$/.test(n)) { return 'bi-file-text'; }
        return 'bi-file-earmark';
    }
    function call(op, params) {
        return NimVor.fetchJson(api, { method: 'POST', body: JSON.stringify(Object.assign({ op: op }, params || {})) })
            .then(function (res) { return res.data; });
    }
    function fail(err) {
        var d = err.details || {};
        NimVor.toast((d.message || err.message) + (d.reason ? ' — ' + d.reason : '') + (d.details ? ' — ' + d.details : ''), 'danger');
    }
    function selectedEntries() { return state.entries.filter(function (e) { return state.selected.has(e.path); }); }

    // ---- dialogs ------------------------------------------------------
    var dlgEl = document.getElementById('fm-dialog');
    function dialog(title, build, okLabel, danger) {
        return new Promise(function (resolve) {
            dlgEl.querySelector('.modal-title').textContent = title;
            var body = dlgEl.querySelector('[data-fm-dialog-body]');
            body.replaceChildren();
            var collect = build(body);
            var ok = dlgEl.querySelector('[data-fm-dialog-ok]');
            ok.textContent = okLabel || 'OK';
            ok.className = 'btn ' + (danger ? 'btn-danger' : 'btn-primary');
            var modal = bootstrap.Modal.getOrCreateInstance(dlgEl);
            var done = false;
            dlgEl.querySelector('form').onsubmit = function (ev) {
                ev.preventDefault();
                var v = collect();
                if (v === false) { return; }
                done = true; modal.hide(); resolve(v);
            };
            dlgEl.addEventListener('hidden.bs.modal', function () { if (!done) { resolve(null); } }, { once: true });
            dlgEl.addEventListener('shown.bs.modal', function () { var f = body.querySelector('input, select'); if (f) { f.focus(); if (f.select) { f.select(); } } }, { once: true });
            modal.show();
        });
    }
    function field(body, label, value, opts) {
        opts = opts || {};
        var id = 'fm-f-' + Math.random().toString(36).slice(2);
        var wrap = el('div', 'mb-3');
        var l = el('label', 'form-label small', label); l.htmlFor = id;
        var i = el('input', 'form-control' + (opts.mono ? ' font-monospace' : '')); i.id = id; i.value = value || ''; i.autocomplete = 'off'; i.spellcheck = false;
        wrap.appendChild(l); wrap.appendChild(i);
        if (opts.help) { wrap.appendChild(el('div', 'form-text', opts.help)); }
        body.appendChild(wrap);
        return i;
    }
    function check(body, label, checked) {
        var id = 'fm-c-' + Math.random().toString(36).slice(2);
        var wrap = el('div', 'form-check');
        var i = el('input', 'form-check-input'); i.type = 'checkbox'; i.id = id; i.checked = !!checked;
        var l = el('label', 'form-check-label small', label); l.htmlFor = id;
        wrap.appendChild(i); wrap.appendChild(l); body.appendChild(wrap);
        return i;
    }
    function nameList(body, names) {
        var ul = el('ul', 'small font-monospace mb-2 nim-fm-namelist');
        names.slice(0, 12).forEach(function (n) { ul.appendChild(el('li', '', n)); });
        if (names.length > 12) { ul.appendChild(el('li', 'text-body-secondary', '… and ' + (names.length - 12) + ' more')); }
        body.appendChild(ul);
    }
    function validName(n) { return n && n.indexOf('/') < 0 && n !== '.' && n !== '..'; }

    // ---- tree ---------------------------------------------------------
    function treeNode(path, name) {
        var li = el('li', 'nim-fm-node'); li.setAttribute('role', 'treeitem'); li.dataset.path = path; li.setAttribute('aria-expanded', 'false');
        var row = el('div', 'nim-fm-node-row');
        var tog = el('button', 'btn btn-link p-0 nim-fm-toggle'); tog.type = 'button'; tog.setAttribute('aria-label', 'Expand ' + name); tog.appendChild(icon('bi-caret-right-fill'));
        var link = el('button', 'btn btn-link p-0 nim-fm-node-link'); link.type = 'button'; link.dataset.go = path;
        link.appendChild(icon('bi-folder-fill text-warning')); link.appendChild(document.createTextNode(' ' + name));
        row.appendChild(tog); row.appendChild(link); li.appendChild(row);
        var ul = el('ul', 'nim-fm-tree-list'); ul.setAttribute('role', 'group'); li.appendChild(ul);
        return li;
    }
    function expand(li, open) {
        var ul = li.querySelector(':scope > ul');
        if (!open) { li.setAttribute('aria-expanded', 'false'); return Promise.resolve(); }
        li.setAttribute('aria-expanded', 'true');
        if (li.dataset.loaded) { return Promise.resolve(); }
        return call('list', { path: li.dataset.path }).then(function (entries) {
            li.dataset.loaded = '1';
            ul.replaceChildren();
            entries.filter(function (e) { return e.type === 'dir' && (state.hidden || e.name[0] !== '.'); })
                .forEach(function (e) { ul.appendChild(treeNode(join(li.dataset.path, e.name), e.name)); });
            if (!ul.children.length) { li.classList.add('nim-fm-leaf'); }
        });
    }
    function buildTree() {
        tree.replaceChildren();
        var rootNode = treeNode('', '/');
        rootNode.classList.add('nim-fm-root');
        tree.appendChild(rootNode);
        return syncTree();
    }
    function syncTree() {
        // Expand every folder on the way to cwd and highlight it.
        var parts = state.cwd ? state.cwd.split('/') : [];
        var acc = '';
        var chain = expand(tree.firstChild, true);
        parts.forEach(function (part) {
            acc = join(acc, part);
            var target = acc;
            chain = chain.then(function () {
                var li = tree.querySelector('li[data-path="' + CSS.escape(target) + '"]');
                return li ? expand(li, true) : null;
            });
        });
        return chain.then(function () {
            tree.querySelectorAll('.nim-fm-node-row.active').forEach(function (r) { r.classList.remove('active'); });
            var cur = tree.querySelector('li[data-path="' + CSS.escape(state.cwd) + '"] > .nim-fm-node-row');
            if (cur) { cur.classList.add('active'); cur.scrollIntoView({ block: 'nearest' }); }
        }).catch(function () { /* folder vanished; the listing reports it */ });
    }
    function refreshTreeAt(path) {
        var li = tree.querySelector('li[data-path="' + CSS.escape(path) + '"]');
        if (li) { delete li.dataset.loaded; li.classList.remove('nim-fm-leaf'); if (li.getAttribute('aria-expanded') === 'true') { expand(li, true); } }
    }

    // ---- listing ------------------------------------------------------
    function sortEntries(entries) {
        var k = state.sort.key, d = state.sort.dir;
        return entries.slice().sort(function (a, b) {
            if ((a.type === 'dir') !== (b.type === 'dir')) { return a.type === 'dir' ? -1 : 1; }
            var x = k === 'type' ? typeOf(a) : a[k], y = k === 'type' ? typeOf(b) : b[k];
            if (k === 'name' || k === 'type') { x = String(x).toLowerCase(); y = String(y).toLowerCase(); }
            return (x < y ? -1 : x > y ? 1 : 0) * d;
        });
    }
    function render() {
        rows.replaceChildren();
        var shown = sortEntries(state.entries.filter(function (e) { return state.hidden || e.name[0] !== '.' || state.search; }));
        if (!state.search && state.cwd) {
            var up = el('tr', 'nim-fm-row'); up.dataset.up = '1';
            up.appendChild(el('td'));
            var td = el('td', '', ''); td.colSpan = 5; td.appendChild(icon('bi-arrow-90deg-up me-2')); td.appendChild(document.createTextNode('Up one level'));
            up.appendChild(td); rows.appendChild(up);
        }
        if (!shown.length) {
            var tr = el('tr'); var t = el('td', 'text-body-secondary p-3', state.search ? 'Nothing matches.' : 'This folder is empty. Drop files here to upload them.'); t.colSpan = 6; tr.appendChild(t); rows.appendChild(tr);
        }
        shown.forEach(function (e) {
            var tr = el('tr', 'nim-fm-row'); tr.dataset.path = e.path; tr.draggable = false;
            if (state.selected.has(e.path)) { tr.classList.add('table-active'); }
            var c = el('td', 'nim-fm-check'); var cb = el('input', 'form-check-input'); cb.type = 'checkbox'; cb.checked = state.selected.has(e.path); cb.setAttribute('aria-label', 'Select ' + e.name); cb.dataset.fmSelect = '1';
            c.appendChild(cb); tr.appendChild(c);
            var n = el('td', 'nim-fm-name'); n.appendChild(icon(iconFor(e) + ' me-2'));
            n.appendChild(document.createTextNode(state.search ? e.path : e.name)); tr.appendChild(n);
            tr.appendChild(el('td', 'text-end small text-nowrap', e.type === 'dir' ? '' : fmtSize(e.size)));
            tr.appendChild(el('td', 'small text-nowrap text-body-secondary nim-fm-optional', e.mtime ? new Date(e.mtime).toLocaleString() : ''));
            tr.appendChild(el('td', 'small text-nowrap nim-fm-optional', typeOf(e)));
            tr.appendChild(el('td', 'small font-monospace nim-fm-optional', e.mode || ''));
            rows.appendChild(tr);
        });
        root.querySelectorAll('[data-fm-sort]').forEach(function (th) {
            th.classList.toggle('nim-fm-sorted', th.dataset.fmSort === state.sort.key);
            th.setAttribute('aria-sort', th.dataset.fmSort === state.sort.key ? (state.sort.dir > 0 ? 'ascending' : 'descending') : 'none');
        });
        updateStatus();
    }
    // Selection changes restyle rows in place (re-rendering would break
    // double-clicks and keyboard focus).
    function paintSelection() {
        rows.querySelectorAll('tr[data-path]').forEach(function (tr) {
            var on = state.selected.has(tr.dataset.path);
            tr.classList.toggle('table-active', on);
            var cb = tr.querySelector('[data-fm-select]'); if (cb) { cb.checked = on; }
        });
        updateStatus();
    }
    function updateStatus() {
        var sel = selectedEntries();
        var total = sel.reduce(function (s, e) { return s + (e.type === 'dir' ? 0 : e.size); }, 0);
        var dirs = state.entries.filter(function (e) { return e.type === 'dir'; }).length;
        status.textContent = (state.search ? 'Search results: ' : '') + dirs + ' folders, ' + (state.entries.length - dirs) + ' files' +
            (sel.length ? ' · ' + sel.length + ' selected (' + fmtSize(total) + ')' : '');
        var all = $('[data-fm-all]');
        all.checked = sel.length > 0 && sel.length === state.entries.length;
        all.indeterminate = sel.length > 0 && sel.length < state.entries.length;
        var single = sel.length === 1 ? sel[0] : null;
        root.querySelectorAll('[data-fm-needs]').forEach(function (b) {
            var need = b.dataset.fmNeeds, ok;
            if (need === '1+') { ok = sel.length > 0; }
            else if (need === '1') { ok = !!single; }
            else if (need === 'file') { ok = !!single && single.type === 'file'; }
            else if (need === 'archive') { ok = !!single && single.type === 'file' && ARCHIVE.test(single.name); }
            b.disabled = !ok;
        });
        root.querySelector('[data-fm-cmd="back"]').disabled = !state.back.length;
        root.querySelector('[data-fm-cmd="forward"]').disabled = !state.forward.length;
        root.querySelector('[data-fm-cmd="up"]').disabled = !state.cwd;
    }
    function load(keepSelection) {
        state.search = null;
        banner.classList.add('d-none');
        pathInput.value = state.cwd;
        rows.replaceChildren(); var tr = el('tr'); var t = el('td', 'text-body-secondary p-3', 'Loading…'); t.colSpan = 6; tr.appendChild(t); rows.appendChild(tr);
        return call('list', { path: state.cwd }).then(function (entries) {
            state.entries = entries.map(function (e) { e.path = join(state.cwd, e.name); return e; });
            if (!keepSelection) { state.selected.clear(); }
            else { state.selected.forEach(function (p) { if (!state.entries.some(function (e) { return e.path === p; })) { state.selected.delete(p); } }); }
            render();
            syncTree();
            try { history.replaceState(null, '', '?path=' + encodeURIComponent(state.cwd)); } catch (e) { /* ignore */ }
        }).catch(function (err) {
            fail(err);
            rows.replaceChildren();
            var r = el('tr'); var c = el('td', 'text-danger p-3', 'This folder could not be opened.'); c.colSpan = 6; r.appendChild(c); rows.appendChild(r);
        });
    }
    function go(path, fromHistory) {
        if (!fromHistory && path !== state.cwd) { state.back.push(state.cwd); state.forward = []; }
        state.cwd = path;
        return load(false);
    }

    // ---- selection ----------------------------------------------------
    function visiblePaths() { return Array.prototype.map.call(rows.querySelectorAll('tr[data-path]'), function (r) { return r.dataset.path; }); }
    function select(path, ev) {
        if (ev && ev.shiftKey && state.anchor) {
            var order = visiblePaths(), a = order.indexOf(state.anchor), b = order.indexOf(path);
            if (a >= 0 && b >= 0) {
                if (!(ev.ctrlKey || ev.metaKey)) { state.selected.clear(); }
                order.slice(Math.min(a, b), Math.max(a, b) + 1).forEach(function (p) { state.selected.add(p); });
            }
        } else if (ev && (ev.ctrlKey || ev.metaKey || ev.fmToggle)) {
            if (state.selected.has(path)) { state.selected.delete(path); } else { state.selected.add(path); }
            state.anchor = path;
        } else {
            state.selected.clear(); state.selected.add(path); state.anchor = path;
        }
        paintSelection();
    }
    function open(entry) {
        if (!entry) { return; }
        if (entry.type === 'dir') { go(entry.path); return; }
        if (entry.type === 'link') { NimVor.toast('Links are not followed by the file manager.', 'secondary'); return; }
        if (TEXT_HINT.test(entry.name) || entry.size < 512 * 1024) { window.location.href = editUrl + '?path=' + encodeURIComponent(entry.path); return; }
        window.location.href = downloadUrl + '?path=' + encodeURIComponent(entry.path);
    }

    rows.addEventListener('click', function (e) {
        var tr = e.target.closest('tr');
        if (!tr) { return; }
        if (tr.dataset.up) { go(parent(state.cwd)); return; }
        if (!tr.dataset.path) { return; }
        if (e.target.dataset.fmSelect) { select(tr.dataset.path, { fmToggle: true }); return; }
        select(tr.dataset.path, e);
    });
    rows.addEventListener('dblclick', function (e) {
        var tr = e.target.closest('tr[data-path]');
        if (!tr) { return; }
        var entry = state.entries.find(function (x) { return x.path === tr.dataset.path; });
        if (state.search && entry && entry.type !== 'dir') { state.cwd = parent(entry.path); }
        open(entry);
    });
    $('[data-fm-all]').addEventListener('change', function (e) {
        state.selected.clear();
        if (e.target.checked) { state.entries.forEach(function (x) { state.selected.add(x.path); }); }
        paintSelection();
    });
    root.querySelectorAll('[data-fm-sort]').forEach(function (th) {
        th.tabIndex = 0;
        var sort = function () { state.sort.dir = state.sort.key === th.dataset.fmSort ? -state.sort.dir : 1; state.sort.key = th.dataset.fmSort; render(); };
        th.addEventListener('click', sort);
        th.addEventListener('keydown', function (e) { if (e.key === 'Enter') { sort(); } });
    });

    // ---- context menu -------------------------------------------------
    var MENU = [
        ['open', 'bi-box-arrow-in-right', 'Open'], ['edit', 'bi-pencil-square', 'Edit'], ['download', 'bi-download', 'Download'], null,
        ['copy', 'bi-files', 'Copy…'], ['move', 'bi-arrows-move', 'Move…'], ['rename', 'bi-input-cursor-text', 'Rename…'], ['perms', 'bi-shield-lock', 'Permissions…'], null,
        ['compress', 'bi-file-zip', 'Compress…'], ['extract', 'bi-box-arrow-up', 'Extract…'], null, ['delete', 'bi-trash', 'Delete…']
    ];
    function showMenu(x, y) {
        var sel = selectedEntries(), single = sel.length === 1 ? sel[0] : null;
        var allowed = {
            open: !!single && single.type !== 'link', edit: !!single && single.type === 'file', download: !!single && single.type === 'file',
            copy: sel.length > 0, move: sel.length > 0, rename: !!single, perms: sel.length > 0, compress: sel.length > 0,
            extract: !!single && single.type === 'file' && ARCHIVE.test(single.name), delete: sel.length > 0
        };
        menu.replaceChildren();
        MENU.forEach(function (m) {
            if (m === null) { var d = el('li'); d.appendChild(el('hr', 'dropdown-divider')); menu.appendChild(d); return; }
            if (!allowed[m[0]]) { return; }
            var li = el('li'); var b = el('button', 'dropdown-item' + (m[0] === 'delete' ? ' text-danger' : '')); b.type = 'button'; b.dataset.fmCmd = m[0]; b.setAttribute('role', 'menuitem');
            b.appendChild(icon(m[1] + ' me-2')); b.appendChild(document.createTextNode(m[2])); li.appendChild(b); menu.appendChild(li);
        });
        menu.classList.add('show');
        var r = menu.getBoundingClientRect();
        menu.style.left = Math.min(x, window.innerWidth - r.width - 8) + 'px';
        menu.style.top = Math.min(y, window.innerHeight - r.height - 8) + 'px';
        var first = menu.querySelector('button'); if (first) { first.focus(); }
    }
    function hideMenu() { menu.classList.remove('show'); }
    rows.addEventListener('contextmenu', function (e) {
        var tr = e.target.closest('tr[data-path]');
        if (!tr) { return; }
        e.preventDefault();
        if (!state.selected.has(tr.dataset.path)) { select(tr.dataset.path); }
        showMenu(e.clientX, e.clientY);
    });
    document.addEventListener('click', function (e) { if (!menu.contains(e.target)) { hideMenu(); } });
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape') { hideMenu(); } });

    // ---- commands -----------------------------------------------------
    function names(sel) { return sel.map(function (e) { return e.name; }); }
    // ---- trash --------------------------------------------------------
    var trashEl = document.getElementById('fm-trash');
    var trashRows = trashEl ? trashEl.querySelector('[data-fm-trash-rows]') : null;
    var trashItems = [], trashSel = new Set();
    function trashButtons() {
        trashEl.querySelector('[data-fm-trash-restore]').disabled = trashSel.size === 0;
        trashEl.querySelector('[data-fm-trash-delete]').disabled = trashSel.size === 0;
        trashEl.querySelector('[data-fm-trash-empty]').disabled = trashItems.length === 0;
        trashEl.querySelector('[data-fm-trash-all]').checked = trashItems.length > 0 && trashSel.size === trashItems.length;
    }
    function renderTrash() {
        trashRows.replaceChildren();
        if (!trashItems.length) {
            var tr = el('tr'); var td = el('td', 'text-center text-body-secondary py-4', 'The trash is empty.'); td.colSpan = 5; tr.appendChild(td); trashRows.appendChild(tr);
        }
        trashItems.forEach(function (it) {
            var tr = el('tr'); tr.dataset.id = it.id;
            var c = el('td', 'nim-fm-check'); var cb = el('input', 'form-check-input'); cb.type = 'checkbox'; cb.checked = trashSel.has(it.id); cb.setAttribute('aria-label', 'Select ' + base(it.path));
            cb.addEventListener('change', function () { if (cb.checked) { trashSel.add(it.id); } else { trashSel.delete(it.id); } trashButtons(); });
            c.appendChild(cb); tr.appendChild(c);
            var n = el('td', 'text-break'); n.appendChild(icon(it.type === 'dir' ? 'bi-folder-fill text-warning me-1' : 'bi-file-earmark me-1')); n.appendChild(document.createTextNode(base(it.path))); tr.appendChild(n);
            tr.appendChild(el('td', 'small font-monospace text-break', '/' + parent(it.path)));
            tr.appendChild(el('td', 'small text-end text-nowrap', fmtSize(it.size || 0)));
            tr.appendChild(el('td', 'small text-nowrap', new Date(it.deleted_at).toLocaleString()));
            trashRows.appendChild(tr);
        });
        trashButtons();
    }
    function openTrash() {
        if (!trashEl) { return; }
        trashSel.clear();
        call('trash_list', {}).then(function (r) {
            trashItems = r.items || []; renderTrash();
            if (trashEl.classList.contains('show')) {
                // Refreshed in place: a button that became disabled lost focus (Escape needs it inside).
                if (!trashEl.contains(document.activeElement) || document.activeElement.disabled) { trashEl.focus(); }
            } else {
                bootstrap.Modal.getOrCreateInstance(trashEl).show();
            }
        }).catch(fail);
    }
    // Confirmations use the file manager's own window: the trash window
    // closes for it and opens again afterwards.
    function trashConfirm(title, text, list, okLabel, danger) {
        return new Promise(function (resolve) {
            trashEl.addEventListener('hidden.bs.modal', function () {
                dialog(title, function (b) { b.appendChild(el('p', 'small', text)); if (list.length) { nameList(b, list); } return function () { return true; }; }, okLabel, danger).then(resolve);
            }, { once: true });
            bootstrap.Modal.getOrCreateInstance(trashEl).hide();
        });
    }
    function selectedTrash() { return trashItems.filter(function (it) { return trashSel.has(it.id); }); }
    if (trashEl) {
        trashEl.querySelector('[data-fm-trash-all]').addEventListener('change', function (e) {
            trashSel.clear(); if (e.target.checked) { trashItems.forEach(function (it) { trashSel.add(it.id); }); } renderTrash();
        });
        trashEl.querySelector('[data-fm-trash-restore]').addEventListener('click', function () {
            var ids = Array.from(trashSel); if (!ids.length) { return; }
            call('trash_restore', { ids: ids }).then(function (r) {
                var done = r.restored || [];
                NimVor.toast('Restored ' + done.length + ' item(s)' + (done.length === 1 ? ' to /' + done[0].path : '') + '.', 'success');
                refreshTreeAt(''); load(); openTrash();
            }).catch(function (e) { fail(e); openTrash(); });
        });
        trashEl.querySelector('[data-fm-trash-delete]').addEventListener('click', function () {
            var sel = selectedTrash(); if (!sel.length) { return; }
            trashConfirm('Delete permanently', 'These items will be deleted for good. This cannot be undone:', sel.map(function (it) { return it.path; }), 'Delete permanently', true).then(function (ok) {
                if (!ok) { openTrash(); return; }
                call('trash_delete', { ids: sel.map(function (it) { return it.id; }) }).then(function (r) { NimVor.toast('Deleted ' + r.deleted + ' item(s) permanently.', 'success'); openTrash(); }).catch(function (e) { fail(e); openTrash(); });
            });
        });
        trashEl.querySelector('[data-fm-trash-empty]').addEventListener('click', function () {
            trashConfirm('Empty trash', 'All ' + trashItems.length + ' item(s) in the trash will be deleted for good. This cannot be undone.', [], 'Empty trash', true).then(function (ok) {
                if (!ok) { openTrash(); return; }
                call('trash_empty', {}).then(function (r) { NimVor.toast('Deleted ' + r.deleted + ' item(s) permanently.', 'success'); openTrash(); }).catch(function (e) { fail(e); openTrash(); });
            });
        });
    }

    var commands = {
        home: function () { go(''); },
        up: function () { if (state.cwd) { go(parent(state.cwd)); } },
        back: function () { if (state.back.length) { state.forward.push(state.cwd); state.cwd = state.back.pop(); load(); } },
        forward: function () { if (state.forward.length) { state.back.push(state.cwd); state.cwd = state.forward.pop(); load(); } },
        reload: function () { refreshTreeAt(state.cwd); load(true); },
        collapse: function () { buildTree(); },
        selectall: function () { state.entries.forEach(function (x) { state.selected.add(x.path); }); paintSelection(); },
        selectnone: function () { state.selected.clear(); paintSelection(); },
        clearuploads: function () { $('[data-fm-upload-list]').querySelectorAll('li.done').forEach(function (li) { li.remove(); }); if (!$('[data-fm-upload-list]').children.length) { $('[data-fm-uploads]').classList.add('d-none'); } },
        open: function () { open(selectedEntries()[0]); },
        edit: function () { var s = selectedEntries()[0]; if (s) { window.location.href = editUrl + '?path=' + encodeURIComponent(s.path); } },
        download: function () { var s = selectedEntries()[0]; if (s) { window.location.href = downloadUrl + '?path=' + encodeURIComponent(s.path); } },
        upload: function () { $('[data-fm-upload-input]').click(); },
        newfile: function () {
            dialog('New file', function (b) { var i = field(b, 'File name in /' + state.cwd, '', { mono: true }); return function () { return validName(i.value.trim()) ? i.value.trim() : (i.classList.add('is-invalid'), false); }; }, 'Create')
                .then(function (n) { if (n) { var p = join(state.cwd, n); call('write', { path: p, content: '' }).then(function () { window.location.href = editUrl + '?path=' + encodeURIComponent(p); }).catch(fail); } });
        },
        newfolder: function () {
            dialog('New folder', function (b) { var i = field(b, 'Folder name in /' + state.cwd, '', { mono: true }); return function () { return validName(i.value.trim()) ? i.value.trim() : (i.classList.add('is-invalid'), false); }; }, 'Create')
                .then(function (n) { if (n) { call('mkdir', { path: join(state.cwd, n) }).then(function () { refreshTreeAt(state.cwd); load(); }).catch(fail); } });
        },
        rename: function () {
            var s = selectedEntries()[0]; if (!s) { return; }
            dialog('Rename', function (b) { var i = field(b, 'New name', s.name, { mono: true }); return function () { return validName(i.value.trim()) ? i.value.trim() : (i.classList.add('is-invalid'), false); }; }, 'Rename')
                .then(function (n) { if (n && n !== s.name) { call('rename', { from: s.path, to: join(parent(s.path), n) }).then(function () { refreshTreeAt(parent(s.path)); load(); }).catch(fail); } });
        },
        copy: function () { transfer('copy'); },
        move: function () { transfer('move'); },
        delete: function () {
            var sel = selectedEntries(); if (!sel.length) { return; }
            var perm;
            dialog('Delete ' + sel.length + ' item' + (sel.length > 1 ? 's' : ''), function (b) {
                b.appendChild(el('p', 'small', 'These will be moved to the Trash, including everything inside folders. You can restore them from there:'));
                nameList(b, names(sel));
                perm = check(b, 'Delete permanently instead (cannot be undone)', false);
                return function () { return { permanent: perm.checked }; };
            }, 'Delete', true).then(function (v) {
                if (!v) { return; }
                call('delete', { paths: sel.map(function (e) { return e.path; }), permanent: v.permanent }).then(function (r) {
                    NimVor.toast(r.trashed ? 'Moved ' + r.deleted + ' item(s) to the Trash.' : 'Deleted ' + r.deleted + ' item(s) permanently.', 'success'); refreshTreeAt(state.cwd); load();
                }).catch(fail);
            });
        },
        trash: function () { openTrash(); },
        perms: function () {
            var sel = selectedEntries(); if (!sel.length) { return; }
            var start = sel[0].mode ? sel[0].mode.slice(-3) : '644';
            dialog('Permissions', function (b) {
                if (sel.length > 1) { nameList(b, names(sel)); }
                var grid = el('table', 'table table-sm text-center mb-2 nim-fm-perms');
                var head = el('tr'); ['', 'Read', 'Write', 'Execute'].forEach(function (h) { head.appendChild(el('th', 'small', h)); }); grid.appendChild(head);
                var boxes = [];
                ['Owner', 'Group', 'Everyone'].forEach(function (who, r) {
                    var tr = el('tr'); tr.appendChild(el('th', 'small text-start', who));
                    [4, 2, 1].forEach(function (bit) {
                        var td = el('td'); var cb = el('input', 'form-check-input'); cb.type = 'checkbox'; cb.dataset.row = r; cb.dataset.bit = bit; cb.setAttribute('aria-label', who + ' ' + ({ 4: 'read', 2: 'write', 1: 'execute' })[bit]);
                        td.appendChild(cb); tr.appendChild(td); boxes.push(cb);
                    });
                    grid.appendChild(tr);
                });
                b.appendChild(grid);
                var octal = field(b, 'Octal', start, { mono: true, help: 'Files usually 644, folders 755. Special bits (setuid, sticky) cannot be set.' });
                function fromOctal() {
                    if (!/^[0-7]{3}$/.test(octal.value)) { return; }
                    boxes.forEach(function (cb) { cb.checked = (parseInt(octal.value[cb.dataset.row], 8) & cb.dataset.bit) !== 0; });
                }
                function toOctal() {
                    var d = [0, 0, 0]; boxes.forEach(function (cb) { if (cb.checked) { d[cb.dataset.row] += +cb.dataset.bit; } });
                    octal.value = d.join('');
                }
                boxes.forEach(function (cb) { cb.addEventListener('change', toOctal); });
                octal.addEventListener('input', fromOctal);
                fromOctal();
                return function () { return /^[0-7]{3}$/.test(octal.value) ? octal.value : (octal.classList.add('is-invalid'), false); };
            }, 'Change').then(function (mode) {
                if (!mode) { return; }
                sequential(sel, function (e) { return call('chmod', { path: e.path, mode: mode }); }).then(function () { NimVor.toast('Permissions changed.', 'success'); load(true); }).catch(fail);
            });
        },
        compress: function () {
            var sel = selectedEntries(); if (!sel.length) { return; }
            var suggested = (sel.length === 1 ? sel[0].name : 'archive') + '.zip';
            dialog('Compress', function (b) {
                nameList(b, names(sel));
                var i = field(b, 'Archive name (in /' + state.cwd + ')', suggested, { mono: true, help: 'Ends in .zip or .tar.gz.' });
                return function () { var v = i.value.trim(); return validName(v) && /\.(zip|tar\.gz)$/i.test(v) ? v : (i.classList.add('is-invalid'), false); };
            }, 'Compress').then(function (name) {
                if (!name) { return; }
                call('compress', { paths: sel.map(function (e) { return e.path; }), dest: join(state.cwd, name) }).then(function (r) { watchJob(r.job_id, 'Compressing'); }).catch(fail);
            });
        },
        extract: function () {
            var s = selectedEntries()[0]; if (!s) { return; }
            var dest = state.cwd, over;
            dialog('Extract ' + s.name, function (b) {
                var i = field(b, 'Extract into folder', dest, { mono: true, help: 'Relative to the website root; empty means the root. The folder must exist.' });
                over = check(b, 'Replace files that already exist', false);
                b.appendChild(el('div', 'form-text', 'Links, device files and entries that would leave the folder are skipped.'));
                return function () { return { dest: i.value.trim().replace(/^\/+|\/+$/g, '') }; };
            }, 'Extract').then(function (v) {
                if (!v) { return; }
                call('extract', { path: s.path, dest: v.dest, overwrite: over.checked }).then(function (r) { watchJob(r.job_id, 'Extracting'); }).catch(fail);
            });
        }
    };
    function sequential(items, fn) {
        return items.reduce(function (p, it) { return p.then(function () { return fn(it); }); }, Promise.resolve());
    }
    function transfer(kind) {
        var sel = selectedEntries(); if (!sel.length) { return; }
        dialog((kind === 'copy' ? 'Copy ' : 'Move ') + sel.length + ' item' + (sel.length > 1 ? 's' : ''), function (b) {
            nameList(b, names(sel));
            var i = field(b, 'To folder (relative to the website root)', state.cwd, { mono: true, help: 'The folder must exist. Empty means the website root.' });
            return function () { return { to: i.value.trim().replace(/^\/+|\/+$/g, '') }; };
        }, kind === 'copy' ? 'Copy' : 'Move').then(function (v) {
            if (!v) { return; }
            sequential(sel, function (e) {
                var to = join(v.to, e.name);
                if (to === e.path) { return kind === 'copy' ? call('copy', { from: e.path, to: e.path + '-copy' }) : null; }
                return call(kind === 'copy' ? 'copy' : 'rename', { from: e.path, to: to });
            }).then(function () {
                NimVor.toast(kind === 'copy' ? 'Copied.' : 'Moved.', 'success');
                refreshTreeAt(state.cwd); refreshTreeAt(v.to); load();
            }).catch(function (err) { fail(err); load(); });
        });
    }
    function watchJob(jobId, label) {
        status.textContent = label + '…';
        (function poll(offset) {
            NimVor.fetchJson('/jobs/' + jobId + '?offset=' + offset).then(function (res) {
                if (!res.done) { setTimeout(function () { poll(res.offset || offset); }, 1000); return; }
                if (res.job.status === 'succeeded') { NimVor.toast(label + ' finished.', 'success'); }
                else { NimVor.toast(label + ' failed: ' + (res.job.error || 'see the log'), 'danger'); NimVor.showJob(jobId); }
                refreshTreeAt(state.cwd); load(true);
            }).catch(function () { setTimeout(function () { poll(offset); }, 3000); });
        })(0);
    }

    root.addEventListener('click', function (e) {
        var go2 = e.target.closest('[data-go]');
        if (go2) { go(go2.dataset.go); return; }
        var tog = e.target.closest('.nim-fm-toggle');
        if (tog) { var li = tog.closest('li'); expand(li, li.getAttribute('aria-expanded') !== 'true'); return; }
        var c = e.target.closest('[data-fm-cmd]');
        if (c && !c.disabled && commands[c.dataset.fmCmd]) { hideMenu(); commands[c.dataset.fmCmd](); }
    });
    $('[data-fm-path-form]').addEventListener('submit', function (e) { e.preventDefault(); go(pathInput.value.trim().replace(/^\/+|\/+$/g, '')); });
    $('[data-fm-search-form]').addEventListener('submit', function (e) {
        e.preventDefault();
        var q = $('[data-fm-search]').value.trim();
        if (!q) { load(); return; }
        call('search', { path: state.cwd, query: q }).then(function (res) {
            state.search = q;
            state.selected.clear();
            state.entries = res.results.map(function (h) { return { name: base(h.path), path: h.path, type: h.type, size: h.size, mode: '', mtime: null }; });
            banner.replaceChildren();
            banner.appendChild(document.createTextNode('Search results for “' + q + '” in /' + state.cwd + (res.truncated ? ' (first results only)' : '') + ' '));
            var back = el('button', 'btn btn-sm btn-link p-0', 'Back to folder'); back.type = 'button'; back.dataset.fmCmd = 'reload';
            banner.appendChild(back);
            banner.classList.remove('d-none');
            render();
        }).catch(fail);
    });
    if (hiddenToggle) {
        hiddenToggle.addEventListener('change', function () {
            state.hidden = hiddenToggle.checked;
            try { localStorage.setItem('nim-fm-hidden', state.hidden ? '1' : '0'); } catch (e) { /* ignore */ }
            buildTree(); render();
        });
    }
    list.addEventListener('keydown', function (e) {
        if (e.target.matches('input')) { return; }
        var sel = selectedEntries();
        if (e.key === 'Delete' && sel.length) { e.preventDefault(); commands.delete(); }
        else if (e.key === 'F2' && sel.length === 1) { e.preventDefault(); commands.rename(); }
        else if (e.key === 'Enter' && sel.length === 1) { e.preventDefault(); open(sel[0]); }
        else if (e.key === 'Backspace') { e.preventDefault(); commands.up(); }
        else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'a') { e.preventDefault(); commands.selectall(); }
        else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
            e.preventDefault();
            var order = visiblePaths(); if (!order.length) { return; }
            var i = order.indexOf(state.anchor);
            i = e.key === 'ArrowDown' ? Math.min(order.length - 1, i + 1) : Math.max(0, i - 1);
            select(order[i], e.shiftKey ? { shiftKey: true } : null);
            var row = rows.querySelector('tr[data-path="' + CSS.escape(order[i]) + '"]'); if (row) { row.scrollIntoView({ block: 'nearest' }); }
        }
    });

    // ---- uploads ------------------------------------------------------
    var queue = [], busy = false;
    function enqueue(file, relPath) {
        var li = el('li', 'nim-fm-upload');
        var name = el('span', 'font-monospace text-truncate', relPath);
        var bar = el('div', 'progress flex-grow-1'); var fill = el('div', 'progress-bar'); bar.appendChild(fill);
        var pct = el('span', 'text-body-secondary', 'waiting');
        li.appendChild(name); li.appendChild(bar); li.appendChild(pct);
        $('[data-fm-upload-list]').appendChild(li);
        $('[data-fm-uploads]').classList.remove('d-none');
        queue.push({ file: file, path: join(state.cwd, relPath), li: li, fill: fill, pct: pct });
        pump();
    }
    function pump() {
        if (busy || !queue.length) { return; }
        busy = true;
        var job = queue.shift();
        var f = job.file;
        function chunk(offset) {
            var blob = f.slice(offset, offset + CHUNK);
            var fd = new FormData();
            fd.append('chunk', blob, 'chunk'); fd.append('path', job.path); fd.append('offset', String(offset));
            fd.append('final', offset + CHUNK >= f.size ? '1' : '0');
            return fetch(uploadUrl, { method: 'POST', body: fd, credentials: 'same-origin', headers: { 'X-CSRF-Token': NimVor.csrfToken(), 'Accept': 'application/json' } })
                .then(function (r) { return r.json(); })
                .then(function (res) {
                    if (!res.success) { throw new Error((res.error && res.error.message) || 'Upload failed'); }
                    var done = Math.min(f.size, offset + CHUNK);
                    var p = f.size ? Math.round(done / f.size * 100) : 100;
                    job.fill.style.width = p + '%'; job.pct.textContent = p + '%';
                    return offset + CHUNK < f.size ? chunk(offset + CHUNK) : null;
                });
        }
        job.pct.textContent = '0%';
        chunk(0).then(function () {
            job.li.classList.add('done'); job.pct.textContent = 'done'; job.fill.classList.add('bg-success');
        }).catch(function (err) {
            job.li.classList.add('done'); job.pct.textContent = err.message; job.pct.classList.add('text-danger'); job.fill.classList.add('bg-danger');
        }).then(function () {
            busy = false;
            if (!queue.length) { load(true); refreshTreeAt(state.cwd); }
            pump();
        });
    }
    function mkdirs(rel) {
        // Create folders for dropped directory trees (ignore "already exists").
        var parts = rel.split('/'), acc = '', p = Promise.resolve();
        parts.forEach(function (part) {
            acc = join(acc, part);
            var target = join(state.cwd, acc);
            p = p.then(function () { return call('mkdir', { path: target }).catch(function () { return null; }); });
        });
        return p;
    }
    function uploadList(files) {
        var existing = new Set(state.entries.map(function (e) { return e.name; }));
        var clash = files.filter(function (f) { return existing.has(f.name); });
        var go2 = clash.length ? dialog('Replace files?', function (b) {
            b.appendChild(el('p', 'small', 'These files already exist in this folder and will be replaced:'));
            nameList(b, clash.map(function (f) { return f.name; }));
            return function () { return true; };
        }, 'Replace', true) : Promise.resolve(true);
        go2.then(function (ok) { if (ok) { files.forEach(function (f) { enqueue(f, f.name); }); } });
    }
    $('[data-fm-upload-input]').addEventListener('change', function (e) {
        var files = Array.prototype.slice.call(e.target.files);
        e.target.value = '';
        uploadList(files);
    });
    function walkEntry(entry, prefix) {
        return new Promise(function (resolve) {
            if (entry.isFile) {
                entry.file(function (f) { enqueue(f, join(prefix, f.name)); resolve(); }, function () { resolve(); });
            } else if (entry.isDirectory) {
                var dir = join(prefix, entry.name);
                mkdirs(dir).then(function () {
                    var reader = entry.createReader(), all = [];
                    (function more() {
                        reader.readEntries(function (batch) {
                            if (!batch.length) { all.reduce(function (p, en) { return p.then(function () { return walkEntry(en, dir); }); }, Promise.resolve()).then(resolve); return; }
                            all = all.concat(Array.prototype.slice.call(batch)); more();
                        }, function () { resolve(); });
                    })();
                });
            } else { resolve(); }
        });
    }
    ['dragenter', 'dragover'].forEach(function (t) {
        list.addEventListener(t, function (e) { if (e.dataTransfer && Array.prototype.indexOf.call(e.dataTransfer.types, 'Files') >= 0) { e.preventDefault(); list.classList.add('nim-fm-dragging'); } });
    });
    ['dragleave', 'drop'].forEach(function (t) {
        list.addEventListener(t, function (e) { if (t === 'dragleave' && list.contains(e.relatedTarget)) { return; } list.classList.remove('nim-fm-dragging'); });
    });
    list.addEventListener('drop', function (e) {
        e.preventDefault();
        var items = e.dataTransfer.items;
        if (items && items.length && items[0].webkitGetAsEntry) {
            var entries = Array.prototype.map.call(items, function (it) { return it.webkitGetAsEntry(); }).filter(Boolean);
            var plainFiles = entries.filter(function (en) { return en.isFile; }).length === entries.length;
            if (plainFiles) { uploadList(Array.prototype.slice.call(e.dataTransfer.files)); return; }
            entries.reduce(function (p, en) { return p.then(function () { return walkEntry(en, ''); }); }, Promise.resolve());
            return;
        }
        uploadList(Array.prototype.slice.call(e.dataTransfer.files));
    });

    // ---- start ----------------------------------------------------------
    var fromUrl = new URLSearchParams(window.location.search).get('path');
    if (fromUrl !== null) { state.cwd = fromUrl.replace(/^\/+|\/+$/g, ''); }
    buildTree();
    load();
    if (list) { list.focus(); }
})();
