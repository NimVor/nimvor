#!/usr/bin/env bash
#
# install.sh --reinstall --public-https on an installed server (run as root
# in CI after postinstall.sh): the panel answers at https://SERVER-IP:8443/
# through Nginx, signs in with an HTTPS-only cookie, sees HTTPS and the
# client's address through the proxy, redirects plain HTTP on the port, and
# the firewall (UFW or firewalld, when active) allows the port.

set -Eeuo pipefail
export NIMVOR_TESTING=1 # the installer must not report test servers
HTTPS_PORT=${HTTPS_PORT:-8443}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
CLI=(runuser -u nimvor -- env NIMVOR_CONFIG=/etc/nimvor/panel.php php /opt/nimvor/panel/bin/nimvor)
J=$(mktemp -d)
trap 'rm -rf "$J"' EXIT
fail() { echo "FAIL: $*" >&2; journalctl -u nginx -n 20 --no-pager >&2 2>/dev/null || true; tail -20 /var/log/nimvor/panel.log >&2 2>/dev/null || true; exit 1; }
pass() { echo "  ok  $*"; }

"$ROOT/installer/install.sh" --reinstall --yes "--public-https=${HTTPS_PORT}" >"$J/install.log" 2>&1 || { cat "$J/install.log"; fail "install.sh --reinstall --public-https"; }
grep -q "panel answers over HTTPS on port ${HTTPS_PORT}" "$J/install.log" || { cat "$J/install.log"; fail "the installer's HTTPS health check"; }
pass "install.sh --reinstall --public-https=${HTTPS_PORT}"

IP=$(hostname -I | awk '{print $1}')
P="https://${IP}:${HTTPS_PORT}"
c=(curl --noproxy '*' -sS -k -b "$J/c" -c "$J/c")
code=$("${c[@]}" -D "$J/h" -o "$J/page" -w '%{http_code}' "$P/login")
[[ $code == 200 ]] || fail "GET $P/login: $code"
grep -qi '^set-cookie: nimvor_session=.*; secure' "$J/h" || fail "the session cookie is not HTTPS-only: $(grep -i '^set-cookie' "$J/h")"
grep -qi '^strict-transport-security:' "$J/h" || fail "no HSTS header: the panel does not see HTTPS through the proxy"
openssl x509 -in "$(sed -n 's/^ *ssl_certificate  *\([^;]*\);/\1/p' /etc/nginx/*/nimvor-panel.conf | head -1)" -noout -ext subjectAltName | grep -q "IP Address:${IP}" || fail "the certificate does not name ${IP}"
pass "sign-in page over HTTPS at $P (HTTPS-only cookie, HSTS, certificate for ${IP})"

PASS="public-https-Check-7731"
printf '%s\n' "$PASS" | "${CLI[@]}" user:password admin >/dev/null
tok=$(grep -o 'name="_token" value="[^"]*"' "$J/page" | head -1 | sed 's/.*value="//;s/"$//')
loc=$("${c[@]}" -o /dev/null -w '%{redirect_url}' --data-urlencode "_token=$tok" --data-urlencode username=admin --data-urlencode "password=$PASS" "$P/login")
[[ $loc == "$P/" ]] || fail "sign-in redirected to '$loc'"
[[ $("${c[@]}" -o "$J/page" -w '%{http_code}' "$P/") == 200 ]] && grep -q 'action="/logout"' "$J/page" || fail "dashboard after sign-in"
grep '"auth.login"' /var/log/nimvor/panel.log | tail -1 | grep -q "\"ip\":\"${IP}\"" || fail "the audit log does not show the client address ${IP}: $(grep '"auth.login"' /var/log/nimvor/panel.log | tail -1)"
pass "signed in over HTTPS; the audit log shows the client address"

[[ $(curl --noproxy '*' -s -o /dev/null -w '%{http_code} %{redirect_url}' "http://${IP}:${HTTPS_PORT}/login") == "301 https://${IP}:${HTTPS_PORT}/login" ]] || fail "plain HTTP on the port is not redirected to HTTPS"
pass "plain HTTP on ${HTTPS_PORT} redirects to HTTPS"

if command -v ufw >/dev/null && ufw status | grep -q "Status: active"; then
    ufw status | grep -q "^${HTTPS_PORT}/tcp .*ALLOW" || fail "UFW does not allow ${HTTPS_PORT}/tcp"
    pass "UFW allows ${HTTPS_PORT}/tcp"
elif command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
    firewall-cmd --query-port="${HTTPS_PORT}/tcp" >/dev/null || fail "firewalld does not allow ${HTTPS_PORT}/tcp"
    pass "firewalld allows ${HTTPS_PORT}/tcp"
fi
# Web terminal: the WebSocket reaches the agent through Nginx (write mode).
grep -q '"terminal_listen": "127.0.0.1:8091"' /etc/nimvor/agent.json || fail "the installer did not set terminal_listen"
if grep -q '"allow_write_actions": true' /etc/nimvor/agent.json; then
    ws=(curl --noproxy '*' -sk --http1.1 -o /dev/null -w '%{http_code}' --max-time 3 -H 'Connection: Upgrade' -H 'Upgrade: websocket'
        -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==')
    code=$("${ws[@]}" -H 'Origin: https://evil.example' "$P/terminal/ws" || true)
    [[ $code == 403 ]] || fail "a WebSocket from another origin was not refused: $code"
    code=$("${ws[@]}" -H "Origin: $P" "$P/terminal/ws" || true)
    [[ $code == 101 ]] || fail "the terminal WebSocket did not open through Nginx: $code"
    pass "web terminal WebSocket proxied to the agent"
fi

echo "Public HTTPS checks passed."
