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

    // Show only the fields that belong to the selected website type.
    function applyKind(select) {
        var kind = select.value;
        document.querySelectorAll('[data-wcp-kind]').forEach(function (el) {
            el.classList.toggle('d-none', el.getAttribute('data-wcp-kind') !== kind);
        });
    }
    document.addEventListener('change', function (e) {
        if (e.target.matches('[data-wcp-toggle-kind]')) { applyKind(e.target); }
    });
    document.addEventListener('DOMContentLoaded', function () {
        var k = document.querySelector('[data-wcp-toggle-kind]');
        if (k) { applyKind(k); }
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
        var classes = { queued: 'text-bg-secondary', running: 'text-bg-info', succeeded: 'text-bg-success', failed: 'text-bg-danger' };
        function setStatus(s) {
            statusEl.textContent = s;
            statusEl.className = 'badge ms-2 ' + (classes[s] || 'text-bg-secondary');
        }
        function poll() {
            if (stopped) { return; }
            WhaCP.fetchJson('/jobs/' + jobId + '?offset=' + offset).then(function (res) {
                if (res.log) {
                    logEl.textContent += res.log;
                    logEl.scrollTop = logEl.scrollHeight;
                }
                offset = res.offset || offset;
                setStatus(res.job ? res.job.status : 'unknown');
                if (res.done) {
                    reloadBtn.classList.remove('d-none');
                    WhaCP.toast(res.job.status === 'succeeded' ? 'Task completed.' : 'Task failed. See the log for details.', res.job.status === 'succeeded' ? 'success' : 'danger');
                    return;
                }
                setTimeout(poll, 1000);
            }).catch(function (err) {
                logEl.textContent += '\n[panel] ' + err.message + '\n';
                setTimeout(poll, 3000);
            });
        }
        modalEl.addEventListener('hidden.bs.modal', function () { stopped = true; }, { once: true });
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
})();
