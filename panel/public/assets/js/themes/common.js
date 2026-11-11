// Behaviour shared by the customer-panel themes (loaded only by them). Everything is keyed off data-nt-* attributes.
(function () {
    'use strict';

    // A filter box over tiles: <input data-nt-filter="#id"> hides the [data-nt-tile] elements that do not match,
    // and the [data-nt-group] sections left without a visible tile.
    document.addEventListener('input', function (e) {
        var box = e.target.closest('[data-nt-filter]');
        if (!box) { return; }
        var root = document.querySelector(box.getAttribute('data-nt-filter'));
        if (!root) { return; }
        var q = box.value.trim().toLowerCase();
        var any = false;
        root.querySelectorAll('[data-nt-tile]').forEach(function (t) {
            var hit = q === '' || (t.getAttribute('data-name') || '').indexOf(q) !== -1;
            t.hidden = !hit;
            any = any || hit;
        });
        root.querySelectorAll('[data-nt-group]').forEach(function (g) {
            g.hidden = !g.querySelector('[data-nt-tile]:not([hidden])');
        });
        var empty = root.querySelector('[data-nt-empty]');
        if (empty) { empty.hidden = any; }
    });

    // The finder in the top bar: type to list matching tools, Enter opens the first, Escape closes.
    document.addEventListener('input', function (e) {
        var input = e.target.closest('[data-nt-finder-input]');
        if (!input) { return; }
        var wrap = input.closest('[data-nt-finder]');
        var list = wrap.querySelector('ul');
        var q = input.value.trim().toLowerCase();
        var shown = 0;
        list.querySelectorAll('[data-nt-finder-item]').forEach(function (li) {
            var hit = q !== '' && (li.getAttribute('data-name') || '').indexOf(q) !== -1 && shown < 8;
            li.hidden = !hit;
            if (hit) { shown++; }
        });
        list.hidden = shown === 0;
        input.setAttribute('aria-expanded', shown === 0 ? 'false' : 'true');
    });
    document.addEventListener('keydown', function (e) {
        var input = e.target.closest ? e.target.closest('[data-nt-finder-input]') : null;
        if (!input) { return; }
        var list = input.closest('[data-nt-finder]').querySelector('ul');
        if (e.key === 'Escape') { list.hidden = true; input.value = ''; input.setAttribute('aria-expanded', 'false'); }
        if (e.key === 'Enter') {
            var first = list.querySelector('[data-nt-finder-item]:not([hidden]) a');
            if (first) { e.preventDefault(); window.location.href = first.href; }
        }
    });
    document.addEventListener('click', function (e) {
        document.querySelectorAll('[data-nt-finder] ul').forEach(function (l) {
            if (!l.parentNode.contains(e.target)) { l.hidden = true; }
        });
    });

    // Slide-in menus on small screens: <button data-nt-nav="toggle"> shows or hides the navigation.
    document.addEventListener('click', function (e) {
        var b = e.target.closest('[data-nt-nav]');
        if (!b) { return; }
        var open = document.body.classList.toggle('nt-nav-open');
        document.querySelectorAll('[data-nt-nav="toggle"]').forEach(function (x) { x.setAttribute('aria-expanded', open ? 'true' : 'false'); });
    });
    document.addEventListener('keydown', function (e) {
        if (e.key === 'Escape') { document.body.classList.remove('nt-nav-open'); }
    });

    // Folding groups: <button data-nt-fold="#id"> shows or hides a group of links.
    document.addEventListener('click', function (e) {
        var b = e.target.closest('[data-nt-fold]');
        if (!b) { return; }
        var target = document.querySelector(b.getAttribute('data-nt-fold'));
        if (!target) { return; }
        target.hidden = !target.hidden;
        b.setAttribute('aria-expanded', target.hidden ? 'false' : 'true');
    });

    // A select that goes to the page it names: <select data-nt-jump>.
    document.addEventListener('change', function (e) {
        var s = e.target.closest('select[data-nt-jump]');
        if (s && s.value) { window.location.href = s.value; }
    });
})();
