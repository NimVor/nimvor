// NimVor PostgreSQL database pages: the filter above the table list (the list itself is plain links).
(function () {
    'use strict';
    var box = document.querySelector('[data-pgf-filter]');
    var holder = document.querySelector('[data-pgf-tables]');
    var count = document.querySelector('[data-pgf-count]');
    if (!box || !holder) { return; }
    var items = Array.prototype.slice.call(holder.querySelectorAll('[data-pgf-name]'));
    var heads = Array.prototype.slice.call(holder.querySelectorAll('[data-pgf-schema]'));
    var total = items.length;
    var empty = count ? count.textContent : '';
    box.addEventListener('input', function () {
        var q = box.value.trim().toLowerCase();
        var shown = 0;
        var visibleSchemas = {};
        items.forEach(function (a) {
            var on = q === '' || a.getAttribute('data-pgf-name').indexOf(q) !== -1;
            a.classList.toggle('d-none', !on);
            if (on) { shown++; visibleSchemas[a.getAttribute('data-pgf-name').split('.')[0]] = true; }
        });
        heads.forEach(function (h) { h.classList.toggle('d-none', q !== '' && !visibleSchemas[h.getAttribute('data-pgf-schema').toLowerCase()]); });
        if (count && total > 0) { count.textContent = q === '' ? total + ' table' + (total === 1 ? '' : 's') : shown + ' of ' + total + ' tables'; }
        else if (count) { count.textContent = empty; }
    });
})();
