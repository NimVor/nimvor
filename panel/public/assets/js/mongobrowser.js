/* MongoDB database browser (Server → MongoDB → Browser).
 *
 * Drives the full-page three-pane #wcp-mongo tool: a databases/collections
 * sidebar, a centre pane with the query bar, view tabs and a documents table
 * (or JSON view) with paging, and a right rail showing the selected document,
 * the collection's indexes, its statistics and quick actions. Loaded from
 * 'self' (the panel CSP forbids inline scripts); all calls go to the
 * mongo.browse endpoint through NimVor.fetchJson, which attaches the CSRF token
 * on POST. */
(function () {
    'use strict';
    var root = document.getElementById('wcp-mongo');
    if (!root || !window.NimVor) { return; }

    var api = root.getAttribute('data-api');
    var exportApi = root.getAttribute('data-export');
    var canDelete = root.getAttribute('data-can-delete') === '1';

    var dbSel = root.querySelector('[data-mb-db]');
    var collList = root.querySelector('[data-mb-colls]');
    var collFilter = root.querySelector('[data-mb-coll-filter]');
    var newCollBtn = root.querySelector('[data-mb-newcoll]');
    var collCount = root.querySelector('[data-mb-colls-count]');
    var bcDb = root.querySelector('[data-mb-bc-db]');
    var bcColl = root.querySelector('[data-mb-bc-coll]');
    var titleEl = root.querySelector('[data-mb-title]');
    var kindBadge = root.querySelector('[data-mb-kind]');
    var moreBtn = root.querySelector('[data-mb-more]');
    var filterIn = root.querySelector('[data-mb-filter]');
    var sortIn = root.querySelector('[data-mb-sort]');
    var projIn = root.querySelector('[data-mb-projection]');
    var runBtn = root.querySelector('[data-mb-run]');
    var pageSizeSel = root.querySelector('[data-mb-pagesize]');
    var tabBtns = root.querySelectorAll('[data-mb-tab]');
    var toolbar = root.querySelector('[data-mb-toolbar]');
    var docFilters = root.querySelector('[data-mb-docfilters]');
    var countEl = root.querySelector('[data-mb-count]');
    var viewToggle = root.querySelector('[data-mb-viewtoggle]');
    var pane = root.querySelector('[data-mb-pane]');
    var pagerEl = root.querySelector('[data-mb-pager]');
    var pagesEl = root.querySelector('[data-mb-pages]');
    var rangeEl = root.querySelector('[data-mb-range]');
    var docJson = root.querySelector('[data-mb-doc-json]');
    var docCopy = root.querySelector('[data-mb-doc-copy]');
    var railIndexes = root.querySelector('[data-mb-rail-indexes]');
    var railIndexesAll = root.querySelector('[data-mb-rail-indexes-all]');
    var railStats = root.querySelector('[data-mb-rail-stats]');
    var qaBtns = root.querySelectorAll('[data-mb-qa]');

    var state = { db: '', collection: '', skip: 0, total: 0, limit: 25, tab: 'documents', view: 'table', docs: [], selected: -1, stats: null };

    function el(tag, cls, text) {
        var e = document.createElement(tag);
        if (cls) { e.className = cls; }
        if (text !== undefined && text !== null) { e.textContent = text; }
        return e;
    }
    function call(op, params) {
        return NimVor.fetchJson(api, { method: 'POST', body: JSON.stringify(Object.assign({ op: op, db: state.db }, params || {})) })
            .then(function (r) { return r.data; });
    }
    function fail(err) {
        var d = (err && err.details) || {};
        NimVor.toast((d.message || (err && err.message) || 'Request failed') + (d.details ? ' — ' + d.details : ''), 'danger');
    }
    function emptyState(target, icon, title, hint) {
        var box = el('div', 'wcp-db-empty');
        box.appendChild(el('i', 'bi bi-' + icon));
        box.appendChild(el('div', 'fw-semibold', title));
        if (hint) { box.appendChild(el('div', 'small text-body-secondary', hint)); }
        target.replaceChildren(box);
    }
    function placeholder(target, text) { target.replaceChildren(el('div', 'text-body-secondary p-4', text)); }

    function fmtInt(n) { return (Number(n) || 0).toLocaleString(); }
    function fmtBytes(n) {
        n = Number(n) || 0;
        var u = ['B', 'KB', 'MB', 'GB', 'TB'], i = 0;
        while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
        return (i === 0 ? n : n.toFixed(n < 10 ? 2 : 1)) + ' ' + u[i];
    }

    // --- Collections sidebar ---

    function disableControls() {
        [filterIn, runBtn, sortIn, projIn, newBtn, importBtn, exportBtn, moreBtn].forEach(function (b) { if (b) { b.disabled = true; } });
        Array.prototype.forEach.call(tabBtns, function (b) { b.disabled = true; });
        Array.prototype.forEach.call(qaBtns, function (b) { b.disabled = true; });
        if (docCopy) { docCopy.disabled = true; }
    }

    function loadCollections() {
        state.collection = '';
        state.skip = 0;
        setActiveCollection(null);
        titleEl.textContent = 'No collection selected';
        if (kindBadge) { kindBadge.hidden = true; }
        bcDb.textContent = state.db || '—';
        bcColl.textContent = '—';
        disableControls();
        if (newCollBtn) { newCollBtn.disabled = !state.db; }
        resetRail();
        setPaging();
        emptyState(pane, state.db ? 'collection' : 'hdd-stack',
            state.db ? 'Choose a collection' : 'Choose a database, then a collection',
            state.db ? 'Pick a collection on the left to browse its documents.' : 'Pick one on the left to browse its documents.');
        collList.replaceChildren();
        collCount.textContent = '';
        if (!state.db) { return; }
        collList.replaceChildren(el('li', 'list-group-item small text-body-secondary', 'Loading…'));
        call('collections').then(function (d) {
            renderCollections((d && d.collections) || []);
        }).catch(function (e) { collList.replaceChildren(); collCount.textContent = ''; fail(e); });
    }

    function renderCollections(cols) {
        collList.replaceChildren();
        if (!cols.length) { collCount.textContent = 'No collections'; return; }
        cols.forEach(function (c) {
            var li = el('li', 'list-group-item list-group-item-action d-flex justify-content-between align-items-center');
            li.dataset.name = c.name;
            var nameBtn = el('span', 'text-truncate font-monospace small flex-grow-1');
            nameBtn.setAttribute('role', 'button');
            nameBtn.appendChild(el('i', 'bi bi-collection me-2 text-body-secondary'));
            nameBtn.appendChild(document.createTextNode(c.name));
            nameBtn.addEventListener('click', function () { openCollection(c.name); });
            li.appendChild(nameBtn);
            li.appendChild(el('span', 'badge rounded-pill text-bg-light border mx-2', fmtInt(c.count)));
            if (canDelete) { li.appendChild(collectionMenu(c.name)); }
            collList.appendChild(li);
        });
        collCount.textContent = cols.length + (cols.length === 1 ? ' collection' : ' collections');
        applyCollFilter();
    }

    // A per-collection actions menu (empty / rename / drop) in the sidebar.
    function collectionMenu(name) {
        var wrap = el('div', 'dropdown');
        var btn = el('button', 'btn btn-sm btn-link text-body-secondary p-0 px-1');
        btn.type = 'button';
        btn.setAttribute('data-bs-toggle', 'dropdown');
        btn.setAttribute('aria-expanded', 'false');
        btn.setAttribute('aria-label', 'Collection actions');
        btn.innerHTML = '<i class="bi bi-three-dots-vertical"></i>';
        var menu = el('ul', 'dropdown-menu dropdown-menu-end');
        [['Empty', 'empty'], ['Rename…', 'rename'], ['Drop', 'drop']].forEach(function (pair) {
            var item = el('button', 'dropdown-item small' + (pair[1] === 'drop' ? ' text-danger' : ''), pair[0]);
            item.type = 'button';
            item.addEventListener('click', function () { collectionAction(name, pair[1]); });
            var liItem = el('li');
            liItem.appendChild(item);
            menu.appendChild(liItem);
        });
        wrap.appendChild(btn);
        wrap.appendChild(menu);
        return wrap;
    }

    function collectionAction(name, op) {
        if (op === 'drop') {
            if (!window.confirm('Drop the collection "' + name + '" and all its documents? This cannot be undone.')) { return; }
            call('coll_drop', { collection: name }).then(function () {
                NimVor.toast('Collection dropped.', 'success');
                if (state.collection === name) { loadCollections(); } else { reloadCollections(); }
            }).catch(fail);
        } else if (op === 'empty') {
            if (!window.confirm('Remove every document from "' + name + '"? The collection and its indexes stay. This cannot be undone.')) { return; }
            call('coll_empty', { collection: name }).then(function (d) {
                NimVor.toast('Removed ' + (d.deleted || 0) + ' document' + ((d.deleted === 1) ? '' : 's') + '.', 'success');
                reloadCollections();
                if (state.collection === name) { state.skip = 0; loadRail(); if (state.tab === 'documents') { loadDocuments(); } }
            }).catch(fail);
        } else if (op === 'rename') {
            var to = window.prompt('Rename collection "' + name + '" to:', name);
            if (!to || to.trim() === '' || to === name) { return; }
            call('coll_rename', { collection: name, name: to.trim() }).then(function () {
                NimVor.toast('Collection renamed.', 'success');
                if (state.collection === name) { loadCollections(); } else { reloadCollections(); }
            }).catch(fail);
        }
    }

    function reloadCollections() {
        if (!state.db) { return; }
        call('collections').then(function (d) {
            renderCollections((d && d.collections) || []);
            setActiveCollection(state.collection);
        }).catch(function (e) { fail(e); });
    }

    function applyCollFilter() {
        var q = collFilter.value.trim().toLowerCase();
        Array.prototype.forEach.call(collList.children, function (li) {
            if (!li.dataset.name) { return; }
            li.classList.toggle('d-none', q !== '' && li.dataset.name.toLowerCase().indexOf(q) === -1);
        });
    }

    function setActiveCollection(name) {
        Array.prototype.forEach.call(collList.children, function (li) {
            if (li.dataset.name) { li.classList.toggle('active', li.dataset.name === name); }
        });
    }

    function openCollection(name) {
        state.collection = name;
        state.skip = 0;
        setActiveCollection(name);
        titleEl.textContent = name;
        bcDb.textContent = state.db;
        bcColl.textContent = name;
        if (kindBadge) { kindBadge.hidden = false; }
        [filterIn, runBtn, sortIn, projIn, newBtn, importBtn, exportBtn, moreBtn].forEach(function (b) { if (b) { b.disabled = false; } });
        Array.prototype.forEach.call(tabBtns, function (b) { b.disabled = false; });
        Array.prototype.forEach.call(qaBtns, function (b) { b.disabled = false; });
        loadRail();
        setTab('documents');
    }

    // --- Right rail (document preview, indexes, stats) ---

    function resetRail() {
        state.stats = null;
        if (docJson) { docJson.replaceChildren(el('span', 'text-body-secondary', 'Select a document to preview it here.')); }
        if (docCopy) { docCopy.disabled = true; }
        if (railIndexes) { railIndexes.replaceChildren(el('span', 'small text-body-secondary', '—')); }
        if (railStats) { railStats.replaceChildren(el('span', 'small text-body-secondary', '—')); }
    }

    function loadRail() {
        if (railStats) { railStats.replaceChildren(el('span', 'small text-body-secondary', 'Loading…')); }
        call('collection_stats', { collection: state.collection }).then(function (s) {
            state.stats = s || {};
            renderRailStats(state.stats);
        }).catch(function () { if (railStats) { railStats.replaceChildren(el('span', 'small text-body-secondary', 'Unavailable.')); } });
        if (railIndexes) { railIndexes.replaceChildren(el('span', 'small text-body-secondary', 'Loading…')); }
        call('indexes', { collection: state.collection }).then(function (d) {
            renderRailIndexes(Array.isArray(d.indexes) ? d.indexes : []);
        }).catch(function () { if (railIndexes) { railIndexes.replaceChildren(el('span', 'small text-body-secondary', 'Unavailable.')); } });
    }

    function renderRailStats(s) {
        if (!railStats) { return; }
        var rows = [
            ['Documents', fmtInt(s.count)],
            ['Data size', fmtBytes(s.size)],
            ['Avg. document', s.avgObjSize ? fmtBytes(s.avgObjSize) : '—'],
            ['Storage size', fmtBytes(s.storageSize)],
            ['Indexes', fmtInt(s.nindexes)],
            ['Index size', fmtBytes(s.totalIndexSize)],
        ];
        var dl = el('dl', 'wcp-mongo-stat mb-0');
        rows.forEach(function (r) {
            dl.appendChild(el('dt', null, r[0]));
            dl.appendChild(el('dd', null, r[1]));
        });
        railStats.replaceChildren(dl);
    }

    function indexType(ix) {
        if (ix.name === '_id_') { return 'Unique'; }
        var key = ix.key || {};
        var ks = Object.keys(key);
        if (ks.some(function (k) { return key[k] === 'text'; })) { return 'Text'; }
        if (typeof ix.expireAfterSeconds !== 'undefined') { return 'TTL'; }
        if (ix.unique) { return 'Unique'; }
        if (ks.length && key[ks[0]] === -1) { return 'Descending'; }
        return 'Ascending';
    }

    function renderRailIndexes(list) {
        if (!railIndexes) { return; }
        if (!list.length) { railIndexes.replaceChildren(el('span', 'small text-body-secondary', 'No indexes.')); return; }
        var ul = el('ul', 'list-unstyled mb-0');
        list.slice(0, 6).forEach(function (ix) {
            var li = el('li', 'd-flex align-items-center justify-content-between py-1');
            li.appendChild(el('span', 'font-monospace small text-truncate me-2', ix.name));
            li.appendChild(el('span', 'badge rounded-pill text-bg-light border', indexType(ix)));
            ul.appendChild(li);
        });
        railIndexes.replaceChildren(ul);
    }

    function selectDoc(i) {
        state.selected = i;
        Array.prototype.forEach.call(pane.querySelectorAll('[data-mb-rowidx]'), function (tr) {
            tr.classList.toggle('table-active', Number(tr.getAttribute('data-mb-rowidx')) === i);
        });
        var doc = state.docs[i];
        if (docJson) {
            docJson.textContent = doc ? JSON.stringify(doc, null, 2) : '';
            if (docCopy) { docCopy.disabled = !doc; }
        }
    }

    // --- Tabs ---

    function setTab(tab) {
        state.tab = tab;
        Array.prototype.forEach.call(tabBtns, function (b) { b.classList.toggle('active', b.getAttribute('data-mb-tab') === tab); });
        var docs = tab === 'documents', idx = tab === 'indexes', usr = tab === 'users';
        toolbar.classList.toggle('d-none', !(docs || idx || usr));
        if (countEl) { countEl.classList.toggle('d-none', !docs); }
        if (docFilters) { docFilters.classList.toggle('d-none', !docs); }
        if (viewToggle) { viewToggle.classList.toggle('d-none', !docs); }
        pagerEl.hidden = !docs;
        if (newIndexBtn) {
            newIndexBtn.classList.toggle('d-none', !idx);
            newIndexBtn.disabled = !idx || !state.collection;
        }
        if (newUserBtn) {
            newUserBtn.classList.toggle('d-none', !usr);
            newUserBtn.disabled = !usr || !state.db;
        }
        if (docs) { state.skip = 0; loadDocuments(); }
        else if (idx) { loadIndexes(); }
        else if (tab === 'schema') { loadSchema(); }
        else if (tab === 'validation') { renderValidation(); }
        else if (tab === 'stats') { renderStats(); }
        else if (usr) { loadUsers(); }
        else { renderAggregate(); }
    }

    // --- Validation & Stats tabs (from collection_stats) ---

    function withStats(cb) {
        if (state.stats) { cb(state.stats); return; }
        placeholder(pane, 'Loading…');
        call('collection_stats', { collection: state.collection }).then(function (s) { state.stats = s || {}; cb(state.stats); })
            .catch(function (e) { placeholder(pane, 'The collection statistics could not be read.'); fail(e); });
    }

    function renderValidation() {
        withStats(function (s) {
            if (!s.validator || (typeof s.validator === 'object' && Object.keys(s.validator).length === 0)) {
                emptyState(pane, 'shield-check', 'No validation rules', 'This collection accepts any document shape.');
                return;
            }
            var wrap = el('div', 'p-3');
            var meta = el('div', 'd-flex gap-2 mb-2');
            meta.appendChild(el('span', 'badge text-bg-light border', 'Level: ' + (s.validationLevel || 'strict')));
            meta.appendChild(el('span', 'badge text-bg-light border', 'Action: ' + (s.validationAction || 'error')));
            wrap.appendChild(meta);
            var pre = el('pre', 'wcp-mongo-json mb-0 p-3 border rounded');
            pre.textContent = JSON.stringify(s.validator, null, 2);
            wrap.appendChild(pre);
            pane.replaceChildren(wrap);
        });
    }

    function renderStats() {
        withStats(function (s) {
            var rows = [
                ['Type', s.type || 'collection'],
                ['Documents', fmtInt(s.count)],
                ['Data size', fmtBytes(s.size)],
                ['Average document size', s.avgObjSize ? fmtBytes(s.avgObjSize) : '—'],
                ['Storage size', fmtBytes(s.storageSize)],
                ['Indexes', fmtInt(s.nindexes)],
                ['Total index size', fmtBytes(s.totalIndexSize)],
                ['Capped', s.capped ? 'yes' : 'no'],
            ];
            var wrap = el('div', 'p-3');
            var grid = el('div', 'row g-3');
            rows.forEach(function (r) {
                var col = el('div', 'col-sm-6 col-xl-4');
                var card = el('div', 'wcp-panel h-100 mb-0');
                card.appendChild(el('div', 'small wcp-muted', r[0]));
                card.appendChild(el('div', 'fs-5 fw-semibold text-break', String(r[1])));
                col.appendChild(card);
                grid.appendChild(col);
            });
            wrap.appendChild(grid);
            pane.replaceChildren(wrap);
        });
    }

    // --- Schema analysis ---

    function loadSchema() {
        placeholder(pane, 'Analysing a sample of documents…');
        call('schema', { collection: state.collection, sample: 1000 }).then(function (d) {
            renderSchema(d && d.sampled, (d && d.fields) || []);
        }).catch(function (e) { placeholder(pane, 'The collection could not be analysed.'); fail(e); });
    }

    function renderSchema(sampled, fields) {
        if (!fields.length) {
            emptyState(pane, 'diagram-2', 'Nothing to analyse', sampled ? 'The sampled documents had no fields.' : 'This collection has no documents.');
            return;
        }
        var wrap = el('div', 'p-3');
        wrap.appendChild(el('div', 'small text-body-secondary mb-2', 'Sampled ' + sampled + ' document' + (sampled === 1 ? '' : 's') + '. Presence is how many of them had the field.'));
        var table = el('table', 'table table-sm align-middle');
        var thead = el('thead');
        var hr = el('tr');
        ['Field', 'Types', 'Presence'].forEach(function (h) { hr.appendChild(el('th', 'small', h)); });
        thead.appendChild(hr);
        table.appendChild(thead);
        var tb = el('tbody');
        fields.forEach(function (f) {
            var tr = el('tr');
            tr.appendChild(el('td', 'font-monospace small', f.field));
            var types = (f.types || []).map(function (t) { return t.type + ' (' + t.count + ')'; }).join(', ');
            tr.appendChild(el('td', 'small', types));
            var pct = sampled ? Math.round((f.total / sampled) * 100) : 0;
            tr.appendChild(el('td', 'small text-body-secondary', f.total + ' / ' + sampled + ' (' + pct + '%)'));
            tb.appendChild(tr);
        });
        table.appendChild(tb);
        wrap.appendChild(table);
        pane.replaceChildren(wrap);
    }

    // --- Aggregation (query lab) ---

    var aggPipeline = '[\n  { "$match": {} }\n]';

    function renderAggregate() {
        var wrap = el('div', 'p-3');
        wrap.appendChild(el('label', 'form-label small', 'Pipeline (JSON array of stages)'));
        var ta = el('textarea', 'form-control font-monospace mb-2');
        ta.rows = 8; ta.spellcheck = false; ta.value = aggPipeline;
        wrap.appendChild(ta);
        var bar = el('div', 'd-flex align-items-center gap-2 mb-2');
        var run = el('button', 'btn btn-sm btn-primary', 'Run');
        run.type = 'button';
        var explainBtn = el('button', 'btn btn-sm btn-outline-secondary', 'Explain');
        explainBtn.type = 'button';
        var status = el('span', 'small text-body-secondary');
        bar.appendChild(run);
        bar.appendChild(explainBtn);
        bar.appendChild(status);
        wrap.appendChild(bar);
        var results = el('div', '');
        wrap.appendChild(results);
        pane.replaceChildren(wrap);

        function pipelineText() {
            aggPipeline = ta.value;
            var text = ta.value.trim();
            var parsed;
            try { parsed = JSON.parse(text); } catch (ex) { status.textContent = 'Invalid JSON: ' + ex.message; status.classList.add('text-danger'); return null; }
            if (!Array.isArray(parsed)) { status.textContent = 'The pipeline must be a JSON array of stages.'; status.classList.add('text-danger'); return null; }
            status.classList.remove('text-danger');
            return text;
        }

        run.addEventListener('click', function () {
            var text = pipelineText();
            if (text === null) { return; }
            status.textContent = 'Running…';
            run.disabled = true;
            call('aggregate', { collection: state.collection, pipeline: text, limit: 50 }).then(function (d) {
                run.disabled = false;
                var docs = Array.isArray(d.documents) ? d.documents : [];
                status.textContent = docs.length + ' result' + (docs.length === 1 ? '' : 's') + ' · ' + (d.ms || 0) + ' ms (max 50 shown)';
                results.replaceChildren();
                if (!docs.length) { results.appendChild(el('div', 'small text-body-secondary', 'No results.')); return; }
                docs.forEach(function (doc, i) {
                    var card = el('article', 'card mb-2');
                    card.appendChild(el('div', 'card-header py-1 px-2 small', '#' + (i + 1)));
                    var body = el('pre', 'card-body mb-0 small wcp-mongo-json');
                    body.textContent = JSON.stringify(doc, null, 2);
                    card.appendChild(body);
                    results.appendChild(card);
                });
            }).catch(function (e) { run.disabled = false; status.textContent = ''; fail(e); });
        });

        explainBtn.addEventListener('click', function () {
            var text = pipelineText();
            if (text === null) { return; }
            status.textContent = 'Explaining…';
            explainBtn.disabled = true;
            call('explain', { collection: state.collection, pipeline: text }).then(function (d) {
                explainBtn.disabled = false;
                status.textContent = 'Query plan (the pipeline was not run).';
                var card = el('article', 'card');
                card.appendChild(el('div', 'card-header py-1 px-2 small', 'explain'));
                var body = el('pre', 'card-body mb-0 small wcp-mongo-json');
                body.textContent = JSON.stringify(d.explain, null, 2);
                card.appendChild(body);
                results.replaceChildren(card);
            }).catch(function (e) { explainBtn.disabled = false; status.textContent = ''; fail(e); });
        });
    }

    // --- Users ---

    function loadUsers() {
        placeholder(pane, 'Loading users…');
        call('users').then(function (d) {
            renderUsers((d && d.users) || []);
        }).catch(function (e) { placeholder(pane, 'The users could not be listed.'); fail(e); });
    }

    function renderUsers(users) {
        if (!users.length) {
            emptyState(pane, 'people', 'No database users', 'This database has no users yet.');
            return;
        }
        var wrap = el('div', 'p-3');
        wrap.appendChild(el('div', 'small text-body-secondary mb-2', 'Users defined on ' + state.db + '.'));
        var table = el('table', 'table table-sm align-middle');
        var thead = el('thead');
        var hr = el('tr');
        ['User', 'Roles', ''].forEach(function (h) { hr.appendChild(el('th', 'small', h)); });
        thead.appendChild(hr);
        table.appendChild(thead);
        var tb = el('tbody');
        users.forEach(function (u) {
            var tr = el('tr');
            tr.appendChild(el('td', 'font-monospace small', u.user));
            tr.appendChild(el('td', 'small', (u.roles || []).join(', ')));
            var act = el('td', 'text-end');
            if (canDelete) {
                var pw = el('button', 'btn btn-sm btn-outline-secondary py-0 me-1', 'Change password');
                pw.type = 'button';
                pw.addEventListener('click', function () { openUser('password', u.user); });
                act.appendChild(pw);
                var drop = el('button', 'btn btn-sm btn-outline-danger py-0', 'Drop');
                drop.type = 'button';
                drop.addEventListener('click', function () { dropUser(u.user, drop); });
                act.appendChild(drop);
            }
            tr.appendChild(act);
            tb.appendChild(tr);
        });
        table.appendChild(tb);
        wrap.appendChild(table);
        pane.replaceChildren(wrap);
    }

    function dropUser(name, btn) {
        if (!window.confirm('Drop the user "' + name + '"? Applications using it will lose access.')) { return; }
        btn.disabled = true;
        call('user_drop', { user: name }).then(function () {
            NimVor.toast('User dropped.', 'success');
            loadUsers();
        }).catch(function (e) { btn.disabled = false; fail(e); });
    }

    // --- Documents pane ---

    function setPaging() {
        if (!pagesEl) { return; }
        pagesEl.replaceChildren();
        if (!state.collection) { rangeEl.textContent = ''; return; }
        var limit = state.limit;
        var pages = Math.max(1, Math.ceil(state.total / limit));
        var cur = Math.floor(state.skip / limit) + 1;
        if (state.total === 0) { rangeEl.textContent = '0 documents'; } else {
            rangeEl.textContent = (state.skip + 1) + '–' + Math.min(state.skip + limit, state.total) + ' of ' + fmtInt(state.total);
        }
        function pbtn(label, page, opts) {
            opts = opts || {};
            var b = el('button', 'btn btn-outline-secondary' + (opts.active ? ' active' : ''));
            b.type = 'button';
            if (opts.icon) { b.innerHTML = '<i class="bi bi-' + opts.icon + '"></i>'; } else { b.textContent = label; }
            if (opts.disabled) { b.disabled = true; }
            else { b.addEventListener('click', function () { state.skip = (page - 1) * limit; loadDocuments(); }); }
            pagesEl.appendChild(b);
        }
        pbtn('', cur - 1, { icon: 'chevron-left', disabled: cur <= 1 });
        var list = pageList(cur, pages);
        list.forEach(function (p) {
            if (p === '…') { var g = el('button', 'btn btn-outline-secondary disabled', '…'); g.type = 'button'; g.disabled = true; pagesEl.appendChild(g); }
            else { pbtn(String(p), p, { active: p === cur }); }
        });
        pbtn('', cur + 1, { icon: 'chevron-right', disabled: cur >= pages });
    }

    // Page numbers to show: 1 … (cur-1 cur cur+1) … last, de-duplicated.
    function pageList(cur, last) {
        var s = {};
        [1, 2, last - 1, last, cur - 1, cur, cur + 1].forEach(function (p) { if (p >= 1 && p <= last) { s[p] = true; } });
        var nums = Object.keys(s).map(Number).sort(function (a, b) { return a - b; });
        var out = [];
        for (var i = 0; i < nums.length; i++) {
            if (i > 0 && nums[i] - nums[i - 1] > 1) { out.push('…'); }
            out.push(nums[i]);
        }
        return out;
    }

    function validObject(input, label) {
        var v = input.value.trim();
        input.classList.remove('is-invalid');
        if (v !== '') {
            try { JSON.parse(v); } catch (e) {
                input.classList.add('is-invalid');
                NimVor.toast('The ' + label + ' must be a JSON object, e.g. { "name": 1 }.', 'danger');
                return null;
            }
        }
        return v;
    }

    function loadDocuments() {
        var filter = validObject(filterIn, 'filter');
        if (filter === null) { return; }
        var sort = sortIn ? validObject(sortIn, 'sort') : '';
        if (sort === null) { return; }
        var proj = projIn ? validObject(projIn, 'fields') : '';
        if (proj === null) { return; }
        placeholder(pane, 'Loading…');
        call('documents', { collection: state.collection, filter: filter, sort: sort, projection: proj, skip: state.skip, limit: state.limit }).then(function (d) {
            state.total = d.total || 0;
            state.docs = Array.isArray(d.documents) ? d.documents : [];
            state.selected = -1;
            if (countEl) { countEl.textContent = fmtInt(state.total) + ' document' + (state.total === 1 ? '' : 's'); }
            renderDocuments();
            setPaging();
        }).catch(function (e) { placeholder(pane, 'The documents could not be read.'); fail(e); });
    }

    function renderDocuments() {
        var docs = state.docs;
        if (!docs.length) {
            var filtered = filterIn && filterIn.value.trim() !== '';
            if (state.total === 0 && !filtered) {
                emptyState(pane, 'collection', 'This collection is empty', canDelete ? 'Use “Insert Document” above to add the first one.' : null);
            } else {
                emptyState(pane, 'funnel', 'No matching documents', 'No document matches the current query.');
            }
            return;
        }
        if (state.view === 'json') { renderJsonView(docs); } else { renderTableView(docs); }
        if (state.selected < 0 && docs.length) { selectDoc(0); }
    }

    function idLabel(id) {
        if (id && typeof id === 'object') {
            if (typeof id.$oid === 'string') { return id.$oid; }
            return JSON.stringify(id);
        }
        return String(id);
    }

    function cellText(v) {
        if (v === null) { return { text: 'null', cls: 'wcp-db-null' }; }
        if (typeof v === 'undefined') { return { text: '', cls: '' }; }
        if (typeof v === 'object') {
            if (typeof v.$oid === 'string') { return { text: v.$oid, cls: '' }; }
            if (typeof v.$date !== 'undefined') { return { text: String(v.$date), cls: '' }; }
            if (typeof v.$numberLong !== 'undefined') { return { text: String(v.$numberLong), cls: '' }; }
            if (Array.isArray(v)) { return { text: '[' + v.length + ']', cls: 'text-body-secondary' }; }
            return { text: '{' + Object.keys(v).length + ' field' + (Object.keys(v).length === 1 ? '' : 's') + '}', cls: 'text-body-secondary' };
        }
        if (typeof v === 'boolean') { return { text: v ? 'true' : 'false', cls: '' }; }
        return { text: String(v), cls: '' };
    }

    // Columns: _id first, then the top-level keys seen across the page (capped).
    function columnsFor(docs) {
        var seen = {}, cols = [];
        docs.forEach(function (d) {
            if (!d || typeof d !== 'object') { return; }
            Object.keys(d).forEach(function (k) {
                if (k === '_id') { return; }
                if (!seen[k]) { seen[k] = true; cols.push(k); }
            });
        });
        return ['_id'].concat(cols.slice(0, 7));
    }

    function renderTableView(docs) {
        var cols = columnsFor(docs);
        var wrap = el('div', 'wcp-mongo-tablewrap');
        var table = el('table', 'table table-hover align-middle mb-0 wcp-mongo-table');
        var thead = el('thead');
        var hr = el('tr');
        cols.forEach(function (c) { hr.appendChild(el('th', c === '_id' ? 'wcp-mongo-idcol' : null, c)); });
        if (canDelete) { hr.appendChild(el('th', 'text-end', '')); }
        thead.appendChild(hr);
        table.appendChild(thead);
        var tb = el('tbody');
        docs.forEach(function (doc, i) {
            var tr = el('tr');
            tr.setAttribute('data-mb-rowidx', String(i));
            tr.addEventListener('click', function () { selectDoc(i); });
            cols.forEach(function (c) {
                if (c === '_id') {
                    var td = el('td', 'wcp-mongo-idcol');
                    var a = el('button', 'btn btn-link p-0 font-monospace small text-truncate', (doc && typeof doc._id !== 'undefined') ? idLabel(doc._id) : '(no _id)');
                    a.type = 'button';
                    a.addEventListener('click', function (e) { e.stopPropagation(); selectDoc(i); });
                    td.appendChild(a);
                    tr.appendChild(td);
                } else {
                    var cv = cellText(doc ? doc[c] : undefined);
                    tr.appendChild(el('td', 'font-monospace small text-truncate ' + cv.cls, cv.text));
                }
            });
            if (canDelete) {
                var act = el('td', 'text-end text-nowrap');
                var edit = el('button', 'btn btn-sm btn-link p-0 me-2', null);
                edit.type = 'button'; edit.title = 'Edit'; edit.innerHTML = '<i class="bi bi-pencil"></i>';
                edit.addEventListener('click', function (e) { e.stopPropagation(); openEditor('edit', doc); });
                act.appendChild(edit);
                if (doc && typeof doc._id !== 'undefined') {
                    var del = el('button', 'btn btn-sm btn-link p-0 text-danger', null);
                    del.type = 'button'; del.title = 'Delete'; del.innerHTML = '<i class="bi bi-trash"></i>';
                    del.addEventListener('click', function (e) { e.stopPropagation(); deleteDoc(doc._id, del); });
                    act.appendChild(del);
                }
                tr.appendChild(act);
            }
            tb.appendChild(tr);
        });
        table.appendChild(tb);
        wrap.appendChild(table);
        pane.replaceChildren(wrap);
    }

    function renderJsonView(docs) {
        var wrap = el('div', 'wcp-mongo-docs p-2');
        docs.forEach(function (doc, i) {
            var card = el('article', 'card mb-2');
            var head = el('div', 'card-header d-flex align-items-center gap-2 py-1 px-2');
            head.appendChild(el('span', 'badge text-bg-secondary', '#' + (state.skip + i + 1)));
            var id = (doc && typeof doc._id !== 'undefined') ? idLabel(doc._id) : '(no _id)';
            head.appendChild(el('code', 'small text-truncate me-auto', '_id: ' + id));
            if (canDelete) {
                var edit = el('button', 'btn btn-sm btn-outline-secondary py-0', 'Edit');
                edit.type = 'button';
                edit.addEventListener('click', function () { openEditor('edit', doc); });
                head.appendChild(edit);
                var dup = el('button', 'btn btn-sm btn-outline-secondary py-0', 'Duplicate');
                dup.type = 'button';
                dup.addEventListener('click', function () { openEditor('duplicate', doc); });
                head.appendChild(dup);
            }
            if (canDelete && doc && typeof doc._id !== 'undefined') {
                var del = el('button', 'btn btn-sm btn-outline-danger py-0', 'Delete');
                del.type = 'button';
                del.addEventListener('click', function () { deleteDoc(doc._id, del); });
                head.appendChild(del);
            }
            card.appendChild(head);
            var body = el('pre', 'card-body mb-0 small wcp-mongo-json');
            body.textContent = JSON.stringify(doc, null, 2);
            card.appendChild(body);
            wrap.appendChild(card);
        });
        pane.replaceChildren(wrap);
    }

    function deleteDoc(id, btn) {
        if (!window.confirm('Delete this document? This cannot be undone.')) { return; }
        btn.disabled = true;
        call('delete', { collection: state.collection, id: JSON.stringify(id) }).then(function () {
            NimVor.toast('Document deleted.', 'success');
            if (state.skip > 0 && state.skip + 1 >= state.total) { state.skip = Math.max(0, state.skip - state.limit); }
            loadRail();
            loadDocuments();
        }).catch(function (e) { btn.disabled = false; fail(e); });
    }

    // --- Document editor (new / edit / duplicate) ---

    var editorEl = document.getElementById('mb-editor');
    var editor = null, editorMode = 'new', editorId = null;
    if (editorEl && window.bootstrap) { editor = new bootstrap.Modal(editorEl); }
    var newBtn = root.querySelector('[data-mb-new]');

    function openEditor(mode, doc) {
        if (!editor) { return; }
        editorMode = mode;
        editorId = null;
        var ta = editorEl.querySelector('[data-mb-editor-json]');
        var help = editorEl.querySelector('[data-mb-editor-help]');
        editorEl.querySelector('[data-mb-editor-json]').classList.remove('is-invalid');
        if (mode === 'new') {
            editorEl.querySelector('#mb-editor-title').textContent = 'Insert document in ' + state.collection;
            ta.value = '{\n  \n}';
            help.textContent = 'A new _id is generated if you omit it.';
        } else if (mode === 'edit') {
            editorId = doc._id;
            editorEl.querySelector('#mb-editor-title').textContent = 'Edit document';
            ta.value = JSON.stringify(doc, null, 2);
            help.textContent = 'The _id cannot be changed; MongoDB rejects a different _id.';
        } else {
            var copy = Object.assign({}, doc);
            delete copy._id;
            editorEl.querySelector('#mb-editor-title').textContent = 'Duplicate document';
            ta.value = JSON.stringify(copy, null, 2);
            help.textContent = 'Saved as a new document with a new _id.';
        }
        editor.show();
    }

    if (editorEl) {
        editorEl.querySelector('[data-mb-editor-form]').addEventListener('submit', function (e) {
            e.preventDefault();
            var ta = editorEl.querySelector('[data-mb-editor-json]');
            var errEl = editorEl.querySelector('[data-mb-editor-err]');
            var text = ta.value.trim();
            try { JSON.parse(text); } catch (ex) {
                ta.classList.add('is-invalid');
                errEl.textContent = 'Enter a valid JSON object: ' + ex.message;
                return;
            }
            ta.classList.remove('is-invalid');
            var ok = editorEl.querySelector('[data-mb-editor-ok]');
            ok.disabled = true;
            var done = function () { ok.disabled = false; editor.hide(); loadRail(); loadDocuments(); };
            var failed = function (ee) { ok.disabled = false; fail(ee); };
            if (editorMode === 'edit') {
                call('update', { collection: state.collection, id: JSON.stringify(editorId), document: text }).then(done).catch(failed);
            } else {
                call('insert', { collection: state.collection, document: text }).then(done).catch(failed);
            }
        });
    }

    // --- Indexes ---

    var newIndexBtn = root.querySelector('[data-mb-newindex]');
    var indexEl = document.getElementById('mb-index');
    var indexModal = null;
    if (indexEl && window.bootstrap) { indexModal = new bootstrap.Modal(indexEl); }

    var importBtn = root.querySelector('[data-mb-import]');
    var importEl = document.getElementById('mb-import');
    var importModal = null;
    if (importEl && window.bootstrap) { importModal = new bootstrap.Modal(importEl); }

    var exportBtn = root.querySelector('[data-mb-export]');

    var newUserBtn = root.querySelector('[data-mb-newuser]');
    var userEl = document.getElementById('mb-user');
    var userModal = null, userMode = 'new';
    if (userEl && window.bootstrap) { userModal = new bootstrap.Modal(userEl); }

    function openUser(mode, name) {
        if (!userModal) { return; }
        userMode = mode;
        userEl.querySelector('[data-mb-user-form]').reset();
        userEl.querySelector('[data-mb-user-err]').textContent = '';
        var nameIn = userEl.querySelector('[data-mb-user-name]');
        nameIn.value = name || '';
        nameIn.readOnly = mode === 'password';
        userEl.querySelector('[data-mb-user-namewrap]').classList.toggle('d-none', mode === 'password' && !name);
        userEl.querySelector('[data-mb-user-rolewrap]').classList.toggle('d-none', mode === 'password');
        userEl.querySelector('#mb-user-title').textContent = mode === 'password' ? 'Change password for ' + name : 'New user on ' + state.db;
        userModal.show();
    }

    function loadIndexes() {
        placeholder(pane, 'Loading…');
        call('indexes', { collection: state.collection }).then(function (d) {
            var list = Array.isArray(d.indexes) ? d.indexes : [];
            renderRailIndexes(list);
            renderIndexes(list);
        }).catch(function (e) { placeholder(pane, 'The indexes could not be read.'); fail(e); });
    }

    function indexProps(ix) {
        var p = [];
        if (ix.unique) { p.push('unique'); }
        if (ix.sparse) { p.push('sparse'); }
        if (typeof ix.expireAfterSeconds !== 'undefined') { p.push('TTL ' + ix.expireAfterSeconds + 's'); }
        var key = ix.key || {};
        if (Object.keys(key).some(function (k) { return key[k] === 'text'; })) { p.push('text'); }
        return p.join(', ');
    }

    function renderIndexes(list) {
        var wrap = el('div', 'p-3');
        var table = el('table', 'table table-sm align-middle mb-0');
        var thead = el('thead');
        var htr = el('tr');
        ['Name', 'Keys', 'Properties', ''].forEach(function (h) { htr.appendChild(el('th', h === '' ? 'text-end' : null, h)); });
        thead.appendChild(htr);
        table.appendChild(thead);
        var tb = el('tbody');
        list.forEach(function (ix) {
            var tr = el('tr');
            tr.appendChild(el('td', 'font-monospace small', ix.name));
            tr.appendChild(el('td', 'font-monospace small', JSON.stringify(ix.key || {})));
            tr.appendChild(el('td', 'small text-body-secondary', indexProps(ix)));
            var act = el('td', 'text-end');
            if (canDelete && ix.name !== '_id_') {
                var drop = el('button', 'btn btn-sm btn-outline-danger py-0', 'Drop');
                drop.type = 'button';
                drop.addEventListener('click', function () { dropIndex(ix.name, drop); });
                act.appendChild(drop);
            }
            tr.appendChild(act);
            tb.appendChild(tr);
        });
        table.appendChild(tb);
        wrap.appendChild(table);
        pane.replaceChildren(wrap);
    }

    function dropIndex(name, btn) {
        if (!window.confirm('Drop the index "' + name + '"? Queries that relied on it may slow down.')) { return; }
        btn.disabled = true;
        call('index_drop', { collection: state.collection, name: name }).then(function () {
            NimVor.toast('Index dropped.', 'success');
            loadIndexes();
        }).catch(function (e) { btn.disabled = false; fail(e); });
    }

    if (indexEl) {
        indexEl.querySelector('[data-mb-index-form]').addEventListener('submit', function (e) {
            e.preventDefault();
            var errEl = indexEl.querySelector('[data-mb-index-err]');
            errEl.textContent = '';
            var keysText = indexEl.querySelector('[data-mb-index-keys]').value.trim();
            var keysObj;
            try { keysObj = JSON.parse(keysText); } catch (ex) { errEl.textContent = 'Keys must be a JSON object: ' + ex.message; return; }
            if (!keysObj || typeof keysObj !== 'object' || Array.isArray(keysObj) || Object.keys(keysObj).length === 0) {
                errEl.textContent = 'Keys must be a non-empty JSON object, e.g. { "email": 1 }.';
                return;
            }
            var options = {};
            if (indexEl.querySelector('[data-mb-index-unique]').checked) { options.unique = true; }
            if (indexEl.querySelector('[data-mb-index-sparse]').checked) { options.sparse = true; }
            var ttl = indexEl.querySelector('[data-mb-index-ttl]').value.trim();
            if (ttl !== '' && /^\d+$/.test(ttl)) { options.expireAfterSeconds = parseInt(ttl, 10); }
            var nm = indexEl.querySelector('[data-mb-index-name]').value.trim();
            if (nm !== '') { options.name = nm; }
            var ok = indexEl.querySelector('[data-mb-index-ok]');
            ok.disabled = true;
            call('index_create', { collection: state.collection, keys: JSON.stringify(keysObj), options: JSON.stringify(options) }).then(function () {
                ok.disabled = false; indexModal.hide(); NimVor.toast('Index created.', 'success');
                loadRail();
                if (state.tab === 'indexes') { loadIndexes(); }
            }).catch(function (ee) { ok.disabled = false; errEl.textContent = (ee.details && ee.details.message) || ee.message || 'Failed.'; });
        });
    }

    if (importBtn && importModal) {
        importBtn.addEventListener('click', function () {
            importEl.querySelector('[data-mb-import-form]').reset();
            importEl.querySelector('[data-mb-import-err]').textContent = '';
            importEl.querySelector('#mb-import-title').textContent = 'Import documents into ' + state.collection;
            importModal.show();
        });
        var fileIn = importEl.querySelector('[data-mb-import-file]');
        var textIn = importEl.querySelector('[data-mb-import-text]');
        var fmtIn = importEl.querySelector('[data-mb-import-format]');
        fileIn.addEventListener('change', function () {
            var f = fileIn.files && fileIn.files[0];
            if (!f) { return; }
            if (/\.(jsonl|ndjson)$/i.test(f.name)) { fmtIn.value = 'jsonl'; }
            var reader = new FileReader();
            reader.onload = function () { textIn.value = String(reader.result || ''); };
            reader.readAsText(f);
        });
        importEl.querySelector('[data-mb-import-form]').addEventListener('submit', function (e) {
            e.preventDefault();
            var errEl = importEl.querySelector('[data-mb-import-err]');
            errEl.textContent = '';
            var content = textIn.value.trim();
            if (content === '') { errEl.textContent = 'Paste documents or choose a file first.'; return; }
            var ok = importEl.querySelector('[data-mb-import-ok]');
            ok.disabled = true;
            call('import', { collection: state.collection, format: fmtIn.value, content: content }).then(function (d) {
                ok.disabled = false; importModal.hide();
                var msg = 'Imported ' + d.inserted + ' of ' + d.total + ' document' + (d.total === 1 ? '' : 's') + '.';
                NimVor.toast(msg + (d.errors ? ' ' + d.errors + ' skipped (errors).' : ''), d.errors ? 'warning' : 'success');
                loadRail();
                if (state.tab === 'documents') { state.skip = 0; loadDocuments(); }
            }).catch(function (ee) { ok.disabled = false; errEl.textContent = (ee.details && ee.details.message) || ee.message || 'The import failed.'; });
        });
    }

    if (userEl) {
        userEl.querySelector('[data-mb-user-form]').addEventListener('submit', function (e) {
            e.preventDefault();
            var errEl = userEl.querySelector('[data-mb-user-err]');
            errEl.textContent = '';
            var name = userEl.querySelector('[data-mb-user-name]').value.trim();
            var pass = userEl.querySelector('[data-mb-user-pass]').value;
            if (name === '') { errEl.textContent = 'Enter a user name.'; return; }
            if (pass.length < 8) { errEl.textContent = 'The password must be at least 8 characters.'; return; }
            var ok = userEl.querySelector('[data-mb-user-ok]');
            ok.disabled = true;
            var done = function () {
                ok.disabled = false; userModal.hide();
                NimVor.toast(userMode === 'password' ? 'Password changed.' : 'User created.', 'success');
                loadUsers();
            };
            var failed = function (ee) { ok.disabled = false; errEl.textContent = (ee.details && ee.details.message) || ee.message || 'Failed.'; };
            if (userMode === 'password') {
                call('user_password', { user: name, password: pass }).then(done).catch(failed);
            } else {
                call('user_create', { user: name, password: pass, role: userEl.querySelector('[data-mb-user-role]').value }).then(done).catch(failed);
            }
        });
    }

    // --- Wiring ---

    Array.prototype.forEach.call(tabBtns, function (b) {
        b.addEventListener('click', function () { if (!b.disabled) { setTab(b.getAttribute('data-mb-tab')); } });
    });
    Array.prototype.forEach.call(root.querySelectorAll('[data-mb-view]'), function (b) {
        b.addEventListener('click', function () {
            state.view = b.getAttribute('data-mb-view');
            Array.prototype.forEach.call(root.querySelectorAll('[data-mb-view]'), function (x) { x.classList.toggle('active', x === b); });
            if (state.tab === 'documents') { renderDocuments(); }
        });
    });
    Array.prototype.forEach.call(root.querySelectorAll('[data-mb-coll-op]'), function (b) {
        b.addEventListener('click', function () { if (state.collection) { collectionAction(state.collection, b.getAttribute('data-mb-coll-op')); } });
    });
    Array.prototype.forEach.call(qaBtns, function (b) {
        b.addEventListener('click', function () {
            if (b.disabled) { return; }
            var a = b.getAttribute('data-mb-qa');
            if (a === 'index') { if (newIndexModalOpen) { newIndexModalOpen(); } }
            else if (a === 'validate') { setTab('validation'); }
            else if (a === 'schema') { setTab('schema'); }
        });
    });
    if (docCopy) {
        docCopy.addEventListener('click', function () {
            var text = docJson ? docJson.textContent : '';
            if (!text) { return; }
            if (navigator.clipboard && navigator.clipboard.writeText) {
                navigator.clipboard.writeText(text).then(function () { NimVor.toast('Document copied.', 'success'); }, function () { NimVor.toast('Could not copy.', 'danger'); });
            }
        });
    }
    if (railIndexesAll) { railIndexesAll.addEventListener('click', function () { if (state.collection) { setTab('indexes'); } }); }

    var newIndexModalOpen = null;
    if (indexModal) {
        newIndexModalOpen = function () {
            indexEl.querySelector('[data-mb-index-form]').reset();
            indexEl.querySelector('[data-mb-index-err]').textContent = '';
            indexEl.querySelector('#mb-index-title').textContent = 'New index on ' + state.collection;
            indexModal.show();
        };
    }
    if (newIndexBtn && newIndexModalOpen) { newIndexBtn.addEventListener('click', newIndexModalOpen); }
    if (newUserBtn && userModal) { newUserBtn.addEventListener('click', function () { openUser('new', ''); }); }
    if (newBtn) { newBtn.addEventListener('click', function () { openEditor('new', {}); }); }
    Array.prototype.forEach.call(root.querySelectorAll('[data-mb-export-format]'), function (item) {
        item.addEventListener('click', function () {
            if (!state.db || !state.collection) { return; }
            var q = 'db=' + encodeURIComponent(state.db) +
                '&collection=' + encodeURIComponent(state.collection) +
                '&format=' + encodeURIComponent(item.getAttribute('data-mb-export-format')) +
                '&filter=' + encodeURIComponent(filterIn.value.trim());
            window.location.assign(exportApi + '?' + q);
        });
    });
    dbSel.addEventListener('change', function () { state.db = dbSel.value; loadCollections(); });
    if (newCollBtn) {
        newCollBtn.addEventListener('click', function () {
            if (!state.db) { return; }
            var name = window.prompt('New collection name in ' + state.db + ':', '');
            if (!name || name.trim() === '') { return; }
            call('coll_create', { collection: name.trim() }).then(function () {
                NimVor.toast('Collection created.', 'success');
                reloadCollections();
            }).catch(fail);
        });
    }
    collFilter.addEventListener('input', applyCollFilter);
    runBtn.addEventListener('click', function () { state.skip = 0; loadDocuments(); });
    [filterIn, sortIn, projIn].forEach(function (inp) {
        if (inp) { inp.addEventListener('keydown', function (e) { if (e.key === 'Enter') { state.skip = 0; loadDocuments(); } }); }
    });
    if (pageSizeSel) {
        pageSizeSel.addEventListener('change', function () {
            var n = parseInt(pageSizeSel.value, 10);
            state.limit = (n > 0 && n <= 200) ? n : 25;
            state.skip = 0;
            if (state.collection && state.tab === 'documents') { loadDocuments(); }
        });
    }

    // Open directly on a database when the page was linked with one.
    state.db = root.getAttribute('data-current') || '';
    if (state.db) { loadCollections(); }
})();
