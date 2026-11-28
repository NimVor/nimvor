// Dashboard history charts: small SVG line/area charts with a time axis, subtle grid, tooltips and a range
// selector. No charting library. They draw only what the monitor really recorded (GET /dashboard/series):
// a value that was not measured is a gap, and with too little history the chart says so instead of drawing
// anything. Elements are found by [data-nim-chart]; widgets that are (re)loaded call this again through the
// "nim:widget-loaded" event.
(function () {
    'use strict';

    var SVG = 'http://www.w3.org/2000/svg';
    var RANGE_KEY = 'nim.chart.range';
    var cache = {}; // range -> { at, promise }

    function store(key, value) {
        try { if (value === undefined) { return window.localStorage.getItem(key); } window.localStorage.setItem(key, value); } catch (e) { /* storage may be unavailable */ }
        return null;
    }

    function fmtPct(v) { return (Math.abs(v) >= 10 || v === 0 ? v.toFixed(0) : v.toFixed(1)) + '%'; }
    function fmtNum(v) { return Math.abs(v) >= 100 ? v.toFixed(0) : (Math.abs(v) >= 10 ? v.toFixed(1) : v.toFixed(2)); }
    function fmtSize(v) {
        var u = ['B', 'KB', 'MB', 'GB'], i = 0;
        while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
        return (i === 0 || v >= 100 ? v.toFixed(0) : v.toFixed(1)) + ' ' + u[i];
    }
    function fmtBytes(v) {
        var u = ['B', 'KB', 'MB', 'GB'], i = 0;
        while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
        return (i === 0 || v >= 100 ? v.toFixed(0) : v.toFixed(1)) + ' ' + u[i] + '/s';
    }

    // What each chart shows: the series (their keys come from /dashboard/series), the scale and the wording.
    var PRESETS = {
        cpu: { label: 'CPU', series: [{ key: 'cpu', label: 'CPU used' }], max: 100, fmt: fmtPct },
        mem: { label: 'Memory', series: [{ key: 'mem', label: 'Memory used' }, { key: 'swap', label: 'Swap used' }], max: 100, fmt: fmtPct, none: null },
        load: { label: 'Load', series: [{ key: 'load1', label: '1 min' }, { key: 'load5', label: '5 min' }, { key: 'load15', label: '15 min' }], fmt: fmtNum },
        net: { label: 'Network', series: [{ key: 'rx', label: 'In' }, { key: 'tx', label: 'Out' }], fmt: fmtBytes },
        disk: { label: 'Disk', series: [{ key: 'disk', label: 'Fullest disk' }], max: 100, fmt: fmtPct },
        svc: { label: 'Services', series: [{ key: 'svc', label: 'Services running' }], max: 100, fmt: fmtPct, none: 'Service availability has not been recorded yet: it is kept from now on.' },
        db_conn: { label: 'Connections', series: [{ key: 'db_conn', label: 'Connections' }], fmt: function (v) { return v.toFixed(0); }, none: 'No database history yet: it is recorded every five minutes while a database server runs.' },
        // A website's Resources page (GET /sites/{id}/resources/series). The values are what the recorded samples measured.
        site_cpu: { label: 'CPU', series: [{ key: 'cpu', label: 'CPU of the PHP workers (100% = one core)' }], fmt: function (v) { return (v >= 10 ? v.toFixed(0) : v.toFixed(1)) + '%'; }, none: 'No CPU samples of this website in this range yet.' },
        site_mem: { label: 'Memory', series: [{ key: 'mem', label: 'PHP memory (average)' }, { key: 'mem_max', label: 'PHP memory (highest)' }], fmt: fmtSize, none: 'No memory samples of this website in this range yet.' },
        site_io: { label: 'Disk', series: [{ key: 'read', label: 'Read' }, { key: 'write', label: 'Written' }], fmt: fmtBytes, none: 'No disk samples of this website in this range yet.' },
        site_req: { label: 'Requests', series: [{ key: 'rpm', label: 'Requests per minute' }], fmt: fmtNum, none: 'No access-log samples of this website in this range yet.' },
        site_traffic: { label: 'Traffic', series: [{ key: 'bw_out', label: 'Sent to visitors' }], fmt: fmtBytes, none: 'No access-log samples of this website in this range yet.' },
        site_err: { label: 'Errors', series: [{ key: 'e5', label: '5xx per minute' }, { key: 'e4', label: '4xx per minute' }], fmt: fmtNum, none: 'No access-log samples of this website in this range yet.' },
        site_workers: { label: 'PHP workers', series: [{ key: 'workers', label: 'Workers (average)' }, { key: 'workers_max', label: 'Workers (most)' }], fmt: function (v) { return v.toFixed(v >= 10 ? 0 : 1); }, none: 'No PHP worker samples of this website in this range yet.' },
        site_fpm: { label: 'Concurrent requests', series: [{ key: 'fpm_active', label: 'Busy workers (average)' }, { key: 'fpm_active_max', label: 'Busy workers (most)' }, { key: 'fpm_queue', label: 'Waiting for a worker (most)' }], fmt: function (v) { return v.toFixed(v >= 10 ? 0 : 1); }, none: 'No PHP-FPM status samples yet: an administrator can switch the status page on under Resource usage.' },
        db_qps: { label: 'Statements per second', series: [{ key: 'db_qps', label: 'Statements/s' }], fmt: fmtNum, none: 'No database history yet: it is recorded every five minutes while a database server runs.' }
    };

    function load(url, range) {
        var c = cache[range];
        if (c && Date.now() - c.at < 30000) { return c.promise; }
        var p = window.NimVor.fetchJson(url + (url.indexOf('?') < 0 ? '?' : '&') + 'range=' + encodeURIComponent(range));
        cache[range] = { at: Date.now(), promise: p };
        p.catch(function () { delete cache[range]; });
        return p;
    }

    function el(tag, attrs, text) {
        var n = tag === 'svg' || tag === 'path' || tag === 'line' || tag === 'circle' || tag === 'rect' || tag === 'text' ? document.createElementNS(SVG, tag) : document.createElement(tag);
        Object.keys(attrs || {}).forEach(function (k) { n.setAttribute(k, attrs[k]); });
        if (text !== undefined) { n.textContent = text; }
        return n;
    }

    // A "nice" upper bound for a value scale, and its tick step.
    function niceMax(v) {
        if (!(v > 0)) { return 1; }
        var p = Math.pow(10, Math.floor(Math.log10(v))), f = v / p;
        return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10) * p;
    }

    function timeLabel(t, span) {
        var d = new Date(t * 1000);
        if (span > 2 * 86400) { return d.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric' }); }
        return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
    }

    function render(box, data) {
        var preset = PRESETS[box.getAttribute('data-metric')];
        var note = box.querySelector('.nim-chart-note');
        var plot = box.querySelector('.nim-chart-plot');
        var sum = box.querySelector('.nim-chart-sum');
        var legend = box.querySelector('.nim-chart-legend');
        plot.replaceChildren();
        legend.replaceChildren();
        sum.textContent = '';
        note.hidden = true;

        var pts = (data.points || []).filter(function (p) { return p.t >= data.from; });
        // Series that have at least two measurements in the range; the rest are left out.
        var series = preset.series.filter(function (s) {
            return pts.filter(function (p) { return p[s.key] !== null && p[s.key] !== undefined; }).length >= 2;
        });
        if (series.length === 0) {
            var first = data.since ? new Date(data.since * 1000).toLocaleString() : null;
            note.textContent = preset.none || ('Not enough history yet: the monitor takes a sample every five minutes' + (first ? ' (the first was at ' + first + ')' : '') + '.');
            note.hidden = false;
            return;
        }
        var width = Math.max(220, box.clientWidth || 300), height = parseInt(box.getAttribute('data-height'), 10) || 150;
        var m = { l: 34, r: 8, t: 8, b: 20 }, w = width - m.l - m.r, h = height - m.t - m.b;
        var t0 = Math.max(data.from, pts[0].t), t1 = Math.max(data.to, pts[pts.length - 1].t);
        if (t1 <= t0) { t1 = t0 + 1; }
        var max = preset.max || 0;
        if (!max) {
            series.forEach(function (s) { pts.forEach(function (p) { if (p[s.key] > max) { max = p[s.key]; } }); });
            max = niceMax(max * 1.1);
        }
        var X = function (t) { return m.l + ((t - t0) / (t1 - t0)) * w; };
        var Y = function (v) { return m.t + h - (Math.min(v, max) / max) * h; };

        var svg = el('svg', { viewBox: '0 0 ' + width + ' ' + height, width: width, height: height, role: 'img' });
        // Grid and value labels.
        var rows = height < 110 ? 2 : 4;
        for (var i = 0; i <= rows; i++) {
            var v = (max / rows) * i, y = Y(v);
            svg.appendChild(el('line', { x1: m.l, x2: width - m.r, y1: y, y2: y, 'class': 'grid' }));
            svg.appendChild(el('text', { x: m.l - 5, y: y + 3, 'class': 'tick', 'text-anchor': 'end' }, preset.fmt(v)));
        }
        // Time labels.
        for (var k = 0; k < 5; k++) {
            var tt = t0 + ((t1 - t0) / 4) * k;
            svg.appendChild(el('text', { x: X(tt), y: height - 5, 'class': 'tick', 'text-anchor': k === 0 ? 'start' : (k === 4 ? 'end' : 'middle') }, timeLabel(tt, t1 - t0)));
        }
        // A gap longer than three sample steps (or a missing value) breaks the line.
        var steps = [];
        for (var j = 1; j < pts.length; j++) { steps.push(pts[j].t - pts[j - 1].t); }
        steps.sort(function (a, b) { return a - b; });
        var gap = Math.max(900, (steps.length ? steps[Math.floor(steps.length / 2)] : 300) * 3);
        series.forEach(function (s, idx) {
            var segs = [], cur = [], prevT = null;
            pts.forEach(function (p) {
                var val = p[s.key];
                if (val === null || val === undefined || (prevT !== null && p.t - prevT > gap)) {
                    if (cur.length) { segs.push(cur); cur = []; }
                }
                if (val !== null && val !== undefined) { cur.push([X(p.t), Y(val)]); prevT = p.t; }
            });
            if (cur.length) { segs.push(cur); }
            segs.forEach(function (seg) {
                if (seg.length === 1) { svg.appendChild(el('circle', { cx: seg[0][0], cy: seg[0][1], r: 2.5, 'class': 'dot s-' + s.key })); return; }
                var line = 'M' + seg.map(function (q) { return q[0].toFixed(1) + ' ' + q[1].toFixed(1); }).join(' L');
                if (idx === 0) { svg.appendChild(el('path', { d: line + ' L' + seg[seg.length - 1][0].toFixed(1) + ' ' + Y(0) + ' L' + seg[0][0].toFixed(1) + ' ' + Y(0) + ' Z', 'class': 'area s-' + s.key })); }
                svg.appendChild(el('path', { d: line, 'class': 'line s-' + s.key }));
            });
        });
        // Hover: a guide line, a dot per series and a tooltip with the exact values.
        var guide = el('line', { y1: m.t, y2: m.t + h, 'class': 'guide', visibility: 'hidden' });
        svg.appendChild(guide);
        var dots = series.map(function (s) { var c = el('circle', { r: 3.5, 'class': 'hover s-' + s.key, visibility: 'hidden' }); svg.appendChild(c); return c; });
        var over = el('rect', { x: m.l, y: m.t, width: w, height: h, fill: 'transparent' });
        svg.appendChild(over);
        var tip = box.querySelector('.nim-chart-tip');
        tip.hidden = true;
        function move(ev) {
            var r = svg.getBoundingClientRect(), x = ev.clientX - r.left;
            var best = null, bd = Infinity;
            pts.forEach(function (p) { var d = Math.abs(X(p.t) - x); if (d < bd) { bd = d; best = p; } });
            if (!best) { return; }
            guide.setAttribute('x1', X(best.t)); guide.setAttribute('x2', X(best.t)); guide.setAttribute('visibility', 'visible');
            tip.replaceChildren(el('div', { 'class': 'nim-chart-tip-time' }, new Date(best.t * 1000).toLocaleString()));
            series.forEach(function (s, n) {
                var val = best[s.key];
                if (val === null || val === undefined) { dots[n].setAttribute('visibility', 'hidden'); }
                else { dots[n].setAttribute('cx', X(best.t)); dots[n].setAttribute('cy', Y(val)); dots[n].setAttribute('visibility', 'visible'); }
                var row = el('div', { 'class': 'nim-chart-tip-row' });
                row.appendChild(el('span', { 'class': 'nim-chart-key s-' + s.key }));
                row.appendChild(document.createTextNode(s.label + ': ' + (val === null || val === undefined ? 'not measured' : preset.fmt(val))));
                tip.appendChild(row);
            });
            tip.hidden = false;
            var tx = X(best.t) + 12;
            if (tx + tip.offsetWidth > width) { tx = X(best.t) - 12 - tip.offsetWidth; }
            tip.style.left = Math.max(0, tx) + 'px';
            tip.style.top = (m.t + 4) + 'px';
        }
        function leave() {
            guide.setAttribute('visibility', 'hidden');
            dots.forEach(function (d) { d.setAttribute('visibility', 'hidden'); });
            tip.hidden = true;
        }
        over.addEventListener('pointermove', move);
        over.addEventListener('pointerdown', move);
        over.addEventListener('pointerleave', leave);
        plot.appendChild(svg);

        // Legend (only with more than one series) and the numbers for the main series.
        if (series.length > 1) {
            series.forEach(function (s) {
                var item = el('span', { 'class': 'nim-chart-legend-item' });
                item.appendChild(el('span', { 'class': 'nim-chart-key s-' + s.key }));
                item.appendChild(document.createTextNode(s.label));
                legend.appendChild(item);
            });
        }
        var main = series[0].key, vals = pts.map(function (p) { return p[main]; }).filter(function (x) { return x !== null && x !== undefined; });
        var avg = vals.reduce(function (a, b) { return a + b; }, 0) / vals.length;
        sum.textContent = 'Average ' + preset.fmt(avg) + ' · peak ' + preset.fmt(Math.max.apply(null, vals)) + ' · ' + vals.length + ' samples since ' + timeLabel(t0, t1 - t0 > 0 ? t1 - t0 : 1);
        svg.setAttribute('aria-label', preset.label + ' over the selected period: average ' + preset.fmt(avg) + ', peak ' + preset.fmt(Math.max.apply(null, vals)) + '.');
    }

    function mount(box) {
        if (box.getAttribute('data-mounted') === '1') { return; }
        box.setAttribute('data-mounted', '1');
        box.classList.add('nim-chart');
        box.innerHTML = '<div class="nim-chart-legend"></div><div class="nim-chart-plot"></div><div class="nim-chart-tip" hidden></div><div class="nim-chart-note" hidden></div><div class="nim-chart-sum"></div>';
        var url = box.getAttribute('data-series-url');
        var last = null;
        function draw() {
            var host = box.closest('[data-chart-scope]') || box.parentNode;
            var range = box.getAttribute('data-fixed-range') || store(RANGE_KEY) || box.getAttribute('data-default-range') || '24h';
            box.setAttribute('data-range', range);
            if (host) {
                host.querySelectorAll('[data-range-button]').forEach(function (b) { b.classList.toggle('active', b.getAttribute('data-range-button') === range); b.setAttribute('aria-pressed', b.getAttribute('data-range-button') === range ? 'true' : 'false'); });
            }
            load(url, range).then(function (data) { last = data; render(box, data); }).catch(function () {
                var note = box.querySelector('.nim-chart-note');
                note.textContent = 'The history could not be loaded. Use the refresh button to try again.';
                note.hidden = false;
            });
        }
        box.addEventListener('nim:chart-redraw', draw);
        if (window.ResizeObserver) {
            var raf = 0, width = box.clientWidth;
            new ResizeObserver(function () {
                if (!last || Math.abs(box.clientWidth - width) < 4) { return; }
                width = box.clientWidth;
                cancelAnimationFrame(raf);
                raf = requestAnimationFrame(function () { render(box, last); });
            }).observe(box);
        }
        draw();
    }

    function init(root) {
        (root || document).querySelectorAll('[data-nim-chart]').forEach(mount);
    }

    // Range buttons and metric tabs inside a widget (event delegation: the widget's HTML is replaced on refresh).
    document.addEventListener('click', function (e) {
        var rb = e.target.closest ? e.target.closest('[data-range-button]') : null;
        if (rb) {
            store(RANGE_KEY, rb.getAttribute('data-range-button'));
            document.querySelectorAll('[data-nim-chart]').forEach(function (b) { b.dispatchEvent(new CustomEvent('nim:chart-redraw')); });
            return;
        }
        var tab = e.target.closest ? e.target.closest('[data-chart-tab]') : null;
        if (tab) {
            var scope = tab.closest('[data-chart-scope]');
            var box = scope ? scope.querySelector('[data-nim-chart]') : null;
            if (!box) { return; }
            box.setAttribute('data-metric', tab.getAttribute('data-chart-tab'));
            scope.querySelectorAll('[data-chart-tab]').forEach(function (t) { t.classList.toggle('active', t === tab); t.setAttribute('aria-selected', t === tab ? 'true' : 'false'); });
            showPane(scope, tab.getAttribute('data-chart-tab'));
            var key = scope.getAttribute('data-chart-scope');
            if (key) { store('nim.chart.tab.' + key, tab.getAttribute('data-chart-tab')); }
            box.dispatchEvent(new CustomEvent('nim:chart-redraw'));
        }
    });

    // The readings that belong to the chosen metric are shown, the others hidden.
    function showPane(scope, metric) {
        scope.querySelectorAll('[data-chart-pane]').forEach(function (p) { p.hidden = p.getAttribute('data-chart-pane') !== metric; });
    }

    // Restore the tab last used in a widget.
    function restoreTabs(root) {
        (root || document).querySelectorAll('[data-chart-scope]').forEach(function (scope) {
            var key = scope.getAttribute('data-chart-scope'), saved = key ? store('nim.chart.tab.' + key) : null;
            if (!saved) { return; }
            var tab = scope.querySelector('[data-chart-tab="' + saved + '"]');
            var box = scope.querySelector('[data-nim-chart]');
            if (tab && box) {
                box.setAttribute('data-metric', saved);
                scope.querySelectorAll('[data-chart-tab]').forEach(function (t) { t.classList.toggle('active', t === tab); t.setAttribute('aria-selected', t === tab ? 'true' : 'false'); });
                showPane(scope, saved);
            }
        });
    }

    // Sizes of server-drawn bars arrive as data attributes (inline style attributes are blocked by the page's policy).
    function sizes(root) {
        (root || document).querySelectorAll('[data-bar-height]').forEach(function (n) { n.style.height = Math.max(2, parseFloat(n.getAttribute('data-bar-height')) || 0) + '%'; });
        (root || document).querySelectorAll('[data-grow]').forEach(function (n) { n.style.flexGrow = n.getAttribute('data-grow'); });
    }

    function start(root) { sizes(root); restoreTabs(root); init(root); }
    document.addEventListener('DOMContentLoaded', function () { start(document); });
    document.addEventListener('nim:widget-loaded', function (e) { start(e.detail || document); });
    window.NimCharts = { init: start };
})();
