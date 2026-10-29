// NimVor installation command builder. No inline scripts (the Content-Security-Policy only allows files).
//
// The command it prints is pasted into a root shell, so nothing the visitor types is ever copied into it
// unless it matches the same pattern the installer itself accepts. Anything else is reported and left out.
(function (root) {
    'use strict';

    var RE = {
        user: /^[a-z][a-z0-9._-]{2,31}$/,
        email: /^[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}$/,
        host: /^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/,
        version: /^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}(-[0-9A-Za-z.]{1,12})?$/,
        commit: /^[0-9a-f]{7,40}$/,
        php: /^[5-9]\.[0-9]{1,2}$/,
        remote: /^([0-9.]+|\[[0-9a-fA-F:]+\]):[0-9]{1,5}$/,
        allow: /^[0-9a-fA-F:./,]+$/
    };
    var DEFAULT_PHP = ['8.3', '8.4'];
    var BASE = 'curl -fsSL https://nimvor.com/install | sudo bash -s --';

    function port(value, min) {
        return /^[0-9]{1,5}$/.test(value) && Number(value) >= min && Number(value) <= 65535;
    }

    /**
     * @param {Object} s the form's state (all values are strings or booleans)
     * @returns {{command: string, errors: string[], notes: string[]}}
     */
    function buildCommand(s) {
        var errors = [];
        var notes = [];
        var opts = [];
        var mode = s.mode === 'look' || s.mode === 'agent' ? s.mode : 'hosting';
        var text = function (key) { return typeof s[key] === 'string' ? s[key].trim() : ''; };

        // The one-line script's own options come first.
        if (text('version') !== '') {
            if (RE.version.test(text('version'))) { opts.push('--version ' + text('version')); } else { errors.push('The release must look like 1.1.0.'); }
        }
        if (text('commit') !== '') {
            if (RE.commit.test(text('commit'))) { opts.push('--commit ' + text('commit')); } else { errors.push('The commit must be 7 to 40 characters from 0-9 and a-f.'); }
        }

        if (s.yes) { opts.push('--yes'); }

        if (mode === 'hosting') {
            var profile = ['full', 'web', 'minimal', 'none'].indexOf(s.profile) >= 0 ? s.profile : 'full';
            opts.push('--enable-writes');
            opts.push('--profile=' + profile);
            if (profile === 'full' && text('hostname') === '') {
                notes.push('Without a host name the installer uses the server’s own, and sets mail up only if that is a full name such as server.example.com.');
            }
            // The host name is only the mail server's name: it matters (and is shown) with the full profile.
            if (profile === 'full' && text('hostname') !== '') {
                if (RE.host.test(text('hostname'))) { opts.push('--hostname=' + text('hostname')); } else { errors.push('The host name must be a full name such as server.example.com.'); }
            }
            if (profile !== 'none') {
                var php = (Array.isArray(s.php) ? s.php : []).filter(function (v) { return RE.php.test(v); });
                if (php.length === 0) {
                    errors.push('Choose at least one PHP version.');
                } else {
                    php.sort(function (a, b) { return parseFloat(a) - parseFloat(b); });
                    opts.push('--php=' + php.join(','));
                    php.forEach(function (v) {
                        if (parseFloat(v) < 8.2) { notes.push('PHP ' + v + ' no longer gets upstream security fixes.'); }
                    });
                }
            }
        } else if (mode === 'look') {
            notes.push('Read-only: NimVor inventories the server and changes nothing. You can switch write mode on later.');
        } else {
            opts.push('--agent-only');
            if (text('remote') === '' || !RE.remote.test(text('remote'))) {
                errors.push('Enter the address the agent listens on, such as 0.0.0.0:8443.');
            } else {
                opts.push('--remote-access=' + text('remote'));
            }
            if (text('allow') === '' || !RE.allow.test(text('allow'))) {
                errors.push('Enter the panel server’s IP address (several separated by commas).');
            } else {
                opts.push('--allow-from=' + text('allow'));
            }
        }

        if (mode !== 'agent') {
            if (text('adminUser') !== '') {
                if (RE.user.test(text('adminUser'))) { opts.push('--admin-user=' + text('adminUser')); } else { errors.push('The administrator name needs 3 to 32 characters: lowercase letters, digits, dot, dash or underscore, starting with a letter.'); }
            }
            if (text('adminEmail') !== '') {
                if (!RE.email.test(text('adminEmail'))) {
                    errors.push('Enter a valid email address.');
                } else {
                    opts.push('--admin-email=' + text('adminEmail'));
                    if (/@(example\.(com|org|net)|.*\.test|.*\.invalid)$/i.test(text('adminEmail'))) {
                        notes.push('Let’s Encrypt rejects addresses on example domains: use a real one for expiry notices.');
                    }
                }
            }
            if (text('port') !== '' && text('port') !== '8090') {
                if (port(text('port'), 1024)) { opts.push('--port=' + text('port')); } else { errors.push('The panel port must be between 1024 and 65535.'); }
            }
            if (s.publicHttps) {
                var hp = text('httpsPort') === '' ? '8443' : text('httpsPort');
                if (!port(hp, 1024)) {
                    errors.push('The HTTPS port must be between 1024 and 65535.');
                } else if (hp === (text('port') === '' ? '8090' : text('port'))) {
                    errors.push('The HTTPS port must differ from the panel’s own port.');
                } else {
                    opts.push(hp === '8443' ? '--public-https' : '--public-https=' + hp);
                }
            }
        }

        if (s.resume) { opts.push('--resume'); }
        if (s.noThirdParty && mode === 'hosting') { opts.push('--no-third-party-repos'); }
        if (s.noDeps) { opts.push('--no-install-deps'); }
        if (s.forceUnsupported) { opts.push('--force-unsupported'); }
        if (s.dryRun) { opts.push('--dry-run'); }

        var command = BASE + (opts.length ? ' \\\n  ' + opts.join(' \\\n  ') : '');
        return { command: command, errors: errors, notes: notes };
    }

    var api = { buildCommand: buildCommand, DEFAULT_PHP: DEFAULT_PHP };
    if (typeof module !== 'undefined' && module.exports) { module.exports = api; }
    root.NimVorInstallBuilder = api;

    // ----- the page -----
    if (typeof document === 'undefined') { return; }
    var form = document.getElementById('install-builder');
    if (!form) { return; }
    form.hidden = false;
    var staticBox = document.getElementById('install-static');
    if (staticBox) { staticBox.hidden = true; }

    var out = document.getElementById('ib-command');
    var copy = document.getElementById('ib-copy');
    var errorsBox = document.getElementById('ib-errors');
    var notesBox = document.getElementById('ib-notes');

    function val(name) { var el = form.elements[name]; return el ? el.value : ''; }
    function checked(name) { var el = form.elements[name]; return !!(el && el.checked); }

    function state() {
        var php = [];
        form.querySelectorAll('input[name="php"]:checked').forEach(function (el) { php.push(el.value); });
        var mode = 'hosting';
        form.querySelectorAll('input[name="mode"]').forEach(function (el) { if (el.checked) { mode = el.value; } });
        return {
            mode: mode, profile: val('profile'), hostname: val('hostname'), adminUser: val('adminUser'), adminEmail: val('adminEmail'),
            php: php, port: val('port'), publicHttps: checked('publicHttps'), httpsPort: val('httpsPort'),
            yes: checked('yes'), resume: checked('resume'), noThirdParty: checked('noThirdParty'), noDeps: checked('noDeps'),
            forceUnsupported: checked('forceUnsupported'), dryRun: checked('dryRun'),
            version: val('version'), commit: val('commit'), remote: val('remote'), allow: val('allow')
        };
    }

    function list(box, items) {
        box.textContent = '';
        items.forEach(function (t) {
            var li = document.createElement('li');
            li.textContent = t;
            box.appendChild(li);
        });
        box.hidden = items.length === 0;
    }

    function render() {
        var s = state();
        form.querySelectorAll('[data-mode]').forEach(function (el) {
            el.hidden = el.getAttribute('data-mode').split(' ').indexOf(s.mode) < 0;
        });
        form.querySelectorAll('[data-profile]').forEach(function (el) {
            el.hidden = s.mode !== 'hosting' || el.getAttribute('data-profile').split(' ').indexOf(s.profile) < 0;
        });
        var httpsPort = form.elements.httpsPort;
        if (httpsPort) { httpsPort.disabled = !s.publicHttps; }
        var r = buildCommand(s);
        out.textContent = r.command;
        list(errorsBox, r.errors);
        list(notesBox, r.notes);
        copy.disabled = r.errors.length > 0;
        copy.setAttribute('data-copy', r.command);
    }

    form.addEventListener('input', render);
    form.addEventListener('change', render);
    form.addEventListener('submit', function (e) { e.preventDefault(); });
    render();
})(typeof window !== 'undefined' ? window : globalThis);
