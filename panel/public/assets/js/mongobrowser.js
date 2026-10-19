/* MongoDB database browser (Server → MongoDB).
 *
 * Loaded from 'self' (the panel's CSP forbids inline scripts). It drives the
 * #mongo-browser card: choose a database, list its collections, read documents
 * with an optional EJSON filter and paging, and delete one document. All calls
 * go to the mongo.browse endpoint via WhaCP.fetchJson (which adds the CSRF
 * token on POST). */
(function () {
    'use strict';
    var root = document.getElementById('mongo-browser');
    if (!root || !window.WhaCP) { return; }

    var url = root.getAttribute('data-browse-url');
    var canDelete = root.getAttribute('data-can-delete') === '1';
    var dbSel = root.querySelector('[data-mb="db"]');
    var collSel = root.querySelector('[data-mb="collection"]');
    var filterIn = root.querySelector('[data-mb="filter"]');
    var results = root.querySelector('[data-mb-results]');
    var statusEl = root.querySelector('[data-mb-status]');
    var prevBtn = root.querySelector('[data-mb-prev]');
    var nextBtn = root.querySelector('[data-mb-next]');
    var skip = 0;
    var limit = 50;
    var total = 0;

    function post(op, extra) {
        var body = Object.assign({ op: op, db: dbSel.value }, extra || {});
        return WhaCP.fetchJson(url, { method: 'POST', body: JSON.stringify(body) }).then(function (r) { return r.data; });
    }

    function setStatus(text, isError) {
        statusEl.textContent = text || '';
        statusEl.classList.toggle('text-danger', !!isError);
    }

    function el(tag, cls, text) {
        var x = document.createElement(tag);
        if (cls) { x.className = cls; }
        if (text !== undefined) { x.textContent = text; }
        return x;
    }

    function loadCollections() {
        collSel.innerHTML = '';
        results.replaceChildren();
        setStatus('');
        skip = 0;
        if (!dbSel.value) { collSel.disabled = true; return; }
        collSel.disabled = true;
        post('collections').then(function (d) {
            var cols = (d && d.collections) || [];
            if (!cols.length) {
                collSel.appendChild(new Option('No collections', ''));
                setStatus('This database has no collections.');
                return;
            }
            cols.forEach(function (c) {
                collSel.appendChild(new Option(c.name + ' (' + c.count + ')', c.name));
            });
            collSel.disabled = false;
        }).catch(function (e) { setStatus(e.message || 'Could not list collections.', true); });
    }

    function render(docs) {
        results.replaceChildren();
        if (!docs.length) { results.appendChild(el('div', 'small text-body-secondary', 'No documents match.')); return; }
        docs.forEach(function (doc) {
            var card = el('div', 'border rounded p-2 mb-2 position-relative');
            var pre = el('pre', 'mb-0 small', JSON.stringify(doc, null, 2));
            pre.style.whiteSpace = 'pre-wrap';
            pre.style.wordBreak = 'break-word';
            card.appendChild(pre);
            if (canDelete && doc && typeof doc._id !== 'undefined') {
                var btn = el('button', 'btn btn-sm btn-outline-danger position-absolute top-0 end-0 m-1', 'Delete');
                btn.type = 'button';
                btn.addEventListener('click', function () {
                    if (!window.confirm('Delete this document? This cannot be undone.')) { return; }
                    btn.disabled = true;
                    post('delete', { collection: collSel.value, id: JSON.stringify(doc._id) }).then(function () {
                        run();
                    }).catch(function (e) { btn.disabled = false; setStatus(e.message || 'Delete failed.', true); });
                });
                card.appendChild(btn);
            }
            results.appendChild(card);
        });
    }

    function run() {
        if (!dbSel.value || !collSel.value) { setStatus('Choose a database and collection.'); return; }
        setStatus('Loading…');
        post('documents', { collection: collSel.value, filter: filterIn.value.trim(), skip: skip, limit: limit }).then(function (d) {
            total = d.total || 0;
            render(Array.isArray(d.documents) ? d.documents : []);
            var from = total === 0 ? 0 : skip + 1;
            var to = Math.min(skip + limit, total);
            setStatus(total === 0 ? 'No documents match.' : ('Showing ' + from + '–' + to + ' of ' + total));
            prevBtn.disabled = skip <= 0;
            nextBtn.disabled = skip + limit >= total;
        }).catch(function (e) { setStatus(e.message || 'Could not read documents.', true); });
    }

    dbSel.addEventListener('change', loadCollections);
    root.querySelector('[data-mb-run]').addEventListener('click', function () { skip = 0; run(); });
    prevBtn.addEventListener('click', function () { skip = Math.max(0, skip - limit); run(); });
    nextBtn.addEventListener('click', function () { skip = skip + limit; run(); });
    filterIn.addEventListener('keydown', function (e) { if (e.key === 'Enter') { skip = 0; run(); } });
})();
