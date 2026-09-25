#!/usr/bin/env bash
#
# Post-install end-to-end check on a real server (run as root in CI after
# installer/install.sh --enable-writes). Drives the installed panel over
# HTTP like a browser would:
#   sign in → agent health → create a PHP website (served by Nginx and
#   PHP-FPM) → create a database for it → install a certificate (HTTPS
#   served) → back up files and database → damage both → restore → verify.
# Leaves nothing behind (the website is deleted at the end).

set -Eeuo pipefail
PORT=${PORT:-8090}
P="http://127.0.0.1:${PORT}"
RUN=$(date +%s | tail -c 6)
DOMAIN=${E2E_DOMAIN:-e2e$RUN.whacp.test}
DBNAME=e2e$RUN
J=$(mktemp -d)
CLI=(runuser -u whacp -- env WHACP_CONFIG=/etc/whacp/panel.php php /opt/whacp/panel/bin/whacp)
fail() { echo "FAIL: $*" >&2; tail -30 /var/log/whacp/panel.log 2>/dev/null >&2 || true; exit 1; }
pass() { echo "  ok  $*"; }
remove_site() { # remove_site ID (through the panel)
    CSRF_FROM=/sites/$1/settings post "/sites/$1/delete" "confirm_domain=$DOMAIN" remove_files=1 remove_user=1 >/dev/null || true
}
cleanup() {
    if [[ -n ${SITE:-} && -z ${DELETED:-} ]]; then remove_site "$SITE"; fi
    [[ -n ${DB:-} ]] && mysql -e "DROP DATABASE IF EXISTS \`$DB\`" 2>/dev/null || true
    rm -rf "$J"
}
trap cleanup EXIT

token() { grep -o 'name="_token" value="[^"]*"' "$1" | head -1 | sed 's/.*value="//;s/"$//'; }
get() { curl --noproxy '*' -sS -b "$J/c" -c "$J/c" -o "$J/page" -w '%{http_code}' "$P$1"; }
# post PATH field=value... (prints the redirect target)
post() {
    local path=$1; shift
    get "${CSRF_FROM:-/}" >/dev/null
    local args=(--data-urlencode "_token=$(token "$J/page")")
    for kv in "$@"; do args+=(--data-urlencode "$kv"); done
    curl --noproxy '*' -sS -b "$J/c" -c "$J/c" -o "$J/post" -w '%{redirect_url}' "${args[@]}" "$P$path"
}
flash() { get "$1" >/dev/null; grep -o 'wcp-flash[^>]*>[^<]*\|alert-[a-z]*[^>]*>[^<]*' "$J/page" | head -3 || true; }
waitfor() { # waitfor SECONDS description command...
    local t=$1 what=$2; shift 2
    for _ in $(seq 1 "$t"); do "$@" && return 0; sleep 1; done
    fail "$what"
}

echo "Post-install checks against $P"
curl --noproxy '*' -fsS "$P/healthz" >/dev/null || fail "panel health"
"${CLI[@]}" agent:check >/dev/null || fail "agent health"
pass "panel and agent healthy"

PASS="post-install-Check-4521"
printf '%s\n' "$PASS" | "${CLI[@]}" user:password admin >/dev/null
CSRF_FROM=/login
loc=$(post /login username=admin "password=$PASS")
[[ $loc == */ ]] || fail "sign-in (redirected to '$loc')"
unset CSRF_FROM
[[ $(get /) == 200 ]] || fail "dashboard after sign-in"
pass "signed in as the administrator"

# A leftover from an interrupted run is removed first.
old=$(php -r '$d = new PDO("sqlite:/var/lib/whacp/whacp.sqlite"); $s = $d->prepare("SELECT id FROM websites WHERE domain = ?"); $s->execute([$argv[1]]); echo (string) $s->fetchColumn();' "$DOMAIN" 2>/dev/null || true)
if [[ -n $old ]]; then remove_site "$old"; fi

# Website (PHP 8.3, new account).
loc=$(post /websites/new "domain=$DOMAIN" kind=php php_version=8.3 aliases= redirects= client_max_body_mb=64)
[[ $loc =~ /sites/([0-9]+) ]] || fail "website not created: $(flash /websites/new)"
SITE=${BASH_REMATCH[1]}
ROOT=/home/whacp/$DOMAIN
waitfor 30 "website files" test -d "$ROOT/public_html"
OWNER=$(stat -c %U "$ROOT/public_html")
echo '<?php echo "e2e-ok-" . PHP_VERSION;' > "$ROOT/public_html/index.php"
chown "$OWNER:$(stat -c %G "$ROOT/public_html")" "$ROOT/public_html/index.php"
waitfor 20 "PHP page served" sh -c "curl --noproxy '*' -fsS -H 'Host: $DOMAIN' http://127.0.0.1/index.php | grep -q '^e2e-ok-8.3'"
pass "website $DOMAIN created and served by Nginx + PHP-FPM 8.3 (account $OWNER)"

# Database linked to the website.
CSRF_FROM=/databases
post /databases name=$DBNAME "website_id=$SITE" create_user=1 >/dev/null
DB=$(mysql -N -e "SHOW DATABASES" | grep -E "(^|_)$DBNAME\$" | head -1 || true)
[[ -n $DB ]] || fail "database not created: $(flash /databases)"
mysql -e "DROP TABLE IF EXISTS \`$DB\`.t; CREATE TABLE \`$DB\`.t (v VARCHAR(20)); INSERT INTO \`$DB\`.t VALUES ('before-backup');"
pass "database $DB created"

# Certificate (self-signed; issuing from Let's Encrypt needs a public name).
openssl req -x509 -newkey rsa:2048 -nodes -days 30 -subj "/CN=$DOMAIN" -addext "subjectAltName=DNS:$DOMAIN" -keyout "$J/key.pem" -out "$J/cert.pem" 2>/dev/null
CSRF_FROM=/sites/$SITE/ssl
post "/sites/$SITE/ssl/custom" "certificate=$(cat "$J/cert.pem")" "private_key=$(cat "$J/key.pem")" chain= >/dev/null
waitfor 20 "HTTPS served" sh -c "curl --noproxy '*' -ksS --resolve $DOMAIN:443:127.0.0.1 https://$DOMAIN/index.php | grep -q '^e2e-ok-'"
nginx -t 2>/dev/null || fail "nginx -t after the certificate"
pass "certificate installed, HTTPS served, nginx -t passes"

# Backup (files + databases), damage, restore.
CSRF_FROM=/sites/$SITE/backups
post "/sites/$SITE/backups" files=1 databases=1 encrypt=1 keep_local=1 label=e2e >/dev/null
BACKUP=
for _ in $(seq 1 90); do
    get "/sites/$SITE/backups" >/dev/null
    BACKUP=$(grep -o "/sites/$SITE/backups/[0-9]*/restore" "$J/page" | head -1 | sed 's#.*/backups/##;s#/restore##' || true)
    [[ -n $BACKUP ]] && break
    sleep 1
done
if [[ -z $BACKUP ]]; then
    php -r '$d = new PDO("sqlite:/var/lib/whacp/whacp.sqlite"); foreach ($d->query("SELECT id, status, error, job_id FROM backups ORDER BY id DESC LIMIT 3") as $r) { echo json_encode($r), "\n"; }' >&2 || true
    fail "backup not listed"
fi
pass "encrypted backup #$BACKUP created"

rm -f "$ROOT/public_html/index.php"
mysql -e "UPDATE \`$DB\`.t SET v = 'damaged';"
post "/sites/$SITE/backups/$BACKUP/restore" restore_files=1 "restore_databases[]=$DB" "confirm_domain=$DOMAIN" >/dev/null
waitfor 120 "files restored" test -f "$ROOT/public_html/index.php"
waitfor 60 "database restored" sh -c "[ \"\$(mysql -N -e 'SELECT v FROM \`$DB\`.t')\" = before-backup ]"
waitfor 20 "restored site served" sh -c "curl --noproxy '*' -fsS -H 'Host: $DOMAIN' http://127.0.0.1/index.php | grep -q '^e2e-ok-'"
pass "restore brought back the file and the database row; the site is served"

# Cron "run now": the command runs as the website's account, with no
# capabilities, and cannot write outside what that account may write.
CSRF_FROM=/sites/$SITE/cron
post "/sites/$SITE/cron" "schedule=0 0 1 1 *" "command=cd $ROOT && { id -u; grep ^CapEff /proc/self/status; } > cron-ran.txt; touch /etc/whacp-cron-escape 2>/dev/null; true" description=e2e enabled=1 >/dev/null
JOB=$(php -r '$d = new PDO("sqlite:/var/lib/whacp/whacp.sqlite"); $s = $d->prepare("SELECT MAX(id) FROM cron_jobs WHERE website_id = ?"); $s->execute([$argv[1]]); echo (string) $s->fetchColumn();' "$SITE")
[[ -n $JOB ]] || fail "cron job not created: $(flash /sites/$SITE/cron)"
post "/sites/$SITE/cron/$JOB/run" >/dev/null
waitfor 60 "cron job ran" test -s "$ROOT/cron-ran.txt"
[[ $(head -1 "$ROOT/cron-ran.txt") == "$(id -u "$OWNER")" ]] || fail "cron ran as uid $(head -1 "$ROOT/cron-ran.txt"), not $OWNER"
[[ $(stat -c %U "$ROOT/cron-ran.txt") == "$OWNER" ]] || fail "cron output owned by $(stat -c %U "$ROOT/cron-ran.txt")"
grep -q 'CapEff:[[:space:]]*0000000000000000' "$ROOT/cron-ran.txt" || fail "cron command had capabilities: $(cat "$ROOT/cron-ran.txt")"
[[ ! -e /etc/whacp-cron-escape ]] || { rm -f /etc/whacp-cron-escape; fail "cron command wrote to /etc"; }
pass "cron \"run now\" ran as $OWNER without capabilities and could not write to /etc"

# Clean up through the panel.
CSRF_FROM=/sites/$SITE/settings
post "/sites/$SITE/delete" "confirm_domain=$DOMAIN" remove_files=1 remove_user=1 >/dev/null
DELETED=1
waitfor 30 "website removed" sh -c "! test -e /etc/nginx/sites-enabled/whacp-$DOMAIN.conf"
mysql -e "DROP DATABASE IF EXISTS \`$DB\`" || true
nginx -t 2>/dev/null || fail "nginx -t after deleting the website"
pass "website deleted; nginx -t passes"
echo "Post-install checks passed."
