// Backups > Remote fetched files.
// 1. The "Fetch a remote file" window works in two steps: "Check the server" asks the agent for the server's key
//    (nothing secret is sent), shows it for the person to confirm and pins it in the form; "Fetch file" then posts
//    the form with that key. Changing the server (protocol, host, port, encryption) throws the confirmation away.
// 2. "Delete selected" is enabled while files are ticked, and says how many.
// 3. While a file is being fetched the page reloads every few seconds (unless a window is open).
(function () {
    'use strict';
    var form = document.querySelector('[data-fx-form]');
    if (form) {
        var hostKey = form.querySelector('[data-fx-hostkey]');
        var pin = form.querySelector('[data-fx-pin]');
        var keyBox = form.querySelector('[data-fx-key]');
        var keyText = form.querySelector('[data-fx-key-text]');
        var errBox = form.querySelector('[data-fx-error]');
        var go = form.querySelector('[data-fx-go]');
        var goText = form.querySelector('[data-fx-go-text]');
        var spinner = go.querySelector('.spinner-border');
        var checking = false;

        var confirmed = function () { return hostKey.value !== '' || pin.value !== ''; };
        var setStep = function () { goText.textContent = confirmed() ? 'Fetch file' : 'Check the server'; };
        var clearErrors = function () {
            errBox.classList.add('d-none');
            errBox.textContent = '';
            form.querySelectorAll('.is-invalid').forEach(function (el) { el.classList.remove('is-invalid'); });
            form.querySelectorAll('.invalid-feedback').forEach(function (el) { el.remove(); });
        };
        var forget = function () {
            hostKey.value = '';
            pin.value = '';
            keyBox.classList.add('d-none');
            setStep();
        };
        var showError = function (msg, fields) {
            clearErrors();
            errBox.textContent = msg;
            errBox.classList.remove('d-none');
            Object.keys(fields || {}).forEach(function (name) {
                var el = form.querySelector('[name="' + name + '"]');
                if (!el) { return; }
                el.classList.add('is-invalid');
                var fb = document.createElement('div');
                fb.className = 'invalid-feedback d-block';
                fb.textContent = fields[name];
                el.insertAdjacentElement('afterend', fb);
            });
        };
        var setField = function (name, value) {
            var el = form.querySelector('[name="' + name + '"]');
            if (el && value !== undefined && value !== null && value !== '') { el.value = String(value); }
        };

        form.addEventListener('change', function (e) {
            if (e.target.matches('[data-fx-reset], [name="protocol"]')) { forget(); }
        });
        form.addEventListener('input', function (e) {
            if (e.target.matches('[data-fx-reset]')) { forget(); }
            // An address in the file box decides the server too.
            if (e.target.matches('[data-fx-path]') && /^(sftp|ftps?):\/\//i.test(e.target.value)) { forget(); }
        });

        form.addEventListener('submit', function (e) {
            if (confirmed()) { return; } // second step: post the form
            e.preventDefault();
            if (checking) { return; }
            checking = true;
            clearErrors();
            go.disabled = true;
            spinner.classList.remove('d-none');
            fetch(form.getAttribute('data-probe-url'), { method: 'POST', body: new FormData(form), credentials: 'same-origin', headers: { 'Accept': 'application/json', 'X-Requested-With': 'XMLHttpRequest' } })
                .then(function (res) { return res.json().then(function (b) { return { ok: res.ok && b.success !== false, body: b }; }); })
                .then(function (r) {
                    if (!r.ok) {
                        var er = r.body.error || {};
                        showError(er.message || 'The server could not be checked.', er.fields);
                        return;
                    }
                    var d = r.body.data || {};
                    // The address may have filled in the server: show what will be used.
                    setField('protocol', d.protocol);
                    form.querySelector('[name="protocol"]').dispatchEvent(new Event('change', { bubbles: true }));
                    setField('host', d.host);
                    setField('port', d.port);
                    setField('username', d.username);
                    setField('tls', d.tls);
                    setField('remote_path', d.remote_path);
                    hostKey.value = d.host_key || '';
                    pin.value = d.tls_pin || '';
                    if (!confirmed()) { showError('The server did not give a key to confirm.'); return; }
                    keyText.replaceChildren();
                    var line = function (label, value) {
                        var row = document.createElement('div');
                        var b = document.createElement('strong');
                        b.textContent = label + ' ';
                        var c = document.createElement('code');
                        c.className = 'text-break';
                        c.textContent = value;
                        row.append(b, c);
                        keyText.appendChild(row);
                    };
                    line('Server', d.host + ':' + d.port + ' (' + (d.protocol === 'sftp' ? 'SFTP' : 'FTPS') + ')');
                    if (d.protocol === 'sftp') {
                        line('SSH host key', (d.key_type || '') + ' ' + (d.fingerprint || ''));
                    } else {
                        line('Certificate', d.subject || '');
                        line('Key pin', d.tls_pin || '');
                    }
                    keyBox.classList.remove('d-none');
                    setStep();
                })
                .catch(function () { showError('The server could not be checked. Try again.'); })
                .then(function () {
                    checking = false;
                    go.disabled = false;
                    spinner.classList.add('d-none');
                });
        });

        // After a refused fetch the window opens again; the key must be checked again.
        forget();
        var modal = document.getElementById('fetch-remote');
        if (modal) {
            modal.addEventListener('hidden.bs.modal', function () { forget(); clearErrors(); });
        }
        form.addEventListener('submit', function () {
            if (confirmed() && !checking) { spinner.classList.remove('d-none'); }
        });
    }

    // Delete selected
    var bulk = document.querySelector('[data-fx-bulk]');
    if (bulk) {
        var count = bulk.querySelector('[data-fx-count]');
        var sync = function () {
            var n = document.querySelectorAll('[data-fx-check]:checked').length;
            bulk.disabled = n === 0;
            count.textContent = String(n);
            count.classList.toggle('d-none', n === 0);
            bulk.setAttribute('data-nim-confirm-submit', 'Delete the ' + (n === 1 ? 'selected file' : n + ' selected files') + '? ' + (n === 1 ? 'It is' : 'They are') + ' removed from this server and cannot be recovered.');
        };
        document.addEventListener('change', function (e) {
            // The select-all box ticks the rows in its own listener; look after it has.
            if (e.target.matches('[data-fx-check], [data-nim-select-all]')) { setTimeout(sync, 0); }
        });
        sync();
    }

    // A fetch in progress: look again every few seconds.
    var dt = document.querySelector('[data-fetched-running]');
    if (dt && Number(dt.getAttribute('data-fetched-running')) > 0) {
        setInterval(function () {
            if (!document.querySelector('.modal.show')) { location.reload(); }
        }, 5000);
    }
})();
