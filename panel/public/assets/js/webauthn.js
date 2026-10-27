// NimVor security keys (WebAuthn): add a key on the profile page and sign
// in with one. Binary fields travel base64url-encoded.
(function () {
    'use strict';
    function b64u(buf) {
        var s = '', bytes = new Uint8Array(buf);
        for (var i = 0; i < bytes.length; i++) { s += String.fromCharCode(bytes[i]); }
        return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
    }
    function unb64u(s) {
        s = s.replace(/-/g, '+').replace(/_/g, '/');
        while (s.length % 4) { s += '='; }
        var bin = atob(s), out = new Uint8Array(bin.length);
        for (var i = 0; i < bin.length; i++) { out[i] = bin.charCodeAt(i); }
        return out.buffer;
    }
    function post(url, body) {
        var meta = document.querySelector('meta[name="csrf-token"]');
        return fetch(url, {
            method: 'POST', credentials: 'same-origin',
            headers: { 'Content-Type': 'application/json', 'Accept': 'application/json', 'X-Requested-With': 'XMLHttpRequest', 'X-CSRF-Token': meta ? meta.content : '' },
            body: JSON.stringify(body || {})
        }).then(function (res) {
            return res.json().then(function (j) {
                if (!res.ok || j.success === false) {
                    var e = new Error((j.error && j.error.message) || 'Request failed');
                    e.restart = !!j.restart;
                    throw e;
                }
                return j.data;
            });
        });
    }
    function friendly(e) {
        if (e && (e.name === 'NotAllowedError' || e.name === 'AbortError')) { return 'The key was not used (cancelled or timed out). Try again.'; }
        if (e && e.name === 'InvalidStateError') { return 'This key is registered already.'; }
        if (e && e.name === 'SecurityError') { return 'Security keys need the panel to be opened over HTTPS at its own address.'; }
        return (e && e.message) || 'Something went wrong.';
    }
    function showError(box, msg) { if (box) { box.textContent = msg; box.classList.toggle('d-none', !msg); } }
    function supported(box) {
        if (window.PublicKeyCredential && navigator.credentials) { return true; }
        showError(box, 'This browser does not support security keys, or the page is not opened over HTTPS.');
        return false;
    }

    // ---- profile: add a key -----------------------------------------------
    var keys = document.getElementById('wcp-keys');
    if (keys) {
        var form = keys.querySelector('[data-wcp-key-add]'), err = keys.querySelector('[data-wcp-key-error]');
        form.addEventListener('submit', function (ev) {
            ev.preventDefault();
            showError(err, '');
            if (!supported(err)) { return; }
            var btn = form.querySelector('button'), name = form.elements.name.value.trim();
            btn.disabled = true;
            post(keys.dataset.options).then(function (o) {
                o.challenge = unb64u(o.challenge);
                o.user.id = unb64u(o.user.id);
                o.excludeCredentials = (o.excludeCredentials || []).map(function (c) { return { type: c.type, id: unb64u(c.id) }; });
                return navigator.credentials.create({ publicKey: o });
            }).then(function (cred) {
                return post(keys.dataset.add, { name: name, client_data: b64u(cred.response.clientDataJSON), attestation: b64u(cred.response.attestationObject) });
            }).then(function (d) {
                window.location.href = d.redirect;
                window.location.reload();
            }).catch(function (e) { showError(err, friendly(e)); btn.disabled = false; });
        });
    }

    // ---- sign-in with a key ---------------------------------------------------
    var login = document.getElementById('wcp-key-login');
    if (login) {
        var lbtn = login.querySelector('[data-wcp-key-login]'), lerr = login.querySelector('[data-wcp-key-error]');
        var run = function () {
            showError(lerr, '');
            if (!supported(lerr)) { return; }
            lbtn.disabled = true;
            post(login.dataset.options).then(function (o) {
                o.challenge = unb64u(o.challenge);
                o.allowCredentials = (o.allowCredentials || []).map(function (c) { return { type: c.type, id: unb64u(c.id) }; });
                return navigator.credentials.get({ publicKey: o });
            }).then(function (a) {
                return post(login.dataset.verify, {
                    id: b64u(a.rawId), client_data: b64u(a.response.clientDataJSON),
                    authenticator_data: b64u(a.response.authenticatorData), signature: b64u(a.response.signature)
                });
            }).then(function (d) {
                window.location.href = d.redirect;
            }).catch(function (e) {
                if (e.restart) { window.location.href = '/login'; return; }
                showError(lerr, friendly(e));
                lbtn.disabled = false;
            });
        };
        lbtn.addEventListener('click', run);
    }
})();
