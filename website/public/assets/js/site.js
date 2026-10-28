// NimVor website. No inline scripts: the Content-Security-Policy only allows this file.
(function () {
    'use strict';

    var header = document.querySelector('.site-header');
    var toggle = document.querySelector('[data-nav-toggle]');
    if (header && toggle) {
        toggle.addEventListener('click', function () {
            var open = header.classList.toggle('open');
            toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
        });
    }

    // Dropdowns open on hover and keyboard focus through CSS; a tap or Enter also toggles them.
    document.querySelectorAll('.has-menu > button').forEach(function (btn) {
        btn.addEventListener('click', function (e) {
            e.stopPropagation();
            var item = btn.parentElement;
            var open = item.classList.toggle('open');
            btn.setAttribute('aria-expanded', open ? 'true' : 'false');
        });
    });
    document.addEventListener('click', function () {
        document.querySelectorAll('.has-menu.open').forEach(function (m) {
            m.classList.remove('open');
            var b = m.querySelector('button');
            if (b) { b.setAttribute('aria-expanded', 'false'); }
        });
    });
    document.addEventListener('keydown', function (e) {
        if (e.key === 'Escape') {
            document.querySelectorAll('.has-menu.open').forEach(function (m) { m.classList.remove('open'); });
        }
    });

    // Copy buttons: data-copy holds the text, or data-copy-target names the element to copy.
    document.querySelectorAll('[data-copy]').forEach(function (btn) {
        btn.addEventListener('click', function () {
            var text = btn.getAttribute('data-copy');
            var done = function () {
                var label = btn.querySelector('.label');
                var old = label ? label.textContent : '';
                btn.classList.add('done');
                if (label) { label.textContent = 'Copied'; }
                setTimeout(function () {
                    btn.classList.remove('done');
                    if (label) { label.textContent = old; }
                }, 1800);
            };
            if (navigator.clipboard && window.isSecureContext) {
                navigator.clipboard.writeText(text).then(done, function () {});
            } else {
                var ta = document.createElement('textarea');
                ta.value = text;
                ta.setAttribute('readonly', '');
                ta.style.position = 'fixed';
                ta.style.opacity = '0';
                document.body.appendChild(ta);
                ta.select();
                try { document.execCommand('copy'); done(); } catch (err) { /* nothing to do */ }
                document.body.removeChild(ta);
            }
        });
    });

    // A destructive button asks first (data-confirm holds the question).
    document.querySelectorAll('form[data-confirm]').forEach(function (f) {
        f.addEventListener('submit', function (e) {
            if (!window.confirm(f.getAttribute('data-confirm'))) { e.preventDefault(); }
        });
    });

    // Submitting a form once: the button is disabled so a double click cannot send it twice.
    document.querySelectorAll('form[data-once]').forEach(function (f) {
        f.addEventListener('submit', function () {
            f.querySelectorAll('button[type="submit"]').forEach(function (b) { b.disabled = true; });
        });
    });
})();
