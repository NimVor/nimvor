// WhaCP UI behaviours. No inline scripts are used anywhere (strict CSP).
(function () {
    'use strict';

    var WhaCP = window.WhaCP = window.WhaCP || {};

    WhaCP.csrfToken = function () {
        var m = document.querySelector('meta[name="csrf-token"]');
        return m ? m.getAttribute('content') : '';
    };

    // JSON fetch helper with CSRF header and uniform error handling.
    WhaCP.fetchJson = function (url, options) {
        options = options || {};
        var headers = Object.assign({ 'Accept': 'application/json', 'X-Requested-With': 'XMLHttpRequest' }, options.headers || {});
        if (options.method && options.method !== 'GET') {
            headers['X-CSRF-Token'] = WhaCP.csrfToken();
            headers['Content-Type'] = 'application/json';
        }
        return fetch(url, Object.assign({}, options, { headers: headers, credentials: 'same-origin' }))
            .then(function (res) {
                if (res.status === 401) {
                    window.location.href = '/login';
                    throw new Error('Session ended');
                }
                return res.json().then(function (body) {
                    if (!res.ok || body.success === false) {
                        var err = new Error((body.error && body.error.message) || 'Request failed');
                        err.details = body.error || {};
                        throw err;
                    }
                    return body;
                });
            });
    };

    WhaCP.toast = function (message, variant) {
        var container = document.getElementById('wcp-toasts');
        if (!container || !window.bootstrap) { return; }
        var el = document.createElement('div');
        el.className = 'toast align-items-center border-0 text-bg-' + (variant || 'secondary');
        el.setAttribute('role', 'status');
        el.setAttribute('aria-live', 'polite');
        var wrap = document.createElement('div');
        wrap.className = 'd-flex';
        var body = document.createElement('div');
        body.className = 'toast-body';
        body.textContent = message;
        var close = document.createElement('button');
        close.type = 'button';
        close.className = 'btn-close btn-close-white me-2 m-auto';
        close.setAttribute('data-bs-dismiss', 'toast');
        close.setAttribute('aria-label', 'Close');
        wrap.appendChild(body);
        wrap.appendChild(close);
        el.appendChild(wrap);
        container.appendChild(el);
        var t = new bootstrap.Toast(el, { delay: 5000 });
        el.addEventListener('hidden.bs.toast', function () { el.remove(); });
        t.show();
    };

    // Sidebar toggle (mobile).
    document.addEventListener('click', function (e) {
        var t = e.target.closest('[data-wcp-toggle="sidebar"]');
        if (t) {
            document.getElementById('wcp-sidebar').classList.toggle('is-open');
        }
    });

    // Theme toggle.
    document.addEventListener('click', function (e) {
        if (!e.target.closest('[data-wcp-theme-toggle]')) { return; }
        var root = document.documentElement;
        var next = root.getAttribute('data-bs-theme') === 'dark' ? 'light' : 'dark';
        root.setAttribute('data-bs-theme', next);
        try { localStorage.setItem('wcp-theme', next); } catch (err) { /* ignore */ }
    });

    // Loading state on submit for forms marked data-wcp-busy.
    document.addEventListener('submit', function (e) {
        var form = e.target;
        if (!form.matches('[data-wcp-busy]') || e.defaultPrevented) { return; }
        var btn = form.querySelector('button[type="submit"], button:not([type])');
        if (btn) {
            btn.disabled = true;
            var spin = btn.querySelector('.spinner-border');
            if (spin) { spin.classList.remove('d-none'); }
        }
    });

    // Confirmation modal for destructive forms: data-wcp-confirm="message",
    // optional data-wcp-confirm-word="text the user must type".
    // A button may ask on its own: data-wcp-confirm-submit="message".
    var pendingForm = null;
    var pendingSubmitter = null;
    document.addEventListener('submit', function (e) {
        var form = e.target;
        var btn = e.submitter && e.submitter.matches('[data-wcp-confirm-submit]') ? e.submitter : null;
        if ((!btn && !form.matches('[data-wcp-confirm]')) || form.dataset.wcpConfirmed === '1') { return; }
        e.preventDefault();
        pendingForm = form;
        pendingSubmitter = e.submitter || null;
        var modalEl = document.getElementById('wcp-confirm');
        modalEl.querySelector('[data-wcp-confirm-message]').textContent = btn ? btn.getAttribute('data-wcp-confirm-submit') : form.getAttribute('data-wcp-confirm');
        var word = btn ? btn.getAttribute('data-wcp-confirm-word') : form.getAttribute('data-wcp-confirm-word');
        var typed = modalEl.querySelector('[data-wcp-confirm-typed]');
        var input = modalEl.querySelector('#wcp-confirm-input');
        var ok = modalEl.querySelector('[data-wcp-confirm-ok]');
        input.value = '';
        if (word) {
            typed.classList.remove('d-none');
            modalEl.querySelector('[data-wcp-confirm-word]').textContent = word;
            ok.disabled = true;
        } else {
            typed.classList.add('d-none');
            ok.disabled = false;
        }
        bootstrap.Modal.getOrCreateInstance(modalEl).show();
    }, true);

    document.addEventListener('input', function (e) {
        if (e.target.id !== 'wcp-confirm-input' || !pendingForm) { return; }
        var word = pendingForm.getAttribute('data-wcp-confirm-word') || '';
        document.querySelector('[data-wcp-confirm-ok]').disabled = e.target.value !== word;
    });

    document.addEventListener('click', function (e) {
        if (!e.target.closest('[data-wcp-confirm-ok]') || !pendingForm) { return; }
        var form = pendingForm;
        var submitter = pendingSubmitter;
        pendingForm = null;
        pendingSubmitter = null;
        form.dataset.wcpConfirmed = '1';
        bootstrap.Modal.getInstance(document.getElementById('wcp-confirm')).hide();
        if (form.requestSubmit) { form.requestSubmit(submitter && submitter.form === form ? submitter : undefined); } else { form.submit(); }
    });

    // Mail queue: headers of a message, loaded into a modal.
    document.addEventListener('click', function (e) {
        var btn = e.target.closest('[data-wcp-mail-headers]');
        if (!btn) { return; }
        var modalEl = document.getElementById('wcp-headers');
        var body = modalEl.querySelector('[data-wcp-headers-body]');
        body.replaceChildren();
        var loading = document.createElement('dd');
        loading.className = 'col-12 wcp-muted';
        loading.textContent = 'Loading…';
        body.appendChild(loading);
        bootstrap.Modal.getOrCreateInstance(modalEl).show();
        fetch(btn.getAttribute('data-wcp-mail-headers'), { headers: { Accept: 'application/json' }, credentials: 'same-origin' })
            .then(function (r) { return r.json(); })
            .then(function (d) {
                body.replaceChildren();
                if (!d.success) {
                    loading.textContent = (d.error && d.error.message) || 'The message could not be read.';
                    body.appendChild(loading);
                    return;
                }
                ['from', 'to', 'reply-to', 'subject', 'date', 'message-id'].forEach(function (k) {
                    if (!d.headers[k]) { return; }
                    var dt = document.createElement('dt');
                    dt.className = 'col-sm-3 text-capitalize';
                    dt.textContent = k;
                    var dd = document.createElement('dd');
                    dd.className = 'col-sm-9 text-break';
                    dd.textContent = d.headers[k];
                    body.append(dt, dd);
                });
            })
            .catch(function () { loading.textContent = 'The message could not be read.'; body.replaceChildren(loading); });
    });

    // Settings: the tab named in the address (#dns) opens; switching tabs
    // updates the address so a reload or a saved form comes back to it.
    // (Runs again when a saved form brings the page back.)
    function openSettingsTab() {
        var root = document.querySelector('[data-wcp-settings-tabs]');
        if (!root || !window.bootstrap) { return; }
        var id = (location.hash || '').replace(/[^a-z]/g, '');
        var link = id && root.querySelector('#tab-' + id);
        if (link && !root.querySelector('.tab-pane.show [aria-invalid="true"], .tab-pane.show .is-invalid')) { bootstrap.Tab.getOrCreateInstance(link).show(); }
    }
    document.addEventListener('DOMContentLoaded', function () {
        var root = document.querySelector('[data-wcp-settings-tabs]');
        if (!root) { return; }
        root.addEventListener('shown.bs.tab', function (e) { history.replaceState(history.state, '', e.target.getAttribute('href')); });
        openSettingsTab();
    });
    window.addEventListener('hashchange', openSettingsTab);

    // Live statistics (Monitoring → Live): polled every 3 seconds while the
    // page is visible; every value is set as text.
    (function () {
        var root = document.querySelector('[data-wcp-live]');
        if (!root) { return; }
        var url = root.getAttribute('data-wcp-live');
        var paused = false, timer = null, busy = false;
        var q = function (sel) { return root.querySelector(sel); };
        var set = function (k, v) { var x = q('[data-live="' + k + '"]'); if (x) { x.textContent = v; } };
        var el = function (tag, cls, text) { var x = document.createElement(tag); if (cls) { x.className = cls; } if (text !== undefined) { x.textContent = text; } return x; };
        var bytes = function (n) { var u = ['B', 'KB', 'MB', 'GB', 'TB']; var i = 0; n = Number(n) || 0; while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; } return (i ? n.toFixed(1) : String(Math.round(n))) + ' ' + u[i]; };
        var rate = function (n) { return bytes(n) + '/s'; };
        var pct = function (n) { return (Number(n) || 0).toFixed(1) + '%'; };
        var dur = function (s) { s = Math.floor(s || 0); var d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60); return (d ? d + ' d ' : '') + h + ' h ' + m + ' min'; };
        var row = function (cells) { var tr = el('tr'); cells.forEach(function (c) { tr.appendChild(el('td', c[1] || '', c[0])); }); return tr; };
        var procs = function (body, list) {
            body.replaceChildren();
            (list || []).forEach(function (p) {
                body.appendChild(row([[p.name, 'font-monospace text-break'], [p.user], [String(p.pid), 'text-end'], [pct(p.cpu_percent), 'text-end'], [bytes(p.rss_bytes) + ' (' + pct(p.mem_percent) + ')', 'text-end text-nowrap']]));
            });
        };
        var render = function (d) {
            set('cpu', pct(d.cpu.percent)); set('cpu-sub', d.cpu.threads + ' logical CPUs');
            var m = d.memory;
            set('mem', pct(m.used_percent)); set('mem-sub', bytes(m.used_bytes) + ' of ' + bytes(m.total_bytes));
            set('swap', m.swap_total_bytes ? pct(100 * m.swap_used_bytes / m.swap_total_bytes) : 'none'); set('swap-sub', m.swap_total_bytes ? bytes(m.swap_used_bytes) + ' of ' + bytes(m.swap_total_bytes) : '');
            set('load', [d.load.load1, d.load.load5, d.load.load15].map(function (x) { return (Number(x) || 0).toFixed(2); }).join(' / '));
            set('load-sub', (d.cpu.threads ? (100 * d.load.load1 / d.cpu.threads).toFixed(0) + '% of the CPUs' : ''));
            set('uptime', dur(d.uptime_seconds));
            set('procs', String(d.processes)); set('procs-sub', d.running + ' running · ' + d.threads + ' threads' + (d.zombies ? ' · ' + d.zombies + ' zombie' : ''));
            set('cpu-model', d.cpu.model || '—'); set('cpu-arch', d.cpu.architecture);
            set('cpu-topology', d.cpu.sockets + ' / ' + d.cpu.cores + ' / ' + d.cpu.threads);
            set('cpu-freq', d.cpu.mhz ? Math.round(d.cpu.mhz) + ' MHz' + (d.cpu.max_mhz ? ' (max ' + Math.round(d.cpu.max_mhz) + ' MHz)' : '') : 'not reported');
            var cores = q('[data-live-cores]'); cores.replaceChildren();
            (d.cpu.per_core || []).forEach(function (c) {
                var w = el('div', 'small'); var pr = el('progress'); pr.max = 100; pr.value = c.percent; pr.setAttribute('aria-label', c.cpu + ' ' + pct(c.percent));
                w.append(el('span', 'text-body-secondary', c.cpu + ' '), el('span', '', pct(c.percent)), pr); cores.appendChild(w);
            });
            var net = q('[data-live-net]'); net.replaceChildren();
            (d.interfaces || []).forEach(function (n) {
                net.appendChild(row([[n.name, 'font-monospace'], [rate(n.rx_bytes_per_sec), 'text-end text-nowrap'], [rate(n.tx_bytes_per_sec), 'text-end text-nowrap'], [bytes(n.rx_bytes), 'text-end'], [bytes(n.tx_bytes), 'text-end'], [(n.rx_errors + n.tx_errors) + ' / ' + (n.rx_dropped + n.tx_dropped), 'text-end']]));
            });
            procs(q('[data-live-topcpu]'), d.top_cpu); procs(q('[data-live-topmem]'), d.top_memory);
            var disks = q('[data-live-disks]'); disks.replaceChildren();
            (d.disks || []).forEach(function (k) {
                var tr = row([[k.mount_point || k.mountpoint || '', 'font-monospace'], [k.device || '', 'small text-body-secondary'], [bytes(k.used_bytes), 'text-end'], [bytes(k.free_bytes), 'text-end'], [bytes(k.total_bytes), 'text-end']]);
                var td = el('td'); var pr = el('progress', 'wcp-du-bar'); pr.max = 100; pr.value = k.used_percent; pr.setAttribute('aria-label', pct(k.used_percent)); td.append(pr, el('span', 'small text-body-secondary', ' ' + pct(k.used_percent))); tr.appendChild(td);
                disks.appendChild(tr);
            });
            set('status', '');
            q('[data-live-status]').textContent = 'Updated ' + new Date(d.timestamp).toLocaleTimeString() + ' · every 3 seconds';
        };
        var poll = function () {
            clearTimeout(timer);
            if (paused || document.hidden || busy) { return; }
            busy = true;
            fetch(url, { headers: { Accept: 'application/json' }, credentials: 'same-origin' })
                .then(function (r) { return r.json(); })
                .then(function (d) { if (d.error) { throw new Error(d.error); } render(d); })
                .catch(function (e) { q('[data-live-status]').textContent = 'Not updated: ' + (e.message || 'the request failed') + '. Retrying…'; })
                .finally(function () { busy = false; timer = setTimeout(poll, 3000); });
        };
        document.addEventListener('visibilitychange', function () { if (!document.hidden) { poll(); } });
        var tog = document.querySelector('[data-wcp-live-toggle]');
        if (tog) {
            tog.addEventListener('click', function () {
                paused = !paused;
                tog.setAttribute('aria-pressed', paused ? 'true' : 'false');
                tog.querySelector('span').textContent = paused ? 'Resume' : 'Pause';
                tog.querySelector('i').className = 'bi ' + (paused ? 'bi-play-fill' : 'bi-pause-fill') + ' me-1';
                if (!paused) { poll(); }
            });
        }
        poll();
    })();

    // Website file permissions (Advanced page): check, then offer repair.
    document.addEventListener('click', function (e) {
        var btn = e.target.closest('[data-wcp-perm-check]');
        if (!btn) { return; }
        var box = document.querySelector('[data-wcp-perm-results]');
        var repair = document.querySelector('[data-wcp-perm-repair]');
        var el = function (tag, cls, text) { var x = document.createElement(tag); if (cls) { x.className = cls; } if (text !== undefined) { x.textContent = text; } return x; };
        var names = { owner: 'another owner', group: 'another account\'s group', world_write: 'writable by anyone', setid: 'set-ID bit', unreadable: 'owner cannot read' };
        btn.disabled = true;
        box.hidden = false;
        repair.hidden = true;
        box.replaceChildren(el('span', 'wcp-muted', 'Checking the files…'));
        fetch(btn.getAttribute('data-wcp-perm-check'), { headers: { Accept: 'application/json' }, credentials: 'same-origin' })
            .then(function (r) { return r.json(); })
            .then(function (d) {
                box.replaceChildren();
                if (d.error) { box.appendChild(el('span', 'text-danger-emphasis', d.error)); return; }
                if (!d.issues) {
                    box.appendChild(el('span', 'text-success-emphasis', 'All ' + d.checked + ' files and folders are in order.'));
                    return;
                }
                var parts = Object.keys(d.counts).map(function (k) { return d.counts[k] + ' × ' + (names[k] || k); });
                box.appendChild(el('div', 'fw-medium', d.issues + ' of ' + d.checked + ' files and folders need repair: ' + parts.join(', ') + '.'));
                var ul = el('ul', 'mb-0 mt-1 ps-3 wcp-scroll-400');
                (d.sample || []).forEach(function (s) {
                    var li = el('li', 'text-break');
                    li.append(el('code', '', s.path), document.createTextNode(' ' + s.mode + ' — ' + s.problems.map(function (p) { return names[p] || p; }).join(', ')));
                    ul.appendChild(li);
                });
                box.appendChild(ul);
                if (d.skipped_count) { box.appendChild(el('div', 'wcp-muted mt-1', d.skipped_count + ' link(s) or special file(s) are never changed.')); }
                repair.hidden = false;
            })
            .catch(function () { box.replaceChildren(el('span', 'text-danger-emphasis', 'The check failed.')); })
            .finally(function () { btn.disabled = false; });
    });

    // Email authentication checker (mail domain page): results as a list.
    document.addEventListener('click', function (e) {
        var btn = e.target.closest('[data-wcp-dns-check]');
        if (!btn) { return; }
        var box = document.querySelector('[data-wcp-dns-results]');
        var el = function (tag, cls, text) { var x = document.createElement(tag); if (cls) { x.className = cls; } if (text !== undefined) { x.textContent = text; } return x; };
        var labels = { pass: 'Pass', warning: 'Warning', fail: 'Fail', missing: 'Missing', info: 'Note' };
        var states = { pass: 'is-ok', warning: 'is-warn', fail: 'is-error', missing: 'is-error', info: '' };
        btn.disabled = true;
        box.hidden = false;
        box.replaceChildren(el('div', 'wcp-muted small', 'Checking the public DNS…'));
        fetch(btn.getAttribute('data-wcp-dns-check'), { headers: { Accept: 'application/json' }, credentials: 'same-origin' })
            .then(function (r) { return r.json(); })
            .then(function (d) {
                box.replaceChildren();
                if (d.error || !d.checks) {
                    box.appendChild(el('div', 'text-danger-emphasis small', d.error || 'The check failed.'));
                    return;
                }
                if (d.simulated) {
                    box.appendChild(el('div', 'wcp-muted small', 'Not checked: the agent simulates commands (development mode).'));
                }
                d.checks.forEach(function (c) {
                    var row = el('div', 'border rounded p-2 mb-2 small');
                    var head = el('div', 'd-flex flex-wrap align-items-center gap-2');
                    head.append(el('span', 'fw-semibold', c.label), el('span', 'wcp-state ' + (states[c.status] || ''), labels[c.status] || c.status));
                    row.appendChild(head);
                    row.appendChild(el('div', 'mt-1', c.detail));
                    if (c.found) { var f = el('div', 'text-body-secondary text-break mt-1', 'Found: '); f.appendChild(el('code', '', c.found)); row.appendChild(f); }
                    if (c.expected && c.status !== 'pass') { var x = el('div', 'text-body-secondary text-break', 'Expected: '); x.appendChild(el('code', '', c.expected)); row.appendChild(x); }
                    box.appendChild(row);
                });
            })
            .catch(function () { box.replaceChildren(el('div', 'text-danger-emphasis small', 'The check failed.')); })
            .finally(function () { btn.disabled = false; });
    });

    // Show only the fields that belong to the selected type (website kind,
    // storage type…). data-wcp-kind may list several kinds; inputs in
    // hidden groups are disabled so they are neither validated nor sent.
    function applyKind(select) {
        var kind = select.value;
        var scope = select.form || document;
        scope.querySelectorAll('[data-wcp-kind]').forEach(function (el) {
            var on = el.getAttribute('data-wcp-kind').split(/\s+/).indexOf(kind) >= 0;
            el.classList.toggle('d-none', !on);
            el.querySelectorAll('input, select, textarea').forEach(function (i) { i.disabled = !on; });
        });
    }
    document.addEventListener('change', function (e) {
        if (e.target.matches('[data-wcp-toggle-kind]')) { applyKind(e.target); }
    });
    document.addEventListener('DOMContentLoaded', function () {
        document.querySelectorAll('[data-wcp-toggle-kind]').forEach(applyKind);
    });

    // DNS record modal: one form for adding and editing (buttons carry the
    // record as JSON), with the fields of the chosen type only.
    var dnsHints = {
        A: ['IPv4 address', 'e.g. 192.0.2.10'], AAAA: ['IPv6 address', 'e.g. 2001:db8::10'],
        CNAME: ['Target host', 'Another name: www → @, or a full host ending in a dot.'],
        NS: ['Name server', 'Host name of the name server, e.g. ns1.example.net.'],
        PTR: ['Host name', 'The host this address belongs to, ending in a dot.'],
        MX: ['Mail server', 'Host that receives mail, e.g. mail or mx.example.net.'],
        SRV: ['Target host', 'Host offering the service (or . for none).']
    };
    function dnsFill(form, rec) {
        var set = function (name, v) { form.querySelectorAll('[name="' + name + '"]').forEach(function (el) { el.value = v === undefined || v === null ? '' : String(v); }); };
        var type = rec.type || 'A';
        set('_record', rec.id || 0); set('type', type); set('name', rec.name || '@'); set('ttl', rec.ttl || 0);
        set('priority', rec.priority !== undefined ? rec.priority : 10); set('weight', rec.weight || 0); set('port', rec.port || 0);
        set('srv_service', ''); set('srv_protocol', '');
        if (type === 'CAA') {
            var v = String(rec.value || ''), sp = v.indexOf(' ');
            set('caa_flags', rec.priority === 128 ? 128 : 0);
            set('caa_tag', sp > 0 ? v.slice(0, sp) : 'issue'); set('caa_value', sp > 0 ? v.slice(sp + 1) : v);
            set('value', '');
        } else {
            set('value', rec.value || ''); set('caa_flags', 0); set('caa_tag', 'issue'); set('caa_value', '');
        }
        form.querySelectorAll('.is-invalid').forEach(function (el) { el.classList.remove('is-invalid'); });
        form.querySelectorAll('.invalid-feedback').forEach(function (el) { el.remove(); });
        dnsType(form);
    }
    function dnsType(form) {
        var sel = form.querySelector('[name="type"]');
        applyKind(sel);
        var h = dnsHints[sel.value] || ['Value', ''];
        var label = form.querySelector('[data-wcp-dns-value-label]'), hint = form.querySelector('[data-wcp-dns-hint]');
        if (label) { label.textContent = h[0]; }
        if (hint) { hint.textContent = h[1]; }
    }
    function dnsOpen(action, rec, title) {
        var modalEl = document.getElementById('dns-record');
        if (!modalEl || !window.bootstrap) { return; }
        var form = modalEl.querySelector('[data-wcp-dns-form]');
        form.action = action || form.getAttribute('data-store');
        if (rec) { dnsFill(form, rec); }
        modalEl.querySelector('.modal-title').textContent = title;
        modalEl.querySelector('[data-wcp-dns-shortcuts]').classList.toggle('d-none', title !== 'Add record');
        window.bootstrap.Modal.getOrCreateInstance(modalEl).show();
    }
    document.addEventListener('click', function (e) {
        var b = e.target.closest && e.target.closest('[data-wcp-dns-edit], [data-wcp-dns-new], [data-wcp-dns-preset]');
        if (!b) { return; }
        var rec;
        try { rec = JSON.parse(b.getAttribute('data-wcp-dns-edit') || b.getAttribute('data-wcp-dns-preset') || '{}'); } catch (x) { rec = {}; }
        if (b.hasAttribute('data-wcp-dns-preset')) {
            var form = b.closest('form');
            rec.id = 0;
            dnsFill(form, rec);
            var first = form.querySelector('[name="value"]:not([disabled]), [name="caa_value"]:not([disabled])');
            if (first) { first.focus(); }
            return;
        }
        if (b.hasAttribute('data-wcp-dns-new')) { rec = {type: 'A', name: '@', ttl: 0, id: 0}; }
        dnsOpen(b.getAttribute('data-wcp-dns-action'), rec, b.hasAttribute('data-wcp-dns-new') ? 'Add record' : 'Edit record');
    });
    document.addEventListener('change', function (e) {
        if (e.target.matches('[data-wcp-dns-form] [name="type"]')) { dnsType(e.target.form); }
    });
    document.addEventListener('DOMContentLoaded', function () {
        var modalEl = document.getElementById('dns-record');
        if (!modalEl) { return; }
        dnsType(modalEl.querySelector('[data-wcp-dns-form]'));
        // After a refused save the form opens again with what was typed.
        if (modalEl.hasAttribute('data-wcp-dns-reopen')) {
            var editing = modalEl.querySelector('[name="_record"]').value !== '0';
            dnsOpen(modalEl.getAttribute('data-wcp-dns-reopen'), null, editing ? 'Edit record' : 'Add record');
        }
    });

    // Copy-to-clipboard buttons: data-wcp-copy="<selector>".
    document.addEventListener('click', function (e) {
        var b = e.target.closest('[data-wcp-copy]');
        if (!b) { return; }
        var src = document.querySelector(b.getAttribute('data-wcp-copy'));
        if (src && navigator.clipboard) {
            navigator.clipboard.writeText((src.matches('input, textarea') ? src.value : src.textContent).trim()).then(function () { WhaCP.toast('Copied to the clipboard.', 'success'); });
        }
    });

    // Countdowns to an ISO deadline: data-wcp-countdown="2026-01-01T00:00:00Z".
    // The page reloads when the deadline passes (e.g. a change was undone).
    document.addEventListener('DOMContentLoaded', function () {
        document.querySelectorAll('[data-wcp-countdown]').forEach(function (el) {
            var end = Date.parse(el.getAttribute('data-wcp-countdown'));
            if (isNaN(end)) { return; }
            var tick = function () {
                // Stops once its page was swapped away.
                if (!el.isConnected) { clearInterval(timer); return; }
                var s = Math.max(0, Math.round((end - Date.now()) / 1000));
                el.textContent = Math.floor(s / 60) + ':' + String(s % 60).padStart(2, '0');
                if (s === 0) { clearInterval(timer); setTimeout(function () { window.location.reload(); }, 3000); }
            };
            var timer = setInterval(tick, 1000);
            tick();
        });
    });

    // Auto-submit selects.
    document.addEventListener('change', function (e) {
        if (e.target.matches('[data-wcp-autosubmit]')) { var f = e.target.form; if (f.requestSubmit) { f.requestSubmit(); } else { f.submit(); } }
    });

    // Apply widths from data attributes (inline styles are blocked by CSP).
    WhaCP.applyWidths = function (root) {
        (root || document).querySelectorAll('[data-width]').forEach(function (el) {
            var w = parseFloat(el.getAttribute('data-width'));
            if (!isNaN(w)) { el.style.width = Math.max(0, Math.min(100, w)) + '%'; }
        });
    };
    document.addEventListener('DOMContentLoaded', function () { WhaCP.applyWidths(); });

    // Background job viewer: polls /jobs/<id> and streams the log.
    WhaCP.showJob = function (jobId) {
        var modalEl = document.getElementById('wcp-job');
        if (!modalEl || !/^[a-f0-9]{16}$/.test(jobId)) { return; }
        var logEl = modalEl.querySelector('[data-wcp-job-log]');
        var statusEl = modalEl.querySelector('[data-wcp-job-status]');
        var reloadBtn = modalEl.querySelector('[data-wcp-job-reload]');
        logEl.textContent = '';
        reloadBtn.classList.add('d-none');
        var offset = 0;
        var stopped = false;
        // Closed before the task ended: keep following it quietly and say
        // when it is done. Closed after: reload, so the page shows the
        // result (e.g. a component now installed).
        var quiet = false;
        var done = false;
        // A newer job shown in the same window replaces this one.
        var run = WhaCP.jobRun = (WhaCP.jobRun || 0) + 1;
        var classes = { queued: 'text-bg-secondary', running: 'text-bg-info', succeeded: 'text-bg-success', failed: 'text-bg-danger' };
        function setStatus(s) {
            statusEl.textContent = s;
            statusEl.className = 'badge ms-2 ' + (classes[s] || 'text-bg-secondary');
        }
        function poll() {
            if (stopped || run !== WhaCP.jobRun) { return; }
            WhaCP.fetchJson('/jobs/' + jobId + '?offset=' + offset).then(function (res) {
                if (stopped || run !== WhaCP.jobRun) { return; }
                if (res.log) {
                    logEl.textContent += res.log;
                    logEl.scrollTop = logEl.scrollHeight;
                }
                offset = res.offset || offset;
                setStatus(res.job ? res.job.status : 'unknown');
                if (res.done) {
                    done = true;
                    reloadBtn.classList.remove('d-none');
                    var ok = res.job.status === 'succeeded';
                    var msg = ok ? 'Task completed.' : 'Task failed. See the log for details.';
                    WhaCP.toast(quiet ? msg + ' Reload the page to see the result.' : msg, ok ? 'success' : 'danger');
                    return;
                }
                setTimeout(poll, 1000);
            }).catch(function (err) {
                logEl.textContent += '\n[panel] ' + err.message + '\n';
                setTimeout(poll, 3000);
            });
        }
        modalEl.addEventListener('hidden.bs.modal', function () {
            if (done) { stopped = true; window.location.reload(); } else { quiet = true; }
        }, { once: true });
        bootstrap.Modal.getOrCreateInstance(modalEl).show();
        setStatus('queued');
        poll();
    };
    document.addEventListener('click', function (e) {
        var t = e.target.closest('[data-wcp-job-open]');
        if (t) { e.preventDefault(); WhaCP.showJob(t.getAttribute('data-wcp-job-open')); }
        if (e.target.closest('[data-wcp-job-reload]')) { window.location.reload(); }
    });
    document.addEventListener('DOMContentLoaded', function () {
        var auto = document.querySelector('[data-wcp-autojob]');
        if (auto) { WhaCP.showJob(auto.getAttribute('data-wcp-autojob')); }
    });

    // Notification bell: refresh the unread count every minute while the
    // tab is visible. The request does not count as activity, so an idle
    // session still times out.
    document.addEventListener('DOMContentLoaded', function () {
        var bell = document.querySelector('[data-wcp-bell]');
        if (!bell) { return; }
        var badge = bell.querySelector('[data-wcp-bell-count]');
        var url = bell.getAttribute('data-wcp-bell-url');
        function refresh() {
            if (document.hidden) { return; }
            WhaCP.fetchJson(url).then(function (res) {
                var n = res.unread || 0;
                badge.textContent = n > 99 ? '99+' : String(n);
                badge.classList.toggle('d-none', n === 0);
                bell.setAttribute('aria-label', n > 0 ? 'Notifications (' + n + ' unread)' : 'Notifications');
            }).catch(function () { /* keep the last count */ });
        }
        if (WhaCP.bellTimer) { return; }
        WhaCP.bellTimer = setInterval(function () {
            var b = document.querySelector('[data-wcp-bell]');
            if (b !== bell) { bell = b; if (!b) { return; } badge = b.querySelector('[data-wcp-bell-count]'); }
            refresh();
        }, 60000);
        document.addEventListener('visibilitychange', refresh);
    });

    // Packages page: "Install" adds a component to a queue without leaving
    // the page. The agent installs queued components one after another; the
    // queue window shows each one's state and the log of the one running,
    // and each row turns into "Installed" when its component is done.
    document.addEventListener('DOMContentLoaded', function () {
        var list = document.querySelector('[data-wcp-pkg-list]');
        var modalEl = document.getElementById('wcp-pkg-queue');
        if (!list || !modalEl) { return; }
        var itemsEl = modalEl.querySelector('[data-wcp-pkg-items]');
        var logEl = modalEl.querySelector('[data-wcp-pkg-log]');
        var logNameEl = modalEl.querySelector('[data-wcp-pkg-logname]');
        var showBtn = document.querySelector('[data-wcp-pkg-show]');
        var countEl = document.querySelector('[data-wcp-pkg-count]');
        var queue = [];
        var timer = null;
        var badges = { queued: 'text-bg-secondary', running: 'text-bg-info', succeeded: 'text-bg-success', failed: 'text-bg-danger' };
        var labels = { queued: 'Queued', running: 'Installing…', succeeded: 'Installed', failed: 'Failed' };

        function el(tag, cls, text) {
            var e = document.createElement(tag);
            if (cls) { e.className = cls; }
            if (text !== undefined) { e.textContent = text; }
            return e;
        }
        function row(name) {
            var found = null;
            list.querySelectorAll('[data-wcp-pkg]').forEach(function (r) { if (r.getAttribute('data-wcp-pkg') === name) { found = r; } });
            return found;
        }
        function pending(it) { return it.status === 'queued' || it.status === 'running'; }
        function setRow(it, version) {
            var r = row(it.name);
            if (!r) { return; }
            var state = r.querySelector('[data-wcp-pkg-state]');
            state.textContent = '';
            if (it.status === 'failed') {
                state.appendChild(el('span', 'badge text-bg-danger me-2', 'Failed'));
                state.appendChild(it.form);
                it.form.querySelector('button').textContent = 'Retry';
                return;
            }
            var b = el('span', 'badge ' + badges[it.status]);
            if (it.status === 'succeeded') { b.appendChild(el('i', 'bi bi-check-circle')); b.appendChild(document.createTextNode(' ')); }
            if (it.status === 'running') { b.appendChild(el('span', 'spinner-border spinner-border-sm me-1')); }
            b.appendChild(document.createTextNode(labels[it.status]));
            state.appendChild(b);
            if (version) { state.appendChild(el('div', 'small text-body-secondary', version)); }
        }
        // The step a component is at, from its job log (the agent writes
        // these lines); on failure, the agent's diagnosis.
        function phase(it) {
            var log = it.log || '';
            if (it.status === 'failed') {
                var d = log.match(/Diagnosis: ([^\n]+)/);
                return d ? d[1] : '';
            }
            if (it.status !== 'running') { return ''; }
            var steps = [
                [/Checking [^\n]*…\n(?![\s\S]*Attempt \d)/, 'Running health check…'],
                [/Automatic recovery: [^\n]*\n(?![\s\S]*(Checking|succeeded on attempt))/, 'Recovering after a failed attempt…'],
                [/Preparing repositories[^\n]*\n(?![\s\S]*(Checking|Attempt \d))/, 'Preparing repositories…'],
                [/Waiting for (another package operation|a package transaction)[^\n]*\n$/, 'Waiting for another package operation…']
            ];
            for (var i = 0; i < steps.length; i++) {
                if (steps[i][0].test(log)) {
                    var a = log.match(/Attempt (\d)\/(\d) failed[^\n]*\n(?![\s\S]*succeeded on attempt)/g);
                    return steps[i][1] + (a && i !== 0 ? ' (attempt ' + a.length + ' of 3 failed)' : '');
                }
            }
            return 'Installing package…';
        }
        function render() {
            itemsEl.textContent = '';
            var shown = null;
            queue.forEach(function (it) {
                var li = el('li', 'list-group-item d-flex align-items-center gap-2');
                li.setAttribute('data-wcp-pkg-item', it.name);
                var name = el('div', 'me-auto');
                name.appendChild(el('span', 'fw-semibold', it.name));
                var ph = phase(it);
                if (ph) { name.appendChild(el('div', 'small ' + (it.status === 'failed' ? 'text-danger-emphasis' : 'text-body-secondary'), ph)); }
                li.appendChild(name);
                li.appendChild(el('span', 'badge ' + badges[it.status], labels[it.status]));
                itemsEl.appendChild(li);
                if (it.status === 'running' || (!shown && it.log)) { shown = it; }
            });
            if (shown) {
                logNameEl.textContent = shown.name;
                logEl.textContent = shown.log;
                logEl.scrollTop = logEl.scrollHeight;
            }
            countEl.textContent = String(queue.filter(pending).length);
            showBtn.classList.toggle('d-none', queue.length === 0);
        }
        function finished(it) {
            if (it.status !== 'succeeded') {
                setRow(it);
                var why = phase(it);
                WhaCP.toast(it.name + ' was not installed' + (why ? ': ' + why : '') + '. The details are in the queue window.', 'danger');
                return;
            }
            WhaCP.fetchJson(list.getAttribute('data-url')).then(function (res) {
                var p = (res.packages || []).filter(function (x) { return x.name === it.name; })[0];
                setRow(it, p && p.installed ? p.version : '');
            }).catch(function () { setRow(it); });
            WhaCP.toast(it.name + ' installed.', 'success');
        }
        function poll() {
            var open = queue.filter(pending);
            if (open.length === 0) { clearInterval(timer); timer = null; return; }
            open.forEach(function (it) {
                if (it.busy) { return; }
                it.busy = true;
                WhaCP.fetchJson('/jobs/' + it.job + '?offset=' + it.offset).then(function (res) {
                    it.busy = false;
                    if (res.log) { it.log += res.log; }
                    it.offset = res.offset || it.offset;
                    var s = res.job ? res.job.status : 'running';
                    if (res.done) { it.status = s === 'succeeded' ? 'succeeded' : 'failed'; finished(it); }
                    else if (s !== it.status) { it.status = s === 'queued' ? 'queued' : 'running'; setRow(it); }
                    render();
                }).catch(function () { it.busy = false; });
            });
        }
        showBtn.addEventListener('click', function () { bootstrap.Modal.getOrCreateInstance(modalEl).show(); });
        list.addEventListener('submit', function (e) {
            var form = e.target.closest('[data-wcp-pkg-install]');
            if (!form) { return; }
            e.preventDefault();
            var name = form.querySelector('input[name=name]').value;
            if (queue.some(function (it) { return it.name === name && pending(it); })) { return; }
            var button = form.querySelector('button');
            button.disabled = true;
            WhaCP.fetchJson(list.getAttribute('data-install-url'), { method: 'POST', body: JSON.stringify({ name: name }) }).then(function (res) {
                button.disabled = false;
                var it = { name: name, job: res.job_id, status: 'queued', offset: 0, log: '', form: form };
                queue = queue.filter(function (x) { return x.name !== name; });
                queue.push(it);
                setRow(it);
                render();
                bootstrap.Modal.getOrCreateInstance(modalEl).show();
                if (!timer) { timer = setInterval(poll, 1000); }
                poll();
            }).catch(function (err) {
                button.disabled = false;
                WhaCP.toast(err.message, 'danger');
            });
        });
    });
    // Website dashboard: "Search features…" filters the action list as a
    // combobox (arrows move, Enter opens, Escape closes).
    document.addEventListener('DOMContentLoaded', function () {
        var box = document.querySelector('[data-wcp-feature-search]');
        if (!box) { return; }
        var input = box.querySelector('[data-wcp-feature-input]');
        var list = box.querySelector('[data-wcp-feature-results]');
        var none = list.querySelector('[data-wcp-feature-none]');
        var items = Array.prototype.slice.call(list.querySelectorAll('[data-wcp-feature]'));
        var shown = [];
        var focus = -1;
        function setFocus(i) {
            shown.forEach(function (el, n) { el.classList.toggle('is-focused', n === i); el.setAttribute('aria-selected', n === i ? 'true' : 'false'); });
            focus = i;
            if (i >= 0) { input.setAttribute('aria-activedescendant', shown[i].id); shown[i].scrollIntoView({ block: 'nearest' }); } else { input.removeAttribute('aria-activedescendant'); }
        }
        function update() {
            var words = input.value.toLowerCase().trim().split(/\s+/).filter(Boolean);
            var ranked = [];
            items.forEach(function (el) {
                var hay = el.getAttribute('data-wcp-feature');
                var label = el.getAttribute('data-wcp-feature-label');
                var on = words.length > 0 && words.every(function (w) { return hay.indexOf(w) >= 0; });
                el.classList.toggle('d-none', !on);
                if (on) {
                    // Matches in the name first, names starting with the query before others.
                    var rank = words.every(function (w) { return label.indexOf(w) >= 0; }) ? (label.indexOf(words[0]) === 0 ? 0 : 1) : 2;
                    ranked.push({ el: el, rank: rank });
                }
            });
            ranked.sort(function (a, b) { return a.rank - b.rank; });
            shown = ranked.map(function (r) { return r.el; });
            shown.forEach(function (el) { list.insertBefore(el, none); });
            var open = words.length > 0;
            list.classList.toggle('d-none', !open);
            none.classList.toggle('d-none', !open || shown.length > 0);
            input.setAttribute('aria-expanded', open ? 'true' : 'false');
            setFocus(shown.length > 0 ? 0 : -1);
        }
        input.addEventListener('input', update);
        input.addEventListener('keydown', function (e) {
            if (e.key === 'ArrowDown' && shown.length) { e.preventDefault(); setFocus((focus + 1) % shown.length); }
            else if (e.key === 'ArrowUp' && shown.length) { e.preventDefault(); setFocus((focus - 1 + shown.length) % shown.length); }
            else if (e.key === 'Enter' && focus >= 0) { e.preventDefault(); window.location.href = shown[focus].href; }
            else if (e.key === 'Escape') { input.value = ''; update(); }
        });
        document.addEventListener('click', function (e) {
            if (!box.contains(e.target)) { list.classList.add('d-none'); input.setAttribute('aria-expanded', 'false'); }
        });
        input.addEventListener('focus', function () { if (input.value.trim() !== '') { update(); } });
        // "/" focuses the search, like many web apps.
        document.addEventListener('keydown', function (e) {
            if (e.key === '/' && !/^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement.tagName) && !document.activeElement.isContentEditable) {
                e.preventDefault(); input.focus();
            }
        });
    });

    // A link to #some-form opens that collapsed form (e.g. "+ Create
    // database" from the dashboard) and focuses its first field.
    function openFromHash() {
        var id = decodeURIComponent(window.location.hash.slice(1));
        if (!id || !/^[A-Za-z][\w-]*$/.test(id) || !window.bootstrap) { return; }
        var el = document.getElementById(id);
        if (!el) { return; }
        if (el.classList.contains('collapse')) {
            bootstrap.Collapse.getOrCreateInstance(el, { toggle: false }).show();
        }
        if (el.tagName === 'DETAILS') { el.open = true; }
        // A form window (#new-database…): open it.
        if (el.classList.contains('modal')) { bootstrap.Modal.getOrCreateInstance(el).show(); return; }
        // A tab pane (#db-users…): show its tab.
        if (el.classList.contains('tab-pane')) {
            var tab = document.querySelector('[data-bs-toggle="tab"][data-bs-target="#' + id + '"]');
            if (tab) { bootstrap.Tab.getOrCreateInstance(tab).show(); }
        }
        setTimeout(function () {
            el.scrollIntoView({ block: 'start', behavior: 'auto' });
            var f = el.querySelector('input:not([type=hidden]):not([disabled]), select, textarea');
            if (f && el.classList.contains('collapse')) { f.focus({ preventScroll: true }); }
        }, 50);
    }
    document.addEventListener('DOMContentLoaded', openFromHash);
    window.addEventListener('hashchange', openFromHash);
    // Focus the first field when a form panel or window is opened.
    ['shown.bs.collapse', 'shown.bs.modal'].forEach(function (ev) {
        document.addEventListener(ev, function (e) {
            if (!e.target.matches('[data-wcp-form-panel]')) { return; }
            var f = e.target.querySelector('input:not([type=hidden]):not([disabled]):not([readonly]), select, textarea');
            if (f) { f.focus(); }
        });
    });
    // A form window marked data-wcp-modal-open (a refused save, shown again
    // with its errors) opens with the page.
    document.addEventListener('DOMContentLoaded', function () {
        var m = document.querySelector('.modal[data-wcp-modal-open]');
        if (m && window.bootstrap) { bootstrap.Modal.getOrCreateInstance(m).show(); }
    });

    // Table filter: <input data-wcp-filter="#table-id"> hides rows whose
    // text does not contain every typed word.
    document.addEventListener('input', function (e) {
        var sel = e.target.getAttribute && e.target.getAttribute('data-wcp-filter');
        if (!sel) { return; }
        var table = document.querySelector(sel);
        if (!table) { return; }
        var words = e.target.value.toLowerCase().trim().split(/\s+/).filter(Boolean);
        var visible = 0;
        table.querySelectorAll('tbody tr:not([data-wcp-filter-empty])').forEach(function (tr) {
            var on = words.every(function (w) { return tr.textContent.toLowerCase().indexOf(w) >= 0; });
            tr.classList.toggle('d-none', !on);
            if (on) { visible++; }
        });
        var empty = table.querySelector('[data-wcp-filter-empty]');
        if (empty) { empty.classList.toggle('d-none', visible > 0); }
    });
    // Shortcut buttons fill a form: data-wcp-fill='{"#field-id":"value"}'.
    // Fields not named keep their value; the last filled field gets focus
    // unless the value field is still empty.
    document.addEventListener('click', function (e) {
        var btn = e.target.closest('[data-wcp-fill]');
        if (!btn) { return; }
        var map;
        try { map = JSON.parse(btn.getAttribute('data-wcp-fill')); } catch (err) { return; }
        var last = null;
        Object.keys(map).forEach(function (sel) {
            var el = document.querySelector(sel);
            if (!el) { return; }
            el.value = map[sel];
            el.dispatchEvent(new Event('change', { bubbles: true }));
            last = el;
        });
        var form = last && last.form;
        var value = form && form.querySelector('[name=value]');
        if (value && value.value === '') { value.focus(); } else if (last) { last.focus(); }
    });
    // Website cards (or rows): search, sort and status filter on the page.
    function applyCards(container) {
        var box = document.querySelector('[data-wcp-cards-toolbar]');
        if (!container || !box) { return; }
        var q = (box.querySelector('[data-wcp-cards-search]') || {}).value || '';
        var sort = (box.querySelector('[data-wcp-cards-sort]') || {}).value || 'created';
        var status = (box.querySelector('[data-wcp-cards-status]') || {}).value || '';
        var words = q.toLowerCase().trim().split(/\s+/).filter(Boolean);
        var cards = Array.prototype.slice.call(container.querySelectorAll('[data-wcp-card]'));
        var shown = 0;
        cards.sort(function (a, b) {
            if (sort === 'name') { return a.dataset.name.localeCompare(b.dataset.name); }
            return (b.dataset.created || '').localeCompare(a.dataset.created || '');
        });
        cards.forEach(function (c) {
            var on = (status === '' || c.dataset.status === status) && words.every(function (w) { return c.dataset.search.indexOf(w) >= 0; });
            c.classList.toggle('d-none', !on);
            if (on) { shown++; }
            c.parentNode.appendChild(c);
        });
        var empty = container.querySelector('[data-wcp-cards-empty]');
        if (empty) { empty.classList.toggle('d-none', shown > 0); empty.parentNode.appendChild(empty); }
    }
    ['input', 'change'].forEach(function (ev) {
        document.addEventListener(ev, function (e) {
            var t = e.target;
            var sel = t.getAttribute && (t.getAttribute('data-wcp-cards-search') || t.getAttribute('data-wcp-cards-sort') || t.getAttribute('data-wcp-cards-status'));
            if (sel) { applyCards(document.querySelector(sel)); }
        });
    });
    document.addEventListener('DOMContentLoaded', function () {
        var c = document.querySelector('[data-wcp-cards]');
        if (c) { applyCards(c); }
    });
    // A switch that submits its form (through the confirmation, if any);
    // cancelling the confirmation puts the switch back.
    document.addEventListener('change', function (e) {
        var sw = e.target;
        if (!sw.matches || !sw.matches('[data-wcp-switch-submit]')) { return; }
        var form = sw.form;
        if (form.requestSubmit) { form.requestSubmit(); } else { form.submit(); }
    });
    document.addEventListener('hidden.bs.modal', function (e) {
        if (e.target.id !== 'wcp-confirm') { return; }
        document.querySelectorAll('[data-wcp-switch-submit]').forEach(function (sw) {
            if (sw.form && sw.form.dataset.wcpConfirmed !== '1') { sw.checked = sw.defaultChecked; }
        });
    });

    // Row menus (<details> "⋮", popovers): one open at a time, closed by a
    // click elsewhere or Escape.
    document.addEventListener('click', function (e) {
        document.querySelectorAll('details[data-wcp-menu][open]').forEach(function (d) {
            if (!d.contains(e.target)) { d.open = false; }
        });
    });
    document.addEventListener('keydown', function (e) {
        if (e.key !== 'Escape') { return; }
        document.querySelectorAll('details[data-wcp-menu][open]').forEach(function (d) { d.open = false; });
    });
    // Open menus are placed against the window, so a table's scroll box
    // does not cut them off; they close when the page scrolls.
    function placeMenu(d) {
        var menu = d.querySelector('.wcp-dropmenu, .wcp-popover');
        var sum = d.querySelector('summary');
        if (!menu || !sum) { return null; }
        var r = sum.getBoundingClientRect();
        menu.style.position = 'fixed';
        menu.style.top = (r.bottom + 4) + 'px';
        menu.style.right = 'auto';
        var w = menu.offsetWidth;
        menu.style.left = Math.max(8, Math.min(r.right - w, window.innerWidth - w - 8)) + 'px';
        var h = menu.offsetHeight;
        if (r.bottom + 4 + h > window.innerHeight - 8 && r.top - h - 4 > 8) { menu.style.top = (r.top - h - 4) + 'px'; }
        return menu;
    }
    document.addEventListener('toggle', function (e) {
        var d = e.target;
        if (!d.matches || !d.matches('details[data-wcp-menu]') || !d.open) { return; }
        var menu = placeMenu(d);
        var f = menu && menu.querySelector('input:not([type=hidden]):not([type=file]), select');
        if (f) { f.focus({ preventScroll: true }); }
    }, true);
    // Menus close when the page scrolls; forms (popovers) follow it.
    window.addEventListener('scroll', function () {
        document.querySelectorAll('details[data-wcp-menu][open]').forEach(function (d) {
            if (d.querySelector('.wcp-popover')) { placeMenu(d); } else { d.open = false; }
        });
    }, true);

    // Data tables: <div data-wcp-dt data-page-size="10"> with a search box
    // [data-wcp-dt-search], filter buttons [data-wcp-dt-seg] (data-key,
    // data-value; rows carry data-dt-KEY), selects [data-wcp-dt-select=KEY],
    // rows tr[data-wcp-row], an empty row [data-wcp-dt-empty] and a pager
    // [data-wcp-dt-pager]. Everything stays in the page (no requests).
    function DataTable(box) {
        var self = this;
        this.box = box;
        this.rows = Array.prototype.slice.call(box.querySelectorAll('tr[data-wcp-row]'));
        this.page = 1;
        this.key = 'wcp-dt-size:' + window.location.pathname + ':' + (box.id || '');
        this.size = parseInt(box.getAttribute('data-page-size') || '10', 10);
        try { var saved = parseInt(localStorage.getItem(this.key) || '', 10); if (saved > 0) { this.size = saved; } } catch (err) { /* no storage */ }
        this.filters = {};
        box.querySelectorAll('[data-wcp-dt-seg].active').forEach(function (b) { self.filters[b.getAttribute('data-key')] = b.getAttribute('data-value'); });
        box.querySelectorAll('[data-wcp-dt-select]').forEach(function (sel) { self.filters[sel.getAttribute('data-wcp-dt-select')] = sel.value; });
        box.addEventListener('input', function (e) { if (e.target.matches('[data-wcp-dt-search]')) { self.page = 1; self.render(); } });
        box.addEventListener('change', function (e) {
            if (e.target.matches('[data-wcp-dt-select]')) { self.filters[e.target.getAttribute('data-wcp-dt-select')] = e.target.value; self.page = 1; self.render(); }
            if (e.target.matches('[data-wcp-dt-size]')) {
                self.size = parseInt(e.target.value, 10); self.page = 1;
                try { localStorage.setItem(self.key, String(self.size)); } catch (err) { /* no storage */ }
                self.render();
            }
            if (e.target.matches('[data-wcp-dt-goto]')) { self.page = parseInt(e.target.value, 10) || 1; self.render(); }
        });
        box.addEventListener('click', function (e) {
            var seg = e.target.closest('[data-wcp-dt-seg]');
            if (seg) {
                seg.parentNode.querySelectorAll('[data-wcp-dt-seg]').forEach(function (b) { b.classList.toggle('active', b === seg); b.setAttribute('aria-pressed', b === seg ? 'true' : 'false'); });
                self.filters[seg.getAttribute('data-key')] = seg.getAttribute('data-value');
                self.page = 1; self.render();
            }
            var pg = e.target.closest('[data-wcp-dt-page]');
            if (pg) { self.page = parseInt(pg.getAttribute('data-wcp-dt-page'), 10); self.render(); }
        });
        this.render();
    }
    DataTable.prototype.render = function () {
        var self = this;
        var search = this.box.querySelector('[data-wcp-dt-search]') || (this.box.id ? document.querySelector('[data-wcp-dt-search-for~="#' + this.box.id + '"]') : null);
        var words = search ? search.value.toLowerCase().trim().split(/\s+/).filter(Boolean) : [];
        var match = this.rows.filter(function (tr) {
            var hay = (tr.getAttribute('data-search') || tr.textContent).toLowerCase();
            if (!words.every(function (w) { return hay.indexOf(w) >= 0; })) { return false; }
            return Object.keys(self.filters).every(function (k) {
                var v = self.filters[k];
                return v === '' || v === undefined || (' ' + (tr.getAttribute('data-dt-' + k) || '') + ' ').indexOf(' ' + v + ' ') >= 0;
            });
        });
        var pages = Math.max(1, Math.ceil(match.length / this.size));
        this.page = Math.min(Math.max(1, this.page), pages);
        var from = (this.page - 1) * this.size, to = from + this.size;
        this.rows.forEach(function (tr) { tr.classList.add('d-none'); });
        match.forEach(function (tr, i) { tr.classList.toggle('d-none', i < from || i >= to); });
        var empty = this.box.querySelector('[data-wcp-dt-empty]');
        if (empty) { empty.classList.toggle('d-none', match.length > 0); }
        var pager = this.box.querySelector('[data-wcp-dt-pager]');
        if (!pager) { return; }
        pager.textContent = '';
        var el = function (tag, cls, text) { var x = document.createElement(tag); if (cls) { x.className = cls; } if (text !== undefined) { x.textContent = text; } return x; };
        var btn = function (label, page, disabled, active, aria) {
            var b = el('button', 'btn btn-sm btn-outline-secondary' + (active ? ' active' : ''), label);
            b.type = 'button'; b.disabled = disabled;
            b.setAttribute('data-wcp-dt-page', String(page));
            if (aria) { b.setAttribute('aria-label', aria); }
            if (active) { b.setAttribute('aria-current', 'page'); }
            return b;
        };
        pager.appendChild(btn('\u2039', this.page - 1, this.page === 1, false, 'Previous page'));
        var start = Math.max(1, Math.min(this.page - 2, pages - 4)), end = Math.min(pages, start + 4);
        for (var p = start; p <= end; p++) { pager.appendChild(btn(String(p), p, false, p === this.page, 'Page ' + p)); }
        pager.appendChild(btn('\u203a', this.page + 1, this.page === pages, false, 'Next page'));
        var sel = el('select', 'form-select form-select-sm');
        sel.setAttribute('data-wcp-dt-size', ''); sel.setAttribute('aria-label', 'Rows per page');
        [10, 20, 50, 100].forEach(function (n) { var o = el('option', '', n + ' / page'); o.value = String(n); o.selected = n === self.size; sel.appendChild(o); });
        pager.appendChild(sel);
        var gl = el('label', 'd-inline-flex align-items-center gap-1', 'Goto');
        var gi = el('input', 'form-control form-control-sm');
        gi.type = 'number'; gi.min = '1'; gi.max = String(pages); gi.value = String(this.page);
        gi.setAttribute('data-wcp-dt-goto', '');
        gl.appendChild(gi);
        pager.appendChild(gl);
        pager.appendChild(el('span', 'wcp-pager-total', 'Total ' + match.length));
    };
    document.addEventListener('DOMContentLoaded', function () {
        document.querySelectorAll('[data-wcp-dt]').forEach(function (box) { box.wcpTable = new DataTable(box); });
    });
    // A search box outside the tables it filters: data-wcp-dt-search-for="#a #b".
    document.addEventListener('input', function (e) {
        var ids = e.target.getAttribute && e.target.getAttribute('data-wcp-dt-search-for');
        if (!ids) { return; }
        ids.split(/\s+/).forEach(function (id) {
            var box = id && document.querySelector(id);
            if (box && box.wcpTable) { box.wcpTable.page = 1; box.wcpTable.render(); }
        });
    });

    // "Generate" fills a password field (and its confirmation) with a
    // strong random password and shows it so it can be copied.
    document.addEventListener('click', function (e) {
        var btn = e.target.closest('[data-wcp-genpass]');
        if (!btn) { return; }
        var chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789-_.!@#%';
        var bytes = new Uint32Array(20);
        window.crypto.getRandomValues(bytes);
        var pw = '';
        bytes.forEach(function (b) { pw += chars[b % chars.length]; });
        [btn.getAttribute('data-wcp-genpass'), btn.getAttribute('data-wcp-genpass-confirm')].forEach(function (sel) {
            var el = sel && document.querySelector(sel);
            if (el) { el.value = pw; el.type = 'text'; }
        });
        WhaCP.toast('A strong password was generated. Copy it before creating the account.', 'success');
    });

    // SQL import: upload the chosen file in chunks into the website's tmp/
    // folder (through the file manager's upload), then start the import;
    // or import a file already in the website.
    document.addEventListener('submit', function (e) {
        var form = e.target;
        if (!form.matches('[data-wcp-sql-import]') || form.dataset.wcpReady === '1') { return; }
        e.preventDefault();
        var err = form.querySelector('[data-wcp-import-error]');
        var fail = function (msg) { err.textContent = msg; err.classList.remove('d-none'); btn.disabled = false; };
        var btn = form.querySelector('button[type=submit]');
        var input = form.querySelector('[data-wcp-import-file]');
        var file = input && input.files && input.files[0];
        var existing = form.querySelector('[name=existing]').value.trim();
        err.classList.add('d-none');
        btn.disabled = true;
        var go = function (path, uploaded) {
            form.querySelector('[name=path]').value = path;
            form.querySelector('[name=uploaded]').value = uploaded ? '1' : '0';
            form.dataset.wcpReady = '1';
            form.submit();
        };
        if (!file) {
            if (!/\.sql(\.gz)?$/.test(existing)) { return fail('Choose a .sql or .sql.gz file.'); }
            return go(existing, false);
        }
        var ext = /\.sql\.gz$/i.test(file.name) ? '.sql.gz' : (/\.sql$/i.test(file.name) ? '.sql' : '');
        if (!ext) { return fail('Choose a .sql or .sql.gz file.'); }
        var rand = '';
        var chars = 'abcdefghijklmnopqrstuvwxyz0123456789';
        var bytes = new Uint8Array(16);
        window.crypto.getRandomValues(bytes);
        bytes.forEach(function (b) { rand += chars[b % chars.length]; });
        var path = 'tmp/whacp-import-' + rand + ext;
        var CHUNK = 1024 * 1024;
        var bar = form.querySelector('[data-wcp-import-progress]');
        bar.classList.remove('d-none');
        var fill = bar.querySelector('.progress-bar');
        var url = form.getAttribute('data-upload-url');
        (function chunk(offset) {
            var fd = new FormData();
            fd.append('chunk', file.slice(offset, offset + CHUNK), 'chunk');
            fd.append('path', path);
            fd.append('offset', String(offset));
            fd.append('final', offset + CHUNK >= file.size ? '1' : '0');
            fetch(url, { method: 'POST', body: fd, credentials: 'same-origin', headers: { 'X-CSRF-Token': WhaCP.csrfToken(), 'Accept': 'application/json' } })
                .then(function (r) { return r.json(); })
                .then(function (res) {
                    if (!res.success) { throw new Error((res.error && res.error.message) || 'Upload failed'); }
                    var done = Math.min(file.size, offset + CHUNK);
                    fill.style.width = (file.size ? Math.round(done / file.size * 100) : 100) + '%';
                    if (offset + CHUNK < file.size) { chunk(offset + CHUNK); } else { go(path, true); }
                })
                .catch(function (x) { fail(x.message); });
        })(0);
    });

    // Forms in the page submit in the background: the server answers a
    // form action with {redirect} (see AjaxForms), the page loads that
    // address and swaps in the new content, keeping the scroll position and
    // showing the result as a notification. Validation, CSRF and
    // authorization are the server's, exactly as for a normal submit.
    // Applies to forms in the page and its header actions. Left alone:
    // forms with a target, data-wcp-noajax (on the form or a parent), GET
    // forms, and pages that load their own scripts
    // (<body data-wcp-ajax="off">). Anything unexpected falls back to a
    // normal page load.
    function ajaxEligible(form) {
        if (!window.fetch || !window.DOMParser || !window.history.pushState) { return false; }
        if (document.body.getAttribute('data-wcp-ajax') === 'off') { return false; }
        if ((form.getAttribute('method') || 'get').toLowerCase() !== 'post' || form.hasAttribute('target')) { return false; }
        if (form.closest('[data-wcp-noajax]') || !form.closest('.wcp-content, .wcp-topbar-actions')) { return false; }
        var action = new URL(form.action, window.location.href);
        return action.origin === window.location.origin;
    }

    function sameAssets(doc) {
        var list = function (d) {
            return Array.prototype.map.call(d.querySelectorAll('script[src], link[rel="stylesheet"]'), function (el) {
                return el.getAttribute('src') || el.getAttribute('href');
            }).join('|');
        };
        return list(doc) === list(document);
    }

    // Replaces the page with a fetched document; false when it cannot be
    // swapped in place (different scripts), so the caller loads it fully.
    WhaCP.swapPage = function (html, url, keepScroll) {
        var doc = new DOMParser().parseFromString(html, 'text/html');
        var shell = doc.querySelector('.wcp-shell');
        if (!shell || !sameAssets(doc) || doc.body.getAttribute('data-wcp-ajax') === 'off') { return false; }
        var y = window.scrollY;
        // Windows of the old page go at once (a hide transition would finish
        // after the swap and undo the new page's window state). The shared
        // windows outside the page (confirmation, job log) close by themselves.
        var closed = false;
        document.querySelectorAll('.wcp-shell .modal').forEach(function (m) {
            var inst = window.bootstrap && bootstrap.Modal.getInstance(m);
            if (inst) { inst.dispose(); closed = true; }
        });
        if (closed && !document.querySelector('.modal.show')) {
            document.querySelectorAll('.modal-backdrop').forEach(function (b) { b.remove(); });
            document.body.classList.remove('modal-open');
            document.body.style.removeProperty('overflow');
            document.body.style.removeProperty('padding-right');
        }
        document.querySelector('.wcp-shell').replaceWith(document.adoptNode(shell));
        document.body.className = doc.body.className;
        document.title = doc.title;
        var meta = doc.querySelector('meta[name="csrf-token"]');
        if (meta) { document.querySelector('meta[name="csrf-token"]').setAttribute('content', meta.getAttribute('content')); }
        if (url) {
            var next = new URL(url, window.location.href);
            // A history step, as a normal form post makes one.
            window.history.pushState({ wcp: 1 }, '', next.href);
        }
        // Page behaviours run again for the new content.
        document.dispatchEvent(new Event('DOMContentLoaded'));
        if (keepScroll && !window.location.hash) { window.scrollTo(0, y); }
        // The result (flash message) as a notification when it is not in view.
        var alert = document.querySelector('.wcp-content > .alert');
        if (alert) {
            var r = alert.getBoundingClientRect();
            if (r.bottom < 0 || r.top > window.innerHeight) {
                var variant = alert.classList.contains('alert-success') ? 'success' : (alert.classList.contains('alert-danger') ? 'danger' : 'secondary');
                WhaCP.toast(alert.textContent.replace(/\s+/g, ' ').trim(), variant);
            }
        }
        return true;
    };

    function loadInto(url, keepScroll) {
        return fetch(url, { credentials: 'same-origin', headers: { 'Accept': 'text/html' } }).then(function (res) {
            var final = res.url || url;
            if (new URL(final).pathname === '/login') { window.location.href = final; return; }
            return res.text().then(function (html) {
                // fetch drops the #section of the address; keep the requested one.
                var hash = new URL(url, window.location.href).hash;
                if (!WhaCP.swapPage(html, final.split('#')[0] + hash, keepScroll)) { window.location.href = url; }
            });
        });
    }

    function download(res) {
        var cd = res.headers.get('Content-Disposition') || '';
        var m = cd.match(/filename\*?=(?:UTF-8'')?"?([^";]+)"?/i);
        return res.blob().then(function (blob) {
            var a = document.createElement('a');
            a.href = URL.createObjectURL(blob);
            a.download = m ? decodeURIComponent(m[1]) : 'download';
            document.body.appendChild(a);
            a.click();
            setTimeout(function () { URL.revokeObjectURL(a.href); a.remove(); }, 1000);
        });
    }

    function restore(form) {
        form.removeAttribute('aria-busy');
        delete form.dataset.wcpConfirmed;
        form.querySelectorAll('button[type="submit"], button:not([type])').forEach(function (b) {
            b.disabled = false;
            var spin = b.querySelector('.spinner-border');
            if (spin) { spin.classList.add('d-none'); }
        });
    }

    document.addEventListener('submit', function (e) {
        var form = e.target;
        if (e.defaultPrevented || !ajaxEligible(form)) { return; }
        // The pressed button may send elsewhere (formaction) or open a window.
        var pressed = e.submitter && e.submitter.form === form ? e.submitter : null;
        if (pressed && (pressed.hasAttribute('formtarget') || (pressed.getAttribute('formmethod') || 'post').toLowerCase() !== 'post')) { return; }
        var actionUrl = pressed && pressed.hasAttribute('formaction') ? pressed.formAction : form.action;
        if (new URL(actionUrl, window.location.href).origin !== window.location.origin) { return; }
        if (form.getAttribute('aria-busy') === 'true') { e.preventDefault(); return; }
        e.preventDefault();
        var data = new FormData(form);
        var sub = e.submitter;
        if (sub && sub.name && !data.has(sub.name)) { data.append(sub.name, sub.value || ''); }
        form.setAttribute('aria-busy', 'true');
        // Loading state: the pressed button (or the form's first) spins.
        var btn = (sub && sub.form === form ? sub : null) || form.querySelector('button[type="submit"], button:not([type])');
        if (btn) {
            btn.disabled = true;
            var spin = btn.querySelector('.spinner-border');
            if (spin) { spin.classList.remove('d-none'); }
        }
        fetch(actionUrl, {
            method: 'POST', body: data, credentials: 'same-origin',
            headers: { 'X-WhaCP-Ajax': '1', 'Accept': 'text/html' }, // as a normal form post
        }).then(function (res) {
            var type = res.headers.get('Content-Type') || '';
            if (type.indexOf('application/json') === 0) {
                return res.json().then(function (body) {
                    if (body && body.redirect) {
                        var to = new URL(body.redirect, window.location.href);
                        if (to.origin !== window.location.origin || to.pathname === '/login') { window.location.href = to.href; return; }
                        return loadInto(to.href, true);
                    }
                    restore(form);
                    WhaCP.toast((body && body.error && body.error.message) || (body && body.message) || (res.ok ? 'Done.' : 'The request failed.'), res.ok ? 'success' : 'danger');
                });
            }
            if (type.indexOf('text/html') === 0) {
                // A page rendered for the form (e.g. an error page).
                return res.text().then(function (html) {
                    if (!WhaCP.swapPage(html, null, false)) { document.open(); document.write(html); document.close(); }
                });
            }
            restore(form);
            if (res.ok) { return download(res); }
            WhaCP.toast('The request failed (HTTP ' + res.status + ').', 'danger');
        }).catch(function () {
            restore(form);
            WhaCP.toast('The panel could not be reached. Check the connection and try again.', 'danger');
        });
    });

    // Back and forward after background submits: load the address fully.
    window.addEventListener('popstate', function (e) {
        if (e.state && e.state.wcp) { window.location.reload(); }
    });

    // Global search (Ctrl+K): filter the tools list; Enter opens the first.
    function paletteFilter(input) {
        var box = input.closest('[data-wcp-palette]');
        var q = input.value.trim().toLowerCase().split(/\s+/).filter(Boolean);
        var shown = 0;
        box.querySelectorAll('[data-wcp-palette-item]').forEach(function (a) {
            var text = a.getAttribute('data-search') || '';
            var ok = q.every(function (w) { return text.indexOf(w) !== -1; });
            a.classList.toggle('d-none', !ok);
            a.classList.remove('is-first');
            if (ok) { shown++; }
        });
        box.querySelectorAll('[data-wcp-palette-group]').forEach(function (g) {
            var n = g.nextElementSibling, any = false;
            while (n && n.hasAttribute('data-wcp-palette-item')) { if (!n.classList.contains('d-none')) { any = true; } n = n.nextElementSibling; }
            g.classList.toggle('d-none', !any);
        });
        var first = box.querySelector('[data-wcp-palette-item]:not(.d-none)');
        if (first && q.length) { first.classList.add('is-first'); }
        box.querySelector('[data-wcp-palette-empty]').classList.toggle('d-none', shown > 0);
    }
    document.addEventListener('input', function (e) {
        if (e.target.matches('[data-wcp-palette-input]')) { paletteFilter(e.target); }
    });
    document.addEventListener('keydown', function (e) {
        if ((e.ctrlKey || e.metaKey) && (e.key === 'k' || e.key === 'K')) {
            var el = document.getElementById('wcp-palette');
            if (el && window.bootstrap) { e.preventDefault(); bootstrap.Modal.getOrCreateInstance(el).show(); }
        }
        if (e.key === 'Enter' && e.target.matches('[data-wcp-palette-input]')) {
            var first = e.target.closest('[data-wcp-palette]').querySelector('[data-wcp-palette-item]:not(.d-none)');
            if (first) { e.preventDefault(); window.location.href = first.href; }
        }
    });
    document.addEventListener('shown.bs.modal', function (e) {
        var input = e.target.querySelector && e.target.querySelector('[data-wcp-palette-input]');
        if (input) { input.value = ''; paletteFilter(input); input.focus(); }
    });
})();
