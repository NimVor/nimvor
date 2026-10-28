// Log viewer with optional live tail (polls every 2 s using offsets or
// journal cursors, so only new lines are transferred).
(function () {
    'use strict';
    var root = document.getElementById('nim-logs');
    if (!root) { return; }
    var out = root.querySelector('[data-log-output]');
    var status = root.querySelector('[data-log-status]');
    var srcSel = root.querySelector('[data-log-source]');
    var linesSel = root.querySelector('[data-log-lines]');
    var filterIn = root.querySelector('[data-log-filter]');
    var live = root.querySelector('[data-log-live]');
    var dl = root.querySelector('[data-log-download]');
    var state = { offset: 0, cursor: '', timer: null };
    var MAX_LINES = 10000;

    function url(follow) {
        var p = new URLSearchParams({ source: srcSel.value, lines: linesSel.value, filter: filterIn.value });
        if (follow) { p.set('follow', '1'); p.set('offset', String(state.offset)); p.set('cursor', state.cursor); }
        return root.getAttribute('data-tail') + '?' + p.toString();
    }

    function append(lines, replace) {
        var nearBottom = out.scrollHeight - out.scrollTop - out.clientHeight < 40;
        if (replace) { out.textContent = ''; }
        if (lines.length) { out.textContent += lines.join('\n') + '\n'; }
        var all = out.textContent.split('\n');
        if (all.length > MAX_LINES) { out.textContent = all.slice(all.length - MAX_LINES).join('\n'); }
        if (replace || nearBottom) { out.scrollTop = out.scrollHeight; }
    }

    function load() {
        if (!srcSel.value) { return; }
        dl.href = root.getAttribute('data-download') + '?source=' + encodeURIComponent(srcSel.value);
        dl.classList.toggle('disabled', srcSel.value === 'panel' || srcSel.value.indexOf('journal:') === 0);
        status.textContent = 'Loading…';
        NimVor.fetchJson(url(false)).then(function (res) {
            state.offset = res.data.offset || 0;
            state.cursor = res.data.cursor || '';
            append(res.data.lines || [], true);
            status.textContent = (res.data.lines || []).length + ' line(s)' + (live.checked ? ' · live' : '');
        }).catch(function (e) { status.textContent = e.message; });
    }

    function poll() {
        clearTimeout(state.timer);
        if (!live.checked) { return; }
        state.timer = setTimeout(function () {
            if (document.hidden) { poll(); return; }
            NimVor.fetchJson(url(true)).then(function (res) {
                if (res.data.rotated) { load(); return; }
                state.offset = res.data.offset || state.offset;
                state.cursor = res.data.cursor || state.cursor;
                append(res.data.lines || [], false);
                status.textContent = 'Live · updated ' + new Date().toLocaleTimeString();
            }).catch(function (e) { status.textContent = e.message; }).finally(poll);
        }, 2000);
    }

    srcSel.addEventListener('change', function () { load(); poll(); });
    linesSel.addEventListener('change', load);
    filterIn.addEventListener('change', load);
    live.addEventListener('change', function () { if (live.checked) { load(); } poll(); });
    root.querySelector('[data-log-refresh]').addEventListener('click', load);
    load();
})();
