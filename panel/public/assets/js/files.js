// WhaCP file manager. All paths are relative to the website root; the agent
// enforces confinement, this script only renders and sends requests.
(function () {
    'use strict';
    var root = document.getElementById('wcp-files');
    if (!root) { return; }
    var api = root.getAttribute('data-api');
    var uploadUrl = root.getAttribute('data-upload');
    var downloadUrl = root.getAttribute('data-download');
    var cwd = root.getAttribute('data-start') || '';
    var rows = root.querySelector('[data-fm-rows]');
    var crumbs = root.querySelector('[data-fm-crumbs]');
    var editor = { modal: null, path: '', mtime: null };
    var CHUNK = 1024 * 1024;

    function join(a, b) { return a ? a + '/' + b : b; }
    function parent(p) { var i = p.lastIndexOf('/'); return i < 0 ? '' : p.slice(0, i); }
    function fmtSize(n) { var u = ['B', 'KB', 'MB', 'GB']; var i = 0; while (n >= 1024 && i < 3) { n /= 1024; i++; } return (i ? n.toFixed(1) : n) + ' ' + u[i]; }
    function el(tag, cls, text) { var e = document.createElement(tag); if (cls) { e.className = cls; } if (text !== undefined) { e.textContent = text; } return e; }

    function call(op, params) {
        return WhaCP.fetchJson(api, { method: 'POST', body: JSON.stringify(Object.assign({ op: op }, params || {})) })
            .then(function (res) { return res.data; });
    }
    function fail(err) {
        var d = err.details || {};
        WhaCP.toast((d.message || err.message) + (d.details ? ' — ' + d.details : ''), 'danger');
    }

    function prompt(title, label, value) {
        return new Promise(function (resolve) {
            var m = document.getElementById('fm-prompt');
            m.querySelector('.modal-title').textContent = title;
            m.querySelector('[data-fm-prompt-label]').textContent = label;
            var input = m.querySelector('input');
            input.value = value || '';
            var form = m.querySelector('form');
            var modal = bootstrap.Modal.getOrCreateInstance(m);
            var done = false;
            form.onsubmit = function (e) { e.preventDefault(); done = true; modal.hide(); resolve(input.value.trim()); };
            m.addEventListener('hidden.bs.modal', function () { if (!done) { resolve(null); } }, { once: true });
            m.addEventListener('shown.bs.modal', function () { input.focus(); input.select(); }, { once: true });
            modal.show();
        });
    }

    function renderCrumbs() {
        crumbs.replaceChildren();
        var parts = cwd ? cwd.split('/') : [];
        var li = el('li', 'breadcrumb-item');
        var a = el('a', '', 'root'); a.href = '#'; a.dataset.go = '';
        li.appendChild(a); crumbs.appendChild(li);
        parts.forEach(function (p, i) {
            var li2 = el('li', 'breadcrumb-item');
            var path = parts.slice(0, i + 1).join('/');
            if (i === parts.length - 1) { li2.textContent = p; li2.classList.add('active'); }
            else { var a2 = el('a', '', p); a2.href = '#'; a2.dataset.go = path; li2.appendChild(a2); }
            crumbs.appendChild(li2);
        });
    }

    function row(entry, path) {
        var tr = el('tr');
        var name = el('td', 'wcp-fm-name');
        var icon = entry.type === 'dir' ? 'bi-folder-fill text-warning' : (entry.type === 'link' ? 'bi-link-45deg' : 'bi-file-earmark');
        var i = el('i', 'bi ' + icon); name.appendChild(i);
        name.appendChild(document.createTextNode(entry.name));
        name.dataset.open = path; name.dataset.type = entry.type;
        tr.appendChild(name);
        tr.appendChild(el('td', 'text-end small', entry.type === 'dir' ? '' : fmtSize(entry.size)));
        tr.appendChild(el('td', 'small font-monospace', entry.mode));
        tr.appendChild(el('td', 'small text-body-secondary', new Date(entry.mtime).toLocaleString()));
        var act = el('td', 'text-end text-nowrap');
        [['bi-download', 'download', 'Download'], ['bi-pencil', 'rename', 'Rename / move'], ['bi-files', 'copy', 'Copy'], ['bi-shield-lock', 'chmod', 'Permissions'], ['bi-trash', 'delete', 'Delete']].forEach(function (b) {
            if (b[1] === 'download' && entry.type !== 'file') { return; }
            var btn = el('button', 'btn btn-sm btn-link px-1' + (b[1] === 'delete' ? ' text-danger' : ''));
            btn.type = 'button'; btn.title = b[2]; btn.setAttribute('aria-label', b[2] + ' ' + entry.name);
            btn.dataset.op = b[1]; btn.dataset.path = path; btn.dataset.mode = entry.mode;
            btn.appendChild(el('i', 'bi ' + b[0]));
            act.appendChild(btn);
        });
        tr.appendChild(act);
        return tr;
    }

    function load() {
        renderCrumbs();
        return call('list', { path: cwd }).then(function (entries) {
            rows.replaceChildren();
            if (cwd) {
                var up = el('tr'); var td = el('td', 'wcp-fm-name', ''); td.colSpan = 5;
                td.appendChild(el('i', 'bi bi-arrow-90deg-up')); td.appendChild(document.createTextNode('..'));
                td.dataset.open = parent(cwd); td.dataset.type = 'dir'; up.appendChild(td); rows.appendChild(up);
            }
            if (entries.length === 0) { var e = el('tr'); var t = el('td', 'text-body-secondary small', 'This folder is empty.'); t.colSpan = 5; e.appendChild(t); rows.appendChild(e); }
            entries.forEach(function (en) { rows.appendChild(row(en, join(cwd, en.name))); });
        }).catch(fail);
    }

    function openFile(path) {
        call('read', { path: path }).then(function (f) {
            if (f.binary || f.too_big) {
                WhaCP.toast(f.too_big ? 'The file is too large for the editor; download it instead.' : 'This is a binary file; download it instead.', 'secondary');
                return;
            }
            editor.path = path; editor.mtime = f.mtime;
            var m = document.getElementById('fm-editor');
            m.querySelector('.modal-title').textContent = path;
            m.querySelector('[data-fm-editor]').value = f.content;
            m.querySelector('[data-fm-editor-info]').textContent = fmtSize(f.size);
            editor.modal = bootstrap.Modal.getOrCreateInstance(m);
            editor.modal.show();
        }).catch(fail);
    }

    document.addEventListener('click', function (e) {
        var go = e.target.closest('[data-go]');
        if (go && root.contains(go)) { e.preventDefault(); cwd = go.dataset.go; load(); return; }
        var open = e.target.closest('[data-open]');
        if (open && root.contains(open)) {
            if (open.dataset.type === 'dir') { cwd = open.dataset.open; load(); } else { openFile(open.dataset.open); }
            return;
        }
        var btn = e.target.closest('[data-op]');
        if (btn && root.contains(btn)) { operate(btn.dataset.op, btn.dataset.path, btn.dataset.mode); return; }
        var tb = e.target.closest('[data-fm-action]');
        if (tb) { toolbar(tb.dataset.fmAction); }
        if (e.target.closest('[data-fm-save]')) { save(); }
    });

    function operate(op, path, mode) {
        var name = path.split('/').pop();
        if (op === 'download') { window.location.href = downloadUrl + '?path=' + encodeURIComponent(path); return; }
        if (op === 'rename') {
            prompt('Rename or move', 'New path (relative to the website root)', path).then(function (to) {
                if (to && to !== path) { call('rename', { from: path, to: to }).then(load).catch(fail); }
            });
        } else if (op === 'copy') {
            prompt('Copy', 'Copy to (relative path)', path + '-copy').then(function (to) {
                if (to) { call('copy', { from: path, to: to }).then(load).catch(fail); }
            });
        } else if (op === 'chmod') {
            prompt('Permissions', 'Octal mode, e.g. 644 or 755', mode).then(function (m) {
                if (m) { call('chmod', { path: path, mode: m }).then(load).catch(fail); }
            });
        } else if (op === 'delete') {
            prompt('Delete ' + name, 'Type DELETE to permanently remove “' + name + '”' , '').then(function (v) {
                if (v === 'DELETE') { call('delete', { paths: [path] }).then(function () { WhaCP.toast('Deleted.', 'success'); load(); }).catch(fail); }
            });
        }
    }

    function toolbar(action) {
        if (action === 'refresh') { load(); }
        if (action === 'newfolder') {
            prompt('New folder', 'Folder name', '').then(function (n) { if (n) { call('mkdir', { path: join(cwd, n) }).then(load).catch(fail); } });
        }
        if (action === 'newfile') {
            prompt('New file', 'File name', '').then(function (n) {
                if (n) { call('write', { path: join(cwd, n), content: '' }).then(function () { load(); openFile(join(cwd, n)); }).catch(fail); }
            });
        }
        if (action === 'search') {
            var q = root.querySelector('[data-fm-search]').value.trim();
            if (!q) { return; }
            call('search', { path: '', query: q }).then(function (res) {
                rows.replaceChildren();
                res.results.forEach(function (h) { rows.appendChild(row({ name: h.path, type: h.type, size: h.size, mode: '', mtime: Date.now() }, h.path)); });
                if (res.truncated) { WhaCP.toast('Showing the first results only.', 'secondary'); }
            }).catch(fail);
        }
    }

    function save() {
        var content = document.querySelector('[data-fm-editor]').value;
        call('write', { path: editor.path, content: content, expected_mtime: editor.mtime }).then(function (r) {
            editor.mtime = r.mtime;
            WhaCP.toast('Saved.', 'success');
            load();
        }).catch(fail);
    }

    // Chunked uploads (1 MiB) so large files pass PHP and agent limits.
    root.querySelector('[data-fm-upload]').addEventListener('change', function (e) {
        var files = Array.prototype.slice.call(e.target.files);
        e.target.value = '';
        var bar = root.querySelector('[data-fm-bar]');
        var box = root.querySelector('[data-fm-progress]');
        var text = root.querySelector('[data-fm-progress-text]');
        box.classList.remove('d-none');
        var total = files.reduce(function (s, f) { return s + f.size; }, 0) || 1;
        var done = 0;
        function sendFile(i) {
            if (i >= files.length) { box.classList.add('d-none'); WhaCP.toast('Upload complete.', 'success'); load(); return; }
            var f = files[i];
            function chunk(offset) {
                var blob = f.slice(offset, offset + CHUNK);
                var fd = new FormData();
                fd.append('chunk', blob, 'chunk');
                fd.append('path', join(cwd, f.name));
                fd.append('offset', String(offset));
                fd.append('final', offset + CHUNK >= f.size ? '1' : '0');
                text.textContent = 'Uploading ' + f.name + '…';
                return fetch(uploadUrl, { method: 'POST', body: fd, credentials: 'same-origin', headers: { 'X-CSRF-Token': WhaCP.csrfToken(), 'Accept': 'application/json' } })
                    .then(function (r) { return r.json(); })
                    .then(function (res) {
                        if (!res.success) { throw new Error((res.error && res.error.message) || 'Upload failed'); }
                        done += blob.size;
                        bar.style.width = Math.round(done / total * 100) + '%';
                        if (offset + CHUNK < f.size) { return chunk(offset + CHUNK); }
                        return null;
                    });
            }
            chunk(0).then(function () { sendFile(i + 1); }).catch(function (err) { box.classList.add('d-none'); fail(err); });
        }
        sendFile(0);
    });

    load();
})();
