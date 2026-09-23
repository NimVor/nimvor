// Dashboard live metrics: polls /dashboard/live and renders lightweight SVG
// sparklines. No charting library is needed.
(function () {
    'use strict';

    var root = document.getElementById('wcp-dashboard');
    if (!root) { return; }
    var url = root.getAttribute('data-live-url');
    var interval = Math.max(3, parseInt(root.getAttribute('data-refresh'), 10) || 10) * 1000;
    var statusEl = document.querySelector('[data-live-status]');
    var timer = null;
    var failures = 0;
    var SVG = 'http://www.w3.org/2000/svg';

    function fmtBytes(n) {
        if (n === null || n === undefined) { return '—'; }
        var u = ['B', 'KB', 'MB', 'GB', 'TB'];
        var i = 0;
        while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
        return (i === 0 ? n.toFixed(0) : n.toFixed(1)) + ' ' + u[i];
    }

    function fmtDuration(s) {
        s = Math.floor(s);
        var d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
        return d > 0 ? d + 'd ' + h + 'h' : (h > 0 ? h + 'h ' + m + 'm' : m + 'm');
    }

    function pathFor(values, max, w, h) {
        if (values.length < 2) { return null; }
        var step = w / (values.length - 1);
        var pts = values.map(function (v, i) {
            var y = h - (Math.min(v, max) / max) * (h - 2) - 1;
            return [i * step, y];
        });
        var line = 'M' + pts.map(function (p) { return p[0].toFixed(1) + ' ' + p[1].toFixed(1); }).join(' L');
        return { line: line, area: line + ' L' + w + ' ' + h + ' L0 ' + h + ' Z' };
    }

    function draw(el, seriesList) {
        var w = 300, h = 60;
        var svg = document.createElementNS(SVG, 'svg');
        svg.setAttribute('viewBox', '0 0 ' + w + ' ' + h);
        svg.setAttribute('preserveAspectRatio', 'none');
        seriesList.forEach(function (s, idx) {
            var p = pathFor(s.values, s.max, w, h);
            if (!p) { return; }
            var suffix = idx === 0 ? '' : '-2';
            var area = document.createElementNS(SVG, 'path');
            area.setAttribute('d', p.area);
            area.setAttribute('class', 'area' + suffix);
            var line = document.createElementNS(SVG, 'path');
            line.setAttribute('d', p.line);
            line.setAttribute('class', 'line' + suffix);
            svg.appendChild(area);
            svg.appendChild(line);
        });
        el.replaceChildren(svg);
    }

    function setMetric(key, value, detail, pct) {
        var card = root.querySelector('[data-metric="' + key + '"]');
        if (!card) { return; }
        card.querySelector('[data-value]').textContent = value;
        card.querySelector('[data-detail]').textContent = detail;
        card.classList.toggle('is-warn', pct !== null && pct >= 75 && pct < 90);
        card.classList.toggle('is-crit', pct !== null && pct >= 90);
    }

    function renderServices(list) {
        var ul = root.querySelector('[data-services]');
        if (!ul) { return; }
        ul.replaceChildren();
        if (!list || list.length === 0) {
            var empty = document.createElement('li');
            empty.className = 'list-group-item text-body-secondary small';
            empty.textContent = 'No monitored services found.';
            ul.appendChild(empty);
            return;
        }
        list.slice(0, 8).forEach(function (s) {
            var li = document.createElement('li');
            li.className = 'list-group-item d-flex justify-content-between align-items-center small';
            var left = document.createElement('span');
            var dot = document.createElement('span');
            dot.className = 'wcp-service-state' + (s.active_state === 'active' ? ' is-active' : (s.active_state === 'failed' ? ' is-failed' : ''));
            left.appendChild(dot);
            left.appendChild(document.createTextNode(s.unit.replace(/\.service$/, '')));
            var right = document.createElement('span');
            right.className = 'text-body-secondary';
            right.textContent = s.active_state === 'active' ? 'running' : s.active_state + ' (' + s.sub_state + ')';
            li.appendChild(left);
            li.appendChild(right);
            ul.appendChild(li);
        });
    }

    function render(data) {
        var m = data.metrics || {};
        var hist = data.history || [];
        var rootDisk = (m.disks || []).filter(function (d) { return d.mount_point === '/'; })[0] || (m.disks || [])[0];

        setMetric('cpu', (m.cpu_percent || 0).toFixed(1) + '%', (m.load ? 'load ' + m.load.load1.toFixed(2) : ''), m.cpu_percent || 0);
        if (m.memory) {
            setMetric('mem', m.memory.used_percent.toFixed(1) + '%', fmtBytes(m.memory.used_bytes) + ' of ' + fmtBytes(m.memory.total_bytes), m.memory.used_percent);
        }
        if (rootDisk) {
            setMetric('disk', rootDisk.used_percent.toFixed(1) + '%', fmtBytes(rootDisk.used_bytes) + ' of ' + fmtBytes(rootDisk.total_bytes), rootDisk.used_percent);
        }
        if (m.load) {
            setMetric('load', m.load.load1.toFixed(2), m.load.load5.toFixed(2) + ' · ' + m.load.load15.toFixed(2) + ' (5/15 min)', null);
        }
        var up = document.querySelector('[data-uptime]');
        if (up && m.uptime_seconds) { up.textContent = fmtDuration(m.uptime_seconds); }

        var rx = 0, tx = 0;
        (m.network || []).forEach(function (n) { rx += n.rx_bytes_per_sec; tx += n.tx_bytes_per_sec; });
        var rxEl = root.querySelector('[data-net-rx]');
        var txEl = root.querySelector('[data-net-tx]');
        if (rxEl) { rxEl.textContent = fmtBytes(rx) + '/s'; }
        if (txEl) { txEl.textContent = fmtBytes(tx) + '/s'; }

        var col = function (k) { return hist.map(function (p) { return p[k] || 0; }); };
        root.querySelectorAll('[data-spark]').forEach(function (el) {
            var key = el.getAttribute('data-spark');
            if (key === 'net') {
                var rxs = col('rx_bps'), txs = col('tx_bps');
                var max = Math.max.apply(null, rxs.concat(txs).concat([1024]));
                draw(el, [{ values: rxs, max: max }, { values: txs, max: max }]);
            } else if (key === 'load1') {
                var loads = col('load1');
                draw(el, [{ values: loads, max: Math.max.apply(null, loads.concat([1])) }]);
            } else {
                draw(el, [{ values: col(key), max: 100 }]);
            }
        });
        renderServices(data.services);
    }

    function tick() {
        if (document.hidden) { schedule(); return; }
        WhaCP.fetchJson(url).then(function (data) {
            failures = 0;
            render(data);
            if (statusEl) { statusEl.textContent = 'Live · updated ' + new Date().toLocaleTimeString(); }
        }).catch(function (err) {
            failures++;
            if (statusEl) { statusEl.textContent = 'Live data unavailable: ' + err.message; }
        }).finally(schedule);
    }

    function schedule() {
        clearTimeout(timer);
        // Back off while the agent is unreachable.
        timer = setTimeout(tick, interval * Math.min(6, 1 + failures));
    }

    document.addEventListener('visibilitychange', function () { if (!document.hidden) { tick(); } });
    tick();
})();
