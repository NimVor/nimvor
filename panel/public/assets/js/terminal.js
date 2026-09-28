// Web terminal: xterm.js over the agent's WebSocket (/terminal/ws).
//
// The page's Content-Security-Policy allows style elements only with this
// response's nonce, and no style attributes. xterm.js creates style
// elements (colours, cell sizes) and sets some colours with
// setAttribute('style'); on this page style elements get the nonce and
// style attributes are applied through the CSSOM instead.
(function () {
    'use strict';

    var root = document.getElementById('wcp-term');
    if (!root) { return; }

    var nonce = root.getAttribute('data-style-nonce') || '';
    var createElement = Document.prototype.createElement;
    Document.prototype.createElement = function (tag, options) {
        var el = createElement.call(this, tag, options);
        if (String(tag).toLowerCase() === 'style') { el.nonce = nonce; }
        return el;
    };
    var setAttribute = Element.prototype.setAttribute;
    Element.prototype.setAttribute = function (name, value) {
        if (String(name).toLowerCase() === 'style' && this.style) {
            this.style.cssText = String(value);
            return;
        }
        return setAttribute.call(this, name, value);
    };

    var WhaCP = window.WhaCP;
    var openUrl = root.getAttribute('data-open-url');
    var unlockForm = document.getElementById('wcp-term-unlock');
    var screen = document.getElementById('wcp-term-screen');
    var state = document.getElementById('wcp-term-state');
    var connectBtn = document.getElementById('wcp-term-connect');
    var closeBtn = document.getElementById('wcp-term-close');
    var term = null;
    var fit = null;
    var ws = null;
    var encoder = new TextEncoder();

    function setState(text, kind) {
        state.textContent = text;
        state.className = 'wcp-state ' + (kind || 'is-off');
    }

    function showUnlock(message) {
        unlockForm.hidden = false;
        connectBtn.hidden = true;
        var err = unlockForm.querySelector('[data-term-error]');
        err.textContent = message || '';
        err.hidden = !message;
        var pw = unlockForm.querySelector('input[name="password"]');
        if (pw) { pw.focus(); }
    }

    function ensureTerminal() {
        if (term) { return; }
        // The UMD builds: window.Terminal is the class, window.FitAddon the module.
        var Term = window.Terminal && window.Terminal.Terminal ? window.Terminal.Terminal : window.Terminal;
        var Fit = window.FitAddon && window.FitAddon.FitAddon ? window.FitAddon.FitAddon : window.FitAddon;
        term = new Term({
            cursorBlink: true,
            fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
            fontSize: 14,
            scrollback: 5000,
            theme: { background: '#0f1419', foreground: '#e6e6e6', cursor: '#e6e6e6' }
        });
        fit = new Fit();
        term.loadAddon(fit);
        term.open(screen);
        fit.fit();
        term.onData(function (d) { send(encoder.encode(d)); });
        term.onBinary(function (d) {
            var b = new Uint8Array(d.length);
            for (var i = 0; i < d.length; i++) { b[i] = d.charCodeAt(i) & 255; }
            send(b);
        });
        term.onResize(function (size) {
            if (ws && ws.readyState === 1) {
                ws.send(JSON.stringify({ type: 'resize', cols: size.cols, rows: size.rows }));
            }
        });
        var timer = null;
        window.addEventListener('resize', function () {
            clearTimeout(timer);
            timer = setTimeout(function () { fit.fit(); }, 100);
        });
    }

    function send(bytes) {
        if (ws && ws.readyState === 1) { ws.send(bytes); }
    }

    function ended(reason) {
        setState('Disconnected', 'is-off');
        connectBtn.hidden = false;
        connectBtn.disabled = false;
        closeBtn.hidden = true;
        if (term && reason) { term.write('\r\n\x1b[33m' + reason + '\x1b[0m\r\n'); }
        ws = null;
    }

    function connect() {
        ensureTerminal();
        connectBtn.disabled = true;
        setState('Connecting…', 'is-warn');
        WhaCP.fetchJson(openUrl, { method: 'POST', body: JSON.stringify({ cols: term.cols, rows: term.rows }) })
            .then(function (res) {
                var proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
                var sock = new WebSocket(proto + '//' + window.location.host + '/terminal/ws');
                var reason = '';
                sock.binaryType = 'arraybuffer';
                sock.onopen = function () {
                    sock.send(JSON.stringify({ type: 'auth', ticket: res.data.ticket }));
                };
                sock.onmessage = function (ev) {
                    if (typeof ev.data !== 'string') {
                        term.write(new Uint8Array(ev.data));
                        return;
                    }
                    var msg;
                    try { msg = JSON.parse(ev.data); } catch (e) { return; }
                    if (msg.type === 'ready') {
                        setState('Connected as ' + msg.user + (msg.mode === 'jailed' ? ' (jailed shell)' : msg.mode === 'root' ? ' (root)' : ''), 'is-ok');
                        closeBtn.hidden = false;
                        connectBtn.hidden = true;
                        term.focus();
                    } else if (msg.type === 'exit') {
                        reason = msg.reason || '';
                    }
                };
                sock.onclose = function () {
                    ended(sock.wcpClosing ? 'Session closed.' : (reason || 'The connection closed.'));
                };
                sock.onerror = function () {
                    if (!reason) { reason = 'The terminal connection failed. The web terminal needs the panel served over HTTPS by Nginx (install.sh --public-https).'; }
                };
                ws = sock;
            })
            .catch(function (err) {
                connectBtn.disabled = false;
                setState('Not connected', 'is-off');
                if (err.details && err.details.unlock) {
                    showUnlock('');
                    return;
                }
                term.write('\r\n\x1b[31m' + err.message + '\x1b[0m\r\n');
            });
    }

    connectBtn.addEventListener('click', connect);
    closeBtn.addEventListener('click', function () {
        if (ws) {
            ws.wcpClosing = true;
            ws.close();
        }
    });
    unlockForm.addEventListener('submit', function (ev) {
        ev.preventDefault();
        var btn = unlockForm.querySelector('button[type="submit"]');
        btn.disabled = true;
        var body = { password: unlockForm.password.value, code: unlockForm.code ? unlockForm.code.value : '' };
        WhaCP.fetchJson(unlockForm.getAttribute('action'), { method: 'POST', body: JSON.stringify(body) })
            .then(function () {
                unlockForm.reset();
                unlockForm.hidden = true;
                connectBtn.hidden = false;
                connect();
            })
            .catch(function (err) { showUnlock(err.message); })
            .then(function () { btn.disabled = false; });
    });

    if (root.getAttribute('data-unlocked') === '1') {
        connect();
    } else {
        showUnlock('');
    }
})();
