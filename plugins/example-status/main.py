#!/usr/bin/env python3
"""Example WhaCP plugin: a status page with a note.

A plugin is an ordinary program that listens on the Unix socket named in
plugin.json. WhaCP signs the user in, checks the route's permission and the
CSRF token, then forwards the request here; the answer is shown inside the
panel. Run it as the same user as the panel, e.g. from a systemd unit.
"""
import html
import json
import os
import socketserver
import sys
import time
from http.server import BaseHTTPRequestHandler
from urllib.parse import parse_qs

SOCKET = os.environ.get("PLUGIN_SOCKET", "/run/whacp/plugins/example-status.sock")
STATE = os.environ.get("PLUGIN_STATE", "/var/lib/whacp/plugins/example-status")


def read(name, default=""):
    try:
        with open(os.path.join(STATE, name), encoding="utf-8") as f:
            return f.read()
    except OSError:
        return default


def write(name, value):
    os.makedirs(STATE, mode=0o700, exist_ok=True)
    with open(os.path.join(STATE, name), "w", encoding="utf-8") as f:
        f.write(value)


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):  # the panel logs requests
        pass

    def reply(self, status, body, ctype="text/html; charset=utf-8", **headers):
        data = body.encode()
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        for k, v in headers.items():
            self.send_header(k.replace("_", "-"), v)
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path.split("?")[0] not in ("/", ""):
            return self.reply(404, "Not found", "text/plain")
        load = " / ".join("%.2f" % x for x in os.getloadavg())
        up = ""
        try:
            with open("/proc/uptime") as f:
                up = "%d days" % (float(f.read().split()[0]) // 86400)
        except OSError:
            pass
        token = self.headers.get("X-WhaCP-CSRF", "")
        page = """
<div class="card"><div class="card-body">
  <h2 class="h5">Status</h2>
  <p>Uptime: {up}. Load: {load}. Last website created: {last}.</p>
  <form method="post" action="/plugins/example-status/note">
    <input type="hidden" name="_token" value="{token}">
    <label class="form-label" for="note">Note</label>
    <textarea class="form-control mb-2" id="note" name="note" rows="3" maxlength="500">{note}</textarea>
    <button class="btn btn-primary btn-sm">Save</button>
  </form>
</div></div>""".format(
            up=html.escape(up or "unknown"), load=html.escape(load),
            last=html.escape(read("last_website") or "none yet"),
            token=html.escape(token), note=html.escape(read("note")),
        )
        self.reply(200, page, X_WhaCP_Title="Status note")

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(min(length, 1 << 16)).decode("utf-8", "replace")
        if self.path.startswith("/_events/"):
            event = self.path[len("/_events/"):]
            if event == "website.created":
                write("last_website", str(json.loads(raw or "{}").get("domain", ""))[:200])
            return self.reply(204, "", "text/plain")
        if self.path == "/note":
            write("note", parse_qs(raw).get("note", [""])[0].strip()[:500])
            return self.reply(303, "", "text/plain", Location="/plugins/example-status")
        self.reply(404, "Not found", "text/plain")


class Server(socketserver.UnixStreamServer):
    def get_request(self):
        request, _ = super().get_request()
        return request, ("local", 0)


if __name__ == "__main__":
    os.makedirs(os.path.dirname(SOCKET), mode=0o750, exist_ok=True)
    try:
        os.unlink(SOCKET)
    except FileNotFoundError:
        pass
    os.umask(0o077)
    with Server(SOCKET, Handler) as srv:
        print("example-status listening on", SOCKET, file=sys.stderr)
        srv.serve_forever()
