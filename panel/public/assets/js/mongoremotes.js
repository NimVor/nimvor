/* Server → MongoDB → Remote servers: the "Get DB from server" modal.
 *
 * When the modal opens for a saved remote, fetch that remote's databases and
 * fill the dropdown. Loaded only on the MongoDB manager page (CSP forbids
 * inline scripts); the endpoint URL and CSRF token are read from the script
 * tag's data attributes. */
(function () {
    'use strict';
    var tag = document.querySelector('script[data-mongo-remote-databases-url]');
    var modal = document.getElementById('mongo-getdb');
    if (!tag || !modal || !window.WhaCP) { return; }
    var url = tag.getAttribute('data-mongo-remote-databases-url');

    var idIn = modal.querySelector('[data-mongo-getdb-id]');
    var nameEl = modal.querySelector('[data-mongo-getdb-name]');
    var select = modal.querySelector('[data-mongo-getdb-select]');
    var target = modal.querySelector('[data-mongo-getdb-target]');
    var errEl = modal.querySelector('[data-mongo-getdb-error]');
    var okBtn = modal.querySelector('[data-mongo-getdb-ok]');

    function option(value, label) {
        var o = document.createElement('option');
        o.value = value;
        o.textContent = label;
        return o;
    }

    modal.addEventListener('show.bs.modal', function (e) {
        var b = e.relatedTarget;
        if (!b) { return; }
        var id = b.getAttribute('data-id') || '';
        idIn.value = id;
        if (nameEl) { nameEl.textContent = b.getAttribute('data-name') || ''; }
        if (target) { target.value = ''; }
        if (errEl) { errEl.classList.add('d-none'); errEl.textContent = ''; }
        if (okBtn) { okBtn.disabled = true; }
        select.replaceChildren(option('', 'Loading…'));

        WhaCP.fetchJson(url, { method: 'POST', body: JSON.stringify({ id: id }) }).then(function (r) {
            var dbs = (r.data && r.data.databases) || [];
            select.replaceChildren();
            if (!dbs.length) {
                select.appendChild(option('', 'No databases on this server'));
                return;
            }
            select.appendChild(option('', 'Choose a database…'));
            dbs.forEach(function (d) {
                select.appendChild(option(d.name, d.name + (d.size ? '  (' + Math.max(1, Math.round(d.size / 1048576)) + ' MB)' : '')));
            });
            if (okBtn) { okBtn.disabled = false; }
        }).catch(function (err) {
            select.replaceChildren(option('', 'Could not list databases'));
            if (errEl) {
                var d = (err && err.details) || {};
                errEl.textContent = d.message || (err && err.message) || 'The remote server could not be reached.';
                errEl.classList.remove('d-none');
            }
        });
    });

    // Keep the copy button disabled until a database is chosen.
    if (select && okBtn) {
        select.addEventListener('change', function () { okBtn.disabled = select.value === ''; });
    }
})();
