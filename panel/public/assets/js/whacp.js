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
        var btn = form.querySelector('button[type="submit"]');
        if (btn) {
            btn.disabled = true;
            var spin = btn.querySelector('.spinner-border');
            if (spin) { spin.classList.remove('d-none'); }
        }
    });

    // Confirmation modal for destructive forms: data-wcp-confirm="message",
    // optional data-wcp-confirm-word="text the user must type".
    var pendingForm = null;
    document.addEventListener('submit', function (e) {
        var form = e.target;
        if (!form.matches('[data-wcp-confirm]') || form.dataset.wcpConfirmed === '1') { return; }
        e.preventDefault();
        pendingForm = form;
        var modalEl = document.getElementById('wcp-confirm');
        modalEl.querySelector('[data-wcp-confirm-message]').textContent = form.getAttribute('data-wcp-confirm');
        var word = form.getAttribute('data-wcp-confirm-word');
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
        pendingForm = null;
        form.dataset.wcpConfirmed = '1';
        bootstrap.Modal.getInstance(document.getElementById('wcp-confirm')).hide();
        if (form.requestSubmit) { form.requestSubmit(); } else { form.submit(); }
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
        if (e.target.matches('[data-wcp-autosubmit]')) { e.target.form.submit(); }
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
        setInterval(refresh, 60000);
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
        function render() {
            itemsEl.textContent = '';
            var shown = null;
            queue.forEach(function (it) {
                var li = el('li', 'list-group-item d-flex align-items-center gap-2');
                li.setAttribute('data-wcp-pkg-item', it.name);
                li.appendChild(el('span', 'fw-semibold', it.name));
                li.appendChild(el('span', 'badge ms-auto ' + badges[it.status], labels[it.status]));
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
                WhaCP.toast(it.name + ' was not installed. See the log in the queue.', 'danger');
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
        setTimeout(function () {
            el.scrollIntoView({ block: 'start', behavior: 'auto' });
            var f = el.querySelector('input:not([type=hidden]):not([disabled]), select, textarea');
            if (f && el.classList.contains('collapse')) { f.focus({ preventScroll: true }); }
        }, 50);
    }
    document.addEventListener('DOMContentLoaded', openFromHash);
    window.addEventListener('hashchange', openFromHash);
    // Focus the first field when a form panel is opened by its button.
    document.addEventListener('shown.bs.collapse', function (e) {
        if (!e.target.matches('[data-wcp-form-panel]')) { return; }
        var f = e.target.querySelector('input:not([type=hidden]):not([disabled]), select, textarea');
        if (f) { f.focus(); }
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
})();
