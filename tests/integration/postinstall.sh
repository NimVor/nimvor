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
export NIMVOR_TELEMETRY=0 # the installer must not report test servers
PORT=${PORT:-8090}
P="http://127.0.0.1:${PORT}"
RUN=$(date +%s | tail -c 6)
DOMAIN=${E2E_DOMAIN:-e2e$RUN.nimvor.test}
DBNAME=e2e$RUN
J=$(mktemp -d)
CLI=(runuser -u nimvor -- env NIMVOR_CONFIG=/etc/nimvor/panel.php php /opt/nimvor/panel/bin/nimvor)
fail() { echo "FAIL: $*" >&2; tail -30 /var/log/nimvor/panel.log 2>/dev/null >&2 || true; exit 1; }
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
flash() { get "$1" >/dev/null; grep -o 'nim-flash[^>]*>[^<]*\|alert-[a-z]*[^>]*>[^<]*' "$J/page" | head -3 || true; }
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
# An administrator's first visit opens the setup wizard (and marks it seen);
# after that the dashboard answers directly.
[[ $(get /setup) == 200 ]] || fail "setup wizard on the first visit"
[[ $(get /) == 200 ]] || fail "dashboard after sign-in"
pass "signed in as the administrator"

# A leftover from an interrupted run is removed first.
old=$(php -r '$d = new PDO("sqlite:/var/lib/nimvor/nimvor.sqlite"); $s = $d->prepare("SELECT id FROM websites WHERE domain = ?"); $s->execute([$argv[1]]); echo (string) $s->fetchColumn();' "$DOMAIN" 2>/dev/null || true)
if [[ -n $old ]]; then remove_site "$old"; fi

# Website (the system's PHP version, set by CI; default 8.3; new account).
PHPV=${NIMVOR_TEST_PHP:-$(cat /etc/nimvor-ci-php 2>/dev/null || echo 8.3)}
loc=$(post /websites/new "domain=$DOMAIN" kind=php php_version=$PHPV aliases= redirects= client_max_body_mb=64)
[[ $loc =~ /sites/([0-9]+) ]] || fail "website not created: $(flash /websites/new)"
SITE=${BASH_REMATCH[1]}
ROOT=/home/nimvor/$DOMAIN
waitfor 30 "website files" test -d "$ROOT/public_html"
OWNER=$(stat -c %U "$ROOT/public_html")
echo '<?php echo "e2e-ok-" . PHP_VERSION;' > "$ROOT/public_html/index.php"
chown "$OWNER:$(stat -c %G "$ROOT/public_html")" "$ROOT/public_html/index.php"
waitfor 20 "PHP page served" sh -c "curl --noproxy '*' -fsS -H 'Host: $DOMAIN' http://127.0.0.1/index.php | grep -q '^e2e-ok-$PHPV'"
pass "website $DOMAIN created and served by Nginx + PHP-FPM $PHPV (account $OWNER)"

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
    php -r '$d = new PDO("sqlite:/var/lib/nimvor/nimvor.sqlite"); foreach ($d->query("SELECT id, status, error, job_id FROM backups ORDER BY id DESC LIMIT 3") as $r) { echo json_encode($r), "\n"; }' >&2 || true
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
post "/sites/$SITE/cron" "schedule=0 0 1 1 *" "command=cd $ROOT && { id -u; grep ^CapEff /proc/self/status; sendmail -bp 2>&1 | head -1; } > cron-ran.txt; touch /etc/nimvor-cron-escape 2>/dev/null; true" description=e2e enabled=1 >/dev/null
JOB=$(php -r '$d = new PDO("sqlite:/var/lib/nimvor/nimvor.sqlite"); $s = $d->prepare("SELECT MAX(id) FROM cron_jobs WHERE website_id = ?"); $s->execute([$argv[1]]); echo (string) $s->fetchColumn();' "$SITE")
[[ -n $JOB ]] || fail "cron job not created: $(flash /sites/$SITE/cron)"
post "/sites/$SITE/cron/$JOB/run" >/dev/null
waitfor 60 "cron job ran" test -s "$ROOT/cron-ran.txt"
[[ $(head -1 "$ROOT/cron-ran.txt") == "$(id -u "$OWNER")" ]] || fail "cron ran as uid $(head -1 "$ROOT/cron-ran.txt"), not $OWNER"
[[ $(stat -c %U "$ROOT/cron-ran.txt") == "$OWNER" ]] || fail "cron output owned by $(stat -c %U "$ROOT/cron-ran.txt")"
grep -q 'CapEff:[[:space:]]*0000000000000000' "$ROOT/cron-ran.txt" || fail "cron command had capabilities: $(cat "$ROOT/cron-ran.txt")"
[[ ! -e /etc/nimvor-cron-escape ]] || { rm -f /etc/nimvor-cron-escape; fail "cron command wrote to /etc"; }
pass "cron \"run now\" ran as $OWNER without capabilities and could not write to /etc"
# Inside that unit sendmail is NimVor's (the installed agent binary, which
# submits over SMTP); Postfix's would hang there (F-25).
if [[ -e /usr/sbin/sendmail ]]; then
    grep -q 'sendmail (NimVor)' "$ROOT/cron-ran.txt" || fail "sendmail in the cron unit is not NimVor's: $(tail -1 "$ROOT/cron-ran.txt")"
    pass "sendmail inside the account's unit is NimVor's SMTP sendmail"
fi

# A component installed through the panel and the installed agent: the
# package manager runs outside the agent's sandbox (Ubuntu 24.04: APT's
# switch to _apt; RHEL with SELinux: RPM scriptlets), its maintainer
# scripts create the service's user again, and the component is only
# reported installed once its service runs (health check).
if [[ -d /run/systemd/system ]]; then
    if command -v dnf >/dev/null; then
        MC_USER=memcached; dnf remove -y -q memcached >/dev/null
    else
        MC_USER=memcache; DEBIAN_FRONTEND=noninteractive apt-get purge -y -q memcached >/dev/null
    fi
    userdel "$MC_USER" 2>/dev/null || true
    get /packages >/dev/null
    out=$(curl --noproxy '*' -sS -b "$J/c" -c "$J/c" -H 'Accept: application/json' --data-urlencode "_token=$(token "$J/page")" --data-urlencode name=memcached "$P/packages/install")
    PJOB=$(grep -o '"job_id":"[a-f0-9]*"' <<<"$out" | cut -d'"' -f4 || true)
    [[ -n $PJOB ]] || fail "component install not started: $out"
    st=
    for _ in $(seq 1 600); do
        st=$(curl --noproxy '*' -sS -b "$J/c" -H 'Accept: application/json' "$P/jobs/$PJOB" | grep -o '"status":"[a-z]*"' | head -1 | cut -d'"' -f4 || true)
        [[ $st == succeeded || $st == failed ]] && break
        sleep 1
    done
    if [[ $st != succeeded ]]; then
        cat "/var/lib/nimvor-agent/jobs/$PJOB.log" >&2 2>/dev/null || true
        journalctl --no-pager -o cat -n 40 -u 'nimvor-pkg-*' >&2 || true
        fail "memcached through the agent: job $st"
    fi
    id "$MC_USER" >/dev/null 2>&1 || fail "the package's script did not create $MC_USER"
    systemctl is-active --quiet memcached || fail "memcached installed but not running"
    pass "memcached installed through the agent (package scripts ran, user $MC_USER created, service running)"
fi

# Security overview fixes through the installed agent (in its own mount
# namespace): hiding processes is reported as done, and taken back.
CSRF_FROM=/security
post /security/fix check=proc_hidepid op=apply >/dev/null
runuser -u nobody -- test -e /proc/1/cmdline && fail "hide processes: nobody still sees PID 1"
[[ $(get /security) == 200 ]] || fail "security overview after hiding processes"
grep -q 'Show all processes again' "$J/page" || { grep -o 'Process visibility.\{0,400\}' "$J/page" >&2; fail "the overview does not show processes as hidden"; }
post /security/fix check=proc_hidepid op=undo >/dev/null
runuser -u nobody -- test -e /proc/1/cmdline || fail "processes still hidden after the undo"
grep -q 'nimvor-proc\|hidepid' /etc/fstab && fail "fstab keeps the /proc entry after the undo"
pass "security overview: hiding processes is applied, reported and undone through the agent"

# Process manager through the installed agent: the privileged scan reads
# another account's program (a copy of sleep in /var/tmp, run as nobody, is
# flagged), and Kill reaches it after the start time is checked.
cp /bin/sleep /var/tmp/nimvor-e2e-sleep && chmod 0755 /var/tmp/nimvor-e2e-sleep
runuser -u nobody -- /var/tmp/nimvor-e2e-sleep 600 &
SPID=$!
sleep 1
SPID=$(pgrep -u nobody -f nimvor-e2e-sleep | head -1)
[[ -n $SPID ]] || fail "test process did not start"
[[ $(get /monitoring/processes) == 200 ]] || fail "process manager page"
grep -q 'could not be read' "$J/page" && { grep -o 'could not be read.\{0,400\}' "$J/page" >&2; fail "the privileged process scan failed"; }
grep -q 'runs a program from /var/tmp' "$J/page" || fail "the process in /var/tmp was not flagged"
START=$(awk '{print $22}' "/proc/$SPID/stat")
CSRF_FROM=/monitoring/processes
post /monitoring/processes/signal "pid=$SPID" "start=$((START + 1))" signal=KILL >/dev/null
kill -0 "$SPID" 2>/dev/null || fail "a signal with the wrong start time was sent"
post /monitoring/processes/signal "pid=$SPID" "start=$START" signal=KILL >/dev/null
waitfor 10 "the process was not killed" sh -c "! kill -0 $SPID 2>/dev/null"
rm -f /var/tmp/nimvor-e2e-sleep
pass "process manager: privileged scan flags /var/tmp programs; kill checks the start time"
[[ $(get "/monitoring/processes/trace?pid=1&start=$(awk '{print $22}' /proc/1/stat)") == 200 ]] || fail "trace of PID 1"
grep -q 'lib/systemd/systemd' "$J/page" || fail "the trace of PID 1 does not show its program (privileged detail)"
pass "process trace reads PID 1's details"

# Top bar, SSL services and the update page render on the installed panel.
[[ $(get /) == 200 ]] || fail "dashboard"
grep -q 'Load averages' "$J/page" || fail "top bar without load averages"
grep -q "$(hostname -s)" "$J/page" || fail "top bar without the host name"
[[ $(get /ssl/services) == 200 ]] || fail "SSL services page"
grep -q 'Mail server (SMTP, submission, IMAP)' "$J/page" || fail "SSL services page content"
[[ $(get /nimvor/update) == 200 ]] || fail "update page"
grep -q 'Source folder' "$J/page" || fail "update page content"
[[ -s /etc/nimvor/source.json ]] || fail "the installer did not record its source folder"
pass "top bar, SSL services and update pages"

# Clean up through the panel.
CSRF_FROM=/sites/$SITE/settings
post "/sites/$SITE/delete" "confirm_domain=$DOMAIN" remove_files=1 remove_user=1 >/dev/null
DELETED=1
waitfor 30 "website removed" sh -c "! test -e /etc/nginx/sites-enabled/nimvor-$DOMAIN.conf"
mysql -e "DROP DATABASE IF EXISTS \`$DB\`" || true
nginx -t 2>/dev/null || fail "nginx -t after deleting the website"
pass "website deleted; nginx -t passes"

# Installer recovery. A second run refuses and points to upgrade.sh. An
# installation interrupted before its services were written (simulated by
# removing panel.php and the panel unit) is reported with what is missing
# and --reinstall; --reinstall completes it and keeps the token, the key,
# the database and write mode.
INSTALLER=$(cd "$(dirname "$0")/../.." && pwd)/installer/install.sh
out=$("$INSTALLER" --yes 2>&1) && fail "a second install.sh succeeded"
grep -q "upgrade.sh" <<<"$out" || fail "second install did not point to upgrade.sh: $out"
TOKEN=$(sha256sum < /etc/nimvor/agent.token)
KEY=$(sha256sum < /etc/nimvor/panel.key)
USERS=$("${CLI[@]}" users:count)
mv /etc/nimvor/panel.php "$J/panel.php.orig"
mv /etc/systemd/system/nimvor-panel.service "$J/nimvor-panel.service.orig"
out=$("$INSTALLER" --yes 2>&1) && fail "install.sh over an incomplete installation succeeded"
grep -q "did not finish" <<<"$out" && grep -q -- "--reinstall" <<<"$out" && grep -q "panel.php" <<<"$out" && grep -q "nimvor-panel.service" <<<"$out" \
    || fail "incomplete installation not explained: $out"
"$INSTALLER" --reinstall --yes >"$J/reinstall.log" 2>&1 || { cat "$J/reinstall.log"; fail "install.sh --reinstall did not complete the installation"; }
[[ -s /etc/nimvor/panel.php && -f /etc/systemd/system/nimvor-panel.service ]] || fail "--reinstall did not recreate panel.php and the unit"
[[ $(sha256sum < /etc/nimvor/agent.token) == "$TOKEN" && $(sha256sum < /etc/nimvor/panel.key) == "$KEY" ]] || fail "--reinstall replaced the token or the panel key"
[[ $("${CLI[@]}" users:count) == "$USERS" ]] || fail "--reinstall changed the users"
grep -q '"allow_write_actions": true' /etc/nimvor/agent.json || fail "--reinstall turned write mode off"
"${CLI[@]}" agent:check >/dev/null || fail "panel cannot reach the agent after --reinstall"
waitfor 30 "panel answering after --reinstall" curl -fsS "$P/healthz"
pass "installer: refuses a second run, explains an interrupted one, --reinstall completes it and keeps token, key, users and write mode"
echo "Post-install checks passed."
