// NimVor PostgreSQL Objects list: selecting several tables / views and applying one action to all of them.
(function () {
    'use strict';
    var form = document.querySelector('[data-pgo-form]');
    if (!form) { return; }
    var all = form.querySelector('[data-pgo-all]');
    var rows = Array.prototype.slice.call(form.querySelectorAll('[data-pgo-row]'));
    var count = form.querySelector('[data-pgo-count]');
    var op = form.querySelector('[data-pgo-op]');
    var danger = form.querySelector('[data-pgo-danger]');
    var restart = form.querySelector('[data-pgo-restart]');
    var go = form.querySelector('[data-pgo-go]');
    var confirmBox = form.querySelector('input[name="confirm"]');
    function chosen() { return rows.filter(function (r) { return r.checked; }).length; }
    function update() {
        var n = chosen();
        count.textContent = n === 0 ? 'None selected' : n + ' selected';
        var destructive = op.value === 'drop' || op.value === 'truncate';
        danger.classList.toggle('d-none', !destructive);
        restart.classList.toggle('d-none', op.value !== 'truncate');
        go.classList.toggle('btn-danger', destructive);
        go.classList.toggle('btn-primary', !destructive);
        go.disabled = n === 0 || op.value === '';
        if (all) { all.checked = n > 0 && n === rows.length; all.indeterminate = n > 0 && n < rows.length; }
    }
    if (all) { all.addEventListener('change', function () { rows.forEach(function (r) { r.checked = all.checked; }); update(); }); }
    rows.forEach(function (r) { r.addEventListener('change', update); });
    op.addEventListener('change', update);
    form.addEventListener('submit', function (e) {
        if ((op.value === 'drop' || op.value === 'truncate') && confirmBox && confirmBox.value.trim() === '') {
            e.preventDefault();
            confirmBox.focus();
        }
    });
    update();
})();
