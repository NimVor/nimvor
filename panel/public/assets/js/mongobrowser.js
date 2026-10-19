/* MongoDB database browser (Server → MongoDB → Browser).
 *
 * Drives the full-page #wcp-mongo tool: a collections sidebar and a documents
 * pane with a JSON filter and paging. Loaded from 'self' (the panel CSP
 * forbids inline scripts); all calls go to the mongo.browse endpoint through
 * WhaCP.fetchJson, which attaches the CSRF token on POST. */
(function () {
    'use strict';
    var root = document.getElementById('wcp-mongo');
    if (!root || !window.WhaCP) { return; }

    var api = root.getAttribute('data-api');
    var canDelete = root.getAttribute('data-can-delete') === '1';
    var LIMIT = 25;

    var dbSel = root.querySelector('[data-mb-db]');
    var collList = root.querySelector('[data-mb-colls]');
    var collFilter = root.querySelector('[data-mb-coll-filter]');
    var collCount = root.querySelector('[data-mb-colls-count]');
    var curLabel = root.querySelector('[data-mb-current]');
    var filterIn = root.querySelector('[data-mb-filter]');
    var runBtn = root.querySelector('[data-mb-run]');
    var prevBtn = root.querySelector('[data-mb-prev]');
    var nextBtn = root.querySelector('[data-mb-next]');
    var rangeEl = root.querySelector('[data-mb-range]');
    var pane = root.querySelector('[data-mb-pane]');
    var tabBtns = root.querySelectorAll('[data-mb-tab]');
    var docTools = root.querySelector('[data-mb-doc-tools]');
    var pagerEl = root.querySelector('[data-mb-pager]');

    var state = { db: '', collection: '', skip: 0, total: 0, tab: 'documents' };

    function el(tag, cls, text) {
        var e = document.createElement(tag);
        if (cls) { e.className = cls; }
        if (text !== undefined && text !== null) { e.textContent = text; }
        return e;
    }
    function call(op, params) {
        return WhaCP.fetchJson(api, { method: 'POST', body: JSON.stringify(Object.assign({ op: op, db: state.db }, params || {})) })
            .then(function (r) { return r.data; });
    }
    function fail(err) {
        var d = (err && err.details) || {};
        WhaCP.toast((d.message || (err && err.message) || 'Request failed') + (d.details ? ' — ' + d.details : ''), 'danger');
    }
    function placeholder(target, text) { target.replaceChildren(el('div', 'text-body-secondary p-4', text)); }

    // --- Collections sidebar ---

    function loadCollections() {
        state.collection = '';
        state.skip = 0;
        setActiveCollection(null);
        curLabel.textContent = 'No collection selected';
        filterIn.disabled = true; runBtn.disabled = true;
        if (newBtn) { newBtn.disabled = true; }
        if (newIndexBtn) { newIndexBtn.disabled = true; }
        Array.prototype.forEach.call(tabBtns, function (b) { b.disabled = true; });
        setPaging();
        placeholder(pane, state.db ? 'Choose a collection to browse its documents.' : 'Choose a database, then a collection.');
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
        if (!cols.length) {
            collCount.textContent = 'No collections';
            return;
        }
        cols.forEach(function (c) {
            var li = el('li', 'list-group-item list-group-item-action d-flex justify-content-between align-items-center');
            li.setAttribute('role', 'button');
            li.dataset.name = c.name;
            li.appendChild(el('span', 'text-truncate font-monospace small', c.name));
            li.appendChild(el('span', 'badge rounded-pill text-bg-light border ms-2', String(c.count)));
            li.addEventListener('click', function () { openCollection(c.name); });
            collList.appendChild(li);
        });
        collCount.textContent = cols.length + (cols.length === 1 ? ' collection' : ' collections');
        applyCollFilter();
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
        curLabel.textContent = name;
        filterIn.disabled = false; runBtn.disabled = false;
        if (newBtn) { newBtn.disabled = false; }
        Array.prototype.forEach.call(tabBtns, function (b) { b.disabled = false; });
        setTab('documents');
    }

    // --- Documents / Indexes tabs ---

    function setTab(tab) {
        state.tab = tab;
        Array.prototype.forEach.call(tabBtns, function (b) { b.classList.toggle('active', b.getAttribute('data-mb-tab') === tab); });
        var docs = tab === 'documents', idx = tab === 'indexes';
        if (docTools) { docTools.classList.toggle('d-none', !docs); }
        if (pagerEl) { pagerEl.classList.toggle('d-none', !docs); }
        rangeEl.classList.toggle('d-none', !docs);
        if (newIndexBtn) {
            newIndexBtn.classList.toggle('d-none', !idx);
            newIndexBtn.disabled = !idx || !state.collection;
        }
        if (docs) { state.skip = 0; loadDocuments(); } else if (idx) { loadIndexes(); } else { renderAggregate(); }
    }

    // --- Aggregation (query lab) ---

    var aggPipeline = '[\n  { "$match": {} }\n]';

    function renderAggregate() {
        var wrap = el('div', 'p-2');
        wrap.appendChild(el('label', 'form-label small', 'Pipeline (JSON array of stages)'));
        var ta = el('textarea', 'form-control font-monospace mb-2');
        ta.rows = 8; ta.spellcheck = false; ta.value = aggPipeline;
        wrap.appendChild(ta);
        var bar = el('div', 'd-flex align-items-center gap-2 mb-2');
        var run = el('button', 'btn btn-sm btn-primary', 'Run');
        run.type = 'button';
        var status = el('span', 'small text-body-secondary');
        bar.appendChild(run);
        bar.appendChild(status);
        wrap.appendChild(bar);
        var results = el('div', '');
        wrap.appendChild(results);
        pane.replaceChildren(wrap);

        run.addEventListener('click', function () {
            aggPipeline = ta.value;
            var text = ta.value.trim();
            var parsed;
            try { parsed = JSON.parse(text); } catch (ex) { status.textContent = 'Invalid JSON: ' + ex.message; status.classList.add('text-danger'); return; }
            if (!Array.isArray(parsed)) { status.textContent = 'The pipeline must be a JSON array of stages.'; status.classList.add('text-danger'); return; }
            status.classList.remove('text-danger');
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
    }

    // --- Documents pane ---

    function setPaging() {
        prevBtn.disabled = state.skip <= 0;
        nextBtn.disabled = state.skip + LIMIT >= state.total;
        if (!state.collection) { rangeEl.textContent = ''; return; }
        if (state.total === 0) { rangeEl.textContent = '0 documents'; return; }
        rangeEl.textContent = (state.skip + 1) + '–' + Math.min(state.skip + LIMIT, state.total) + ' of ' + state.total;
    }

    function loadDocuments() {
        var filter = filterIn.value.trim();
        filterIn.classList.remove('is-invalid');
        if (filter !== '') {
            try { JSON.parse(filter); } catch (e) {
                filterIn.classList.add('is-invalid');
                WhaCP.toast('The filter must be a JSON object, e.g. { "name": "value" }.', 'danger');
                return;
            }
        }
        placeholder(pane, 'Loading…');
        call('documents', { collection: state.collection, filter: filter, skip: state.skip, limit: LIMIT }).then(function (d) {
            state.total = d.total || 0;
            renderDocuments(Array.isArray(d.documents) ? d.documents : []);
            setPaging();
        }).catch(function (e) { placeholder(pane, 'The documents could not be read.'); fail(e); });
    }

    function renderDocuments(docs) {
        if (!docs.length) {
            placeholder(pane, state.total === 0 ? 'This collection has no matching documents.' : 'No documents on this page.');
            return;
        }
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

    function idLabel(id) {
        if (id && typeof id === 'object') {
            if (typeof id.$oid === 'string') { return id.$oid; }
            return JSON.stringify(id);
        }
        return String(id);
    }

    function deleteDoc(id, btn) {
        if (!window.confirm('Delete this document? This cannot be undone.')) { return; }
        btn.disabled = true;
        call('delete', { collection: state.collection, id: JSON.stringify(id) }).then(function () {
            WhaCP.toast('Document deleted.', 'success');
            // Stay on a sensible page if the last row of the last page went.
            if (state.skip > 0 && state.skip + 1 >= state.total) { state.skip = Math.max(0, state.skip - LIMIT); }
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
            editorEl.querySelector('#mb-editor-title').textContent = 'New document in ' + state.collection;
            ta.value = '{\n  \n}';
            help.textContent = 'A new _id is generated if you omit it.';
        } else if (mode === 'edit') {
            editorId = doc._id;
            editorEl.querySelector('#mb-editor-title').textContent = 'Edit document';
            ta.value = JSON.stringify(doc, null, 2);
            help.textContent = 'The _id cannot be changed; MongoDB rejects a different _id.';
        } else { // duplicate
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
            var done = function () { ok.disabled = false; editor.hide(); loadDocuments(); };
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

    function loadIndexes() {
        placeholder(pane, 'Loading…');
        call('indexes', { collection: state.collection }).then(function (d) {
            renderIndexes(Array.isArray(d.indexes) ? d.indexes : []);
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
        var wrap = el('div', 'p-2');
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
            WhaCP.toast('Index dropped.', 'success');
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
                ok.disabled = false; indexModal.hide(); WhaCP.toast('Index created.', 'success'); loadIndexes();
            }).catch(function (ee) { ok.disabled = false; errEl.textContent = (ee.details && ee.details.message) || ee.message || 'Failed.'; });
        });
    }

    // --- Wiring ---

    Array.prototype.forEach.call(tabBtns, function (b) {
        b.addEventListener('click', function () { if (!b.disabled) { setTab(b.getAttribute('data-mb-tab')); } });
    });
    if (newIndexBtn && indexModal) {
        newIndexBtn.addEventListener('click', function () {
            indexEl.querySelector('[data-mb-index-form]').reset();
            indexEl.querySelector('[data-mb-index-err]').textContent = '';
            indexEl.querySelector('#mb-index-title').textContent = 'New index on ' + state.collection;
            indexModal.show();
        });
    }
    if (newBtn) { newBtn.addEventListener('click', function () { openEditor('new', {}); }); }
    dbSel.addEventListener('change', function () { state.db = dbSel.value; loadCollections(); });
    collFilter.addEventListener('input', applyCollFilter);
    runBtn.addEventListener('click', function () { state.skip = 0; loadDocuments(); });
    filterIn.addEventListener('keydown', function (e) { if (e.key === 'Enter') { state.skip = 0; loadDocuments(); } });
    prevBtn.addEventListener('click', function () { state.skip = Math.max(0, state.skip - LIMIT); loadDocuments(); });
    nextBtn.addEventListener('click', function () { state.skip = state.skip + LIMIT; loadDocuments(); });

    // Open directly on a database when the page was linked with one.
    state.db = root.getAttribute('data-current') || '';
    if (state.db) { loadCollections(); }
})();
