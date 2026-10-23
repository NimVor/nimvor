package http

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// This suite drives the website write routes end to end: CSRF, permission and
// tenant gates, form re-render on validation failure, the agent apply/delete
// calls, and the audit trail. The agent is a recording fake on a Unix socket.

type sitesAgent struct {
	mu          sync.Mutex
	actions     []string
	params      []map[string]any
	failApply   bool
	// failActions fails a specific action (agent error paths), e.g.
	// "sites.permissions" -> "the agent refused".
	failActions map[string]string
	// dbDown makes db.server_status report a stopped server.
	dbDown bool
	// jobs answers jobs.get by job id (the stored reply is the full
	// envelope); an unknown id gets the agent's not_found error.
	jobs map[string]string
	// termKillClosed is the terminal.kill reply's closed count.
	termKillClosed int
	// secPending is the security.pending reply's pending object ("" = none).
	secPending string
	// wpScanReply overrides the wordpress.scan reply ("" = the default).
	wpScanReply string
	// wpInfoReply overrides the wordpress.info reply ("" = the default).
	wpInfoReply string
	// wpProtectionReply overrides the protect.status reply ("" = the default).
	wpProtectionReply string
	// wpSecurityReply overrides the wordpress.security reply ("" = the default).
	wpSecurityReply string
	// wpRestoreReply overrides the wordpress.restore_points reply ("" = the default).
	wpRestoreReply string
	// wpLoginFile overrides the wordpress.login file ("" = the default).
	wpLoginFile string
	// wpDebugLog shapes the wordpress.manage debug_log reply: "" = a log with
	// lines, "empty" = an empty log, "missing" = no file.
	wpDebugLog string
	// wpManageJobID overrides the update ops' job id ("" = the default).
	wpManageJobID string
	// wpVerifyReply shapes the verify_core reply: "" = two differing files,
	// "ok" = every file matches.
	wpVerifyReply string
	// wpCopyJobID is the wordpress.copy reply's job id ("" = the default).
	wpCopyJobID string
	// backupJobID is the backup.create reply's job id ("" = the default).
	backupJobID string
	// shellSetEffective overrides the shell.set reply's effective mode
	// ("" = the requested mode, i.e. it takes effect immediately).
	shellSetEffective string
	// fwActive is the firewall.status reply's active flag.
	fwActive bool
	// guardChanged is the guarded (firewall.*/protection) replies' changed
	// flag; false → {changed:false} ("Nothing to change.").
	guardChanged bool
	// f2bManaged is the fail2ban.status reply's managed_config ("" = the
	// panel's fresh-install defaults).
	f2bManaged string
	// f2bInstalled is the fail2ban.status reply's installed flag.
	f2bInstalled bool
	// f2bRunning is the fail2ban.status reply's running flag (default true).
	f2bRunning bool
	// anonRemoved is the db.drop_anonymous_users reply's removed list
	// ("" = the default two, "none" = none).
	anonRemoved string
	// mailData overrides the mail.status reply's data ("" = configured
	// with the test hostname).
	mailData string
	// mailFilters is the mailbox's stored filters + autoresponder (the
	// mail.filters_get data); "" = one enabled filter, mail.filters_save
	// updates it.
	mailFilters string
	// mailQueue overrides the mail.queue reply's data (""); mail.queue_action
	// and the rest of the mail.admin.* replies have fixed test shapes.
	mailQueue string
	// dnsData overrides the dns.status reply's data ("" = installed,
	// configured, running, serving bob.example at serial 7).
	dnsData string
	// zoneParse overrides the dns.zone_parse reply's data ("" = one new A
	// record, the zone's own NS and one skipped line).
	zoneParse string
	// failStatus is the HTTP status a failed action's reply is written with
	// (e.g. "dns.zone_delete" -> 404).
	failStatus map[string]int
	// ftpStatus shapes the ftp.status reply: "" = installed + configured +
	// active (one account for u_bob, SFTP on with one key), "missing" =
	// not installed, "stopped" = configured but not active.
	ftpStatus string
	// avInstalled is the av.status reply's installed flag (default true).
	avInstalled bool
	// avVersion is the av.status reply's version string ("" = the full
	// "ClamAV 1.4.3/27781/2026-09-27").
	avVersion string
	// avFreshclam is the av.status reply's freshclam flag (default true).
	avFreshclam bool
	// avUpdaterLog is the av.status reply's updater_log ("" = none).
	avUpdaterLog string
	// deployKey is the deploy.key reply's public key ("" = a fixed test key).
	deployKey string
	// deployRunJob is the deploy.run reply's job id ("" = 0123456789d0a001).
	deployRunJob string
	// mongoStatus is the mongo.status reply: "" = installed+running,
	// "down" = installed but not running, "missing" = not installed.
	mongoStatus string
	// mongoOverview overrides the mongo.overview reply ("" = the default
	// prefixed + foreign databases/users).
	mongoOverview string
	// mongoBackups overrides the mongo.backups reply ("" = the default).
	mongoBackups string
	// hulkAvailable is the hulk.status reply's available flag (default true);
	// false = nftables is not installed.
	hulkAvailable bool
	// hulkActive is the hulk.status reply's active flag (default true).
	hulkActive bool
	// modsecActive is the modsec.status reply's active flag (default true).
	modsecActive bool
	// avScanJobID is the av.scan reply's job id (default "0123456789abcdef").
	avScanJobID string
	// avQuarantineItemsJSON is the av.quarantine_list reply's items array
	// (default: empty).
	avQuarantineItemsJSON string
	// sslInspectJSON overrides the ssl.inspect reply (" " = the default
	// valid 129-day Let's Encrypt certificate).
	sslInspectJSON string
	// sslRenewalJSON overrides the ssl.renewal_status reply ("" = certbot
	// installed + the renewal timer active).
	sslRenewalJSON string
	// sitesInspectJSON overrides the sites.inspect reply ("" = the default:
	// a healthy site with a 123 MiB root and a stored config).
	sitesInspectJSON string
	// stats* configure the stats.status/stats.data replies. statsInstalled
	// defaults to true; statsEnabled defaults to false.
	statsInstalled   bool
	statsEnabled     bool
	statsMonthsJSON  string
	statsUpdatedAt   string
	statsDataFound   bool
	statsDataContent string
	// mc* configure the memcached.status reply. mcInstalled/mcActive default
	// to true; mcPort defaults to 11211; mcError "" = no error.
	mcInstalled bool
	mcActive    bool
	mcPort      int
	mcError     string
	// mcStats "" = no stats section (memcached answering, but no stats).
	mcStats string
	// installJobID is the packages.install reply's job id.
	installJobID string
	// redisInfoJSON overrides the redis.info reply's data ("" = the fixture).
	redisInfoJSON string
	// cronLogContent overrides the cron.log reply's content ("" = the
	// fixture: loose output + three framed runs, the last still running).
	cronLogContent string
	// discoveryInventory overrides the discovery.inventory reply ("" = the
	// default inventory: one discovered site + one WhaCP-managed site).
	discoveryInventory string
	// adoptPreviewJSON overrides the adopt.preview reply ("" = the default
	// clean preview with one unlink step and a kept database).
	adoptPreviewJSON string
	// adoptRunJobID is the adopt.run reply's job id ("" = 0123456789e0a001).
	adoptRunJobID string
	// dbOverviewJSON overrides the db.overview reply ("" = the default
	// a_main/b_main/legacy_shop fixture).
	dbOverviewJSON string
}

// defaultDiscoveryInventory is the fake's discovery.inventory fixture: one
// discovered website (old.test, PHP, Let's Encrypt certificate) that is NOT
// managed by WhaCP and one website whose configuration lives in the WhaCP
// location (managed.test), plus one cron job referencing the discovered
// website's directory.
const defaultDiscoveryInventory = `{
  "websites": {
    "websites": [
      {
        "id":"abc123","domain":"old.test","aliases":["www.old.test"],"kind":"php","enabled":true,
        "https":true,"https_redirect":true,"document_root":"/var/www/old","document_root_exists":true,
        "listen":["80","443 ssl"],
        "config_files":["/etc/nginx/sites-available/old"],
        "access_logs":["/var/log/nginx/old.access.log"],"error_logs":["/var/log/nginx/old.error.log"],
        "php":{"version":"8.1","pool":"old","user":"old","listen":"127.0.0.1:9000","resolved":true},
        "ssl":{"status":"valid","error":"","not_after":"2026-04-01T00:00:00Z","days_remaining":160,
               "issuer":"Let's Encrypt","cert_file":"/etc/letsencrypt/live/old.test/fullchain.pem",
               "key_file":"/etc/letsencrypt/live/old.test/privkey.pem","lets_encrypt":true,"covers_domain":true}
      },
      {
        "id":"whacp123","domain":"managed.test","aliases":[],"kind":"static","enabled":true,
        "document_root":"/home/whacp/managed.test/public_html","document_root_exists":true,
        "config_files":["/home/whacp/nginx/managed.test.conf"]
      }
    ],
    "total":2,"catch_all_vhosts":1,"warnings":[]
  },
  "cron": {"jobs":[
    {"schedule":"0 3 * * *","user":"old","command":"php /var/www/old/cron/daily.php"}
  ]}
}`

// defaultAdoptPreview is the fake's adopt.preview fixture: a clean preview
// with one unlink step (the enabled symlink) and a found database.
const defaultAdoptPreview = `{
  "domain":"old.test","source":"/var/www/old","target":"/home/whacp/old.test/public_html","user":"u_old",
  "files":12,"bytes":4096,"skipped_links":[],
  "database":"oldddb","database_source":"wp-config.php",
  "new_config":"server { # whacp }","old_config":"server { root /var/www/old; }",
  "blockers":[],"warnings":[],
  "steps":[{"file":"/etc/nginx/sites-available/old","action":"unlink",
            "path":"/etc/nginx/sites-enabled/old","names":["old.test"]}]
}`

// defaultCronLogContent is the fake's cron.log fixture: one loose line
// before the framing, a successful run, a failing run with no output and a
// manual run that has not finished (no end marker).
const defaultCronLogContent = "early loose output line\n" +
	"### whacp-run start 2026-10-21T03:00:00Z\n" +
	"first run line\n" +
	"### whacp-run end 2026-10-21T03:00:02Z exit=0\n" +
	"### whacp-run start 2026-10-22T03:00:00Z\n" +
	"### whacp-run end 2026-10-22T03:00:01Z exit=1\n" +
	"### whacp-run start 2026-10-22T08:15:30Z manual\n" +
	"manual output"

// setJob registers the jobs.get reply for a job id.
func (sa *sitesAgent) setJob(id, reply string) {
	sa.mu.Lock()
	sa.jobs[id] = reply
	sa.mu.Unlock()
}

// strOf renders a JSON-decoded value as a string (the fake's replies).
func strOf(v any) string {
	s, _ := v.(string)
	return s
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// guardedReply is the agent's guarded() reply: a change pending its
// confirmation (or changed:false when nothing moved).
func guardedReply(changed bool, id, kind, summary string) []byte {
	if !changed {
		return []byte(`{"success":true,"data":{"changed":false}}`)
	}
	return []byte(`{"success":true,"data":{"changed":true,"pending":{"id":"` + id + `","kind":"` + kind + `","summary":"` + summary + `","deadline":"2026-01-01T12:03:00Z"}}}`)
}

// defaultWpInfoReply is the wordpress.info fixture: an outdated site with
// one plugin update, debugging on, the file editor on, no HTTPS.
const defaultWpInfoReply = `{
  "version":"6.7.1","php":"8.3.1","mysql":"10.11.6-MariaDB","multisite":false,
  "siteurl":"http://bob.example/","home":"http://bob.example/","title":"Bob's Blog",
  "search_engines":true,
  "core":{"latest":"6.8","php_needed":"7.2.24","checked_at":1761000000},
  "plugins":[
    {"name":"hello","title":"Hello Dolly","version":"1.7","active":true,"update":"1.8","auto_update":false},
    {"name":"jetpack","title":"Jetpack","version":"13.1","active":true,"update":"","auto_update":true}
  ],
  "themes":[
    {"name":"twentytwentyfive","title":"Twenty Twenty-Five","version":"1.0","active":true,"update":""}
  ],
  "admins":[{"id":1,"login":"bob"}],
  "admin_login_exists":false,
  "constants":{"WP_DEBUG":true,"WP_DEBUG_DISPLAY":false,"DISALLOW_FILE_EDIT":false,"DISALLOW_FILE_MODS":false,"WP_AUTO_UPDATE_CORE":"'minor'","AUTOMATIC_UPDATER_DISABLED":false,"DISABLE_WP_CRON":false},
  "maintenance":false,
  "db":{"name":"u_bob_wp","host":"localhost","prefix":"wp_","tables":42,"bytes":5242880},
  "config_mode":"0644"
}`

func startSitesAgent(t *testing.T, failApply bool) (*sitesAgent, string, string) {
	t.Helper()
	sa := &sitesAgent{failApply: failApply, jobs: map[string]string{}, failActions: map[string]string{}, termKillClosed: 1, f2bRunning: true, avInstalled: true, avFreshclam: true, hulkAvailable: true, hulkActive: true, modsecActive: true, avScanJobID: "0123456789abcdef", statsInstalled: true}
	dir := t.TempDir()
	socket := filepath.Join(dir, "agent.sock")
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/actions/", func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Path[len("/v1/actions/"):]
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		sa.mu.Lock()
		sa.actions = append(sa.actions, action)
		sa.params = append(sa.params, p)
		sa.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if msg, fail := sa.failActions[action]; fail {
			if st := sa.failStatus[action]; st > 0 {
				w.WriteHeader(st)
			}
			_, _ = w.Write([]byte(`{"success":false,"error":{"code":"boom","message":"` + msg + `","request_id":"t"}}`))
			return
		}
		if sa.failApply && (action == "sites.apply" || action == "sites.delete") {
			_, _ = w.Write([]byte(`{"success":false,"error":{"code":"boom","message":"the agent refused","request_id":"t"}}`))
			return
		}
		switch action {
		case "discovery.php":
			_, _ = w.Write([]byte(`{"success":true,"data":{"versions":[
				{"version":"8.2","fpm_binary":"/usr/sbin/php-fpm8.2"},
				{"version":"8.3.1","fpm_binary":"/usr/sbin/php-fpm8.3"}]}}`))
		case "agent.status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"sites_root":"/home/whacp"}}`))
		case "dns.status":
			sa.mu.Lock()
			data := sa.dnsData
			sa.mu.Unlock()
			if data == "" {
				data = `{"installed":true,"configured":true,"running":true,"config_file":"/etc/ndn/zones.json","zones":[{"zone":"bob.example","serial":7}]}`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + data + `}`))
		case "dns.zone_apply":
			_, _ = w.Write([]byte(`{"success":true,"data":{"serial":7}}`))
		case "dns.zone_parse":
			sa.mu.Lock()
			data := sa.zoneParse
			sa.mu.Unlock()
			if data == "" {
				data = `{"records":[{"name":"ftp","type":"A","ttl":3600,"value":"198.51.100.7"},{"name":"@","type":"NS","ttl":0,"value":"ns1.example.com."}],"skipped":[{"line":3,"text":"HINFO bob.example","reason":"unsupported record type"}]}`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + data + `}`))
		case "dns.zone_render":
			_, _ = w.Write([]byte(`{"success":true,"data":{"text":"$ORIGIN bob.example\n@\t3600\tSOA\tns1.example.com. hostmaster@bob.example. 7 3600 900 604800 86400\n"}}`))
		case "dns.zone_delete":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "sites.delete":
			_, _ = w.Write([]byte(`{"success":true,"data":{"warnings":["The access log was kept."]}}`))
		case "discovery.inventory":
			sa.mu.Lock()
			data := sa.discoveryInventory
			sa.mu.Unlock()
			if data == "" {
				data = defaultDiscoveryInventory
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + data + `}`))
		case "adopt.preview":
			sa.mu.Lock()
			data := sa.adoptPreviewJSON
			sa.mu.Unlock()
			if data == "" {
				data = defaultAdoptPreview
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + data + `}`))
		case "adopt.run":
			sa.mu.Lock()
			jobID := sa.adoptRunJobID
			sa.mu.Unlock()
			if jobID == "" {
				jobID = "0123456789e0a001"
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"` + jobID + `"}}`))
		case "adopt.undo":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "runtime.status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"arch":"x86_64",
				"node":[{"kind":"node","version":"20.1.0","bin":"/usr/bin/node","binary":"/usr/bin/node"},
				       {"kind":"node","version":"22.0.0","bin":"/usr/bin/node","binary":"/usr/bin/node"}],
				"python":[],
				"system":[{"kind":"python","version":"3.12","bin":"/usr/bin/python3","binary":"/usr/bin/python3"},
				       {"kind":"python","version":"3.11","bin":"/usr/bin/python3","binary":"/usr/bin/python3"}]}}`))
		case "sites.permissions":
			_, _ = w.Write([]byte(`{"success":true,"data":{"checked":120,"fixed":3,"skipped_count":2,"errors":[]}}`))
		case "system.info":
			_, _ = w.Write([]byte(`{"success":true,"data":{"agent_version":"1.0.0","os":{"name":"Ubuntu"},"system":{"hostname":"srv","kernel":"6.8.0","architecture":"x86_64","cpu":{"model":"","cores":4},"uptime_seconds":100000,"boot_time":"2026-01-01T00:00:00Z","timezone":"UTC","interfaces":[{"name":"lo","mac":"","up":true,"addresses":["127.0.0.1/8","::1/128"]},{"name":"eth0","mac":"aa:bb:cc:dd:ee:ff","up":true,"addresses":["10.10.0.15/24","fe80::1/64"]}]}}`))
		case "sites.inspect":
			if sa.sitesInspectJSON != "" {
				_, _ = w.Write([]byte(`{"success":true,"data":` + sa.sitesInspectJSON + `}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"domain":"bob.example","config_exists":true,"enabled":true,"pool_versions":["8.0.30"],"root_exists":true,"disk_bytes":129184560,"disk_complete":true,"config_preview":"server { listen 80; server_name bob.example; }"}}`))
		case "workers.status":
			_, _ = w.Write([]byte(`{"success":true,"data":[{"unit":"whacp-worker-bob-app.service","active_state":"active","sub_state":"running","procs_total":2,"procs_running":2,"memory_bytes":12582912}]}`))
		case "db.dump":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"deadbeefcafe0001","file":"/backups/databases/a_main-20260101T000000Z.sql.gz"}}`))
		case "db.import_site":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"deadbeefcafe0002"}}`))
		case "apps.install":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"feedfacefeed1a01"}}`))
		case "db.user_create":
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		case "db.grant":
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		case "db.user_drop":
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		case "jobs.log":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job":{"id":"deadbeefcafe0003","kind":"Dump a_main","target":"100","status":"succeeded","created_at":"2026-01-01T00:00:00Z"},"log":"Dump finished.\n","offset":42,"done":true}}`))
		case "db.overview":
			sa.mu.Lock()
			overview := sa.dbOverviewJSON
			sa.mu.Unlock()
			if overview == "" {
				overview = `{"version":"10.11.6-MariaDB-0ubuntu0.22.04.1","databases":[{"name":"a_main","size_bytes":10485760,"tables":12},{"name":"b_main","size_bytes":524288,"tables":3},{"name":"legacy_shop","size_bytes":2097152,"tables":5}]}`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + overview + `}`))
		case "db.browse_tables":
			_, _ = w.Write([]byte(`{"success":true,"data":{"database":"a_main","tables":[{"name":"posts","view":false,"engine":"InnoDB","rows":120,"bytes":1048576,"collation":"utf8mb4_unicode_ci","comment":""}],"server":"10.11.6-MariaDB","charset":"utf8mb4","collation":"utf8mb4_unicode_ci"}}`))
		case "db.browse_structure":
			_, _ = w.Write([]byte(`{"success":true,"data":{"table":"posts","columns":[{"name":"id","type":"int(11)"}],"indexes":[],"foreign_keys":[],"key":"id","create":"CREATE TABLE posts (id int)"}}`))
		case "db.browse_rows":
			_, _ = w.Write([]byte(`{"success":true,"data":{"table":"posts","rows":[{"key":"1","values":{"id":"1","title":"First"}}],"count":120}}`))
		case "db.query":
			_, _ = w.Write([]byte(`{"success":true,"data":{"results":[],"rows":0,"ms":2}}`))
		case "db.browse_save":
			_, _ = w.Write([]byte(`{"success":true,"data":{"saved":1}}`))
		case "db.browse_delete":
			_, _ = w.Write([]byte(`{"success":true,"data":{"deleted":1}}`))
		case "db.browse_alter":
			_, _ = w.Write([]byte(`{"success":true,"data":{"sql":"ALTER TABLE posts ADD COLUMN note varchar(255);"}}`))
		case "db.server_status":
			if sa.dbDown {
				_, _ = w.Write([]byte(`{"success":true,"data":{"running":false,"unit":"mariadb.service","drop_in":"/etc/mysql/mariadb.conf.d/zz-whacp.cnf","managed":{},"error":"Connection refused (111)"},"settings":[{"key":"max_connections","label":"Maximum connections","help":"Connections the server accepts at once.","kind":"int","dynamic":true,"group":"Connections"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"running":true,"unit":"mariadb.service","drop_in":"/etc/mysql/mariadb.conf.d/zz-whacp.cnf","managed":{"max_connections":"200"},"config_error":"","variables":{"version":"10.11.6-MariaDB","version_comment":"mariadb.org binary distribution","socket":"/run/mysqld/mysqld.sock","port":"3306","bind_address":"127.0.0.1","datadir":"/var/lib/mysql","log_error":"/var/log/mysql/error.log","have_ssl":"YES","max_connections":"151","innodb_buffer_pool_size":"134217728","table_open_cache":"4000","character_set_server":"utf8mb4"},"status":{"Uptime":"86400","Threads_connected":"12","Max_used_connections":"40","Threads_running":"2","Questions":"1000000","Slow_queries":"3","Aborted_connects":"1","Bytes_received":"1048576","Bytes_sent":"5242880","Open_tables":"100","Com_select":"900000","Com_insert":"90000","Com_update":"9000","Com_delete":"1000","Table_locks_waited":"5","Innodb_buffer_pool_pages_total":"8192","Innodb_buffer_pool_pages_free":"4096","Innodb_buffer_pool_read_requests":"10000","Innodb_buffer_pool_reads":"10","Created_tmp_tables":"50","Created_tmp_disk_tables":"2"},"databases":7,"users":11,"bytes":1073741824,"collations":{"utf8mb4":["utf8mb4_general_ci","utf8mb4_unicode_ci"],"utf8mb3":["utf8mb3_general_ci"]},"settings":[{"key":"max_connections","label":"Maximum connections","help":"Connections the server accepts at once (one more is kept for administrators).","kind":"int","dynamic":true,"group":"Connections"},{"key":"innodb_buffer_pool_size","label":"InnoDB buffer pool","help":"Memory for cached InnoDB data and indexes, e.g. 1G.","kind":"size","dynamic":true,"group":"Memory and storage"},{"key":"skip_name_resolve","label":"Skip host name lookups","help":"Match remote users by IP address only (faster, no DNS).","kind":"bool","dynamic":false,"group":"Connections"},{"key":"character_set_server","label":"Default character set","help":"For new databases created without one.","kind":"choice","choices":["utf8mb4","utf8mb3","latin1"],"dynamic":true,"group":"Character set"},{"key":"collation_server","label":"Default collation","help":"Must belong to the character set.","kind":"choice","dynamic":true,"group":"Character set"}]}}`))
		case "db.processlist":
			_, _ = w.Write([]byte(`{"success":true,"data":{"connections":[{"id":7,"user":"u_bob","host":"localhost","db":"a_main","command":"Query","time":3,"state":"Sending data","info":"SELECT * FROM posts","system":false},{"id":3,"user":"event_scheduler","host":"localhost","db":"","command":"Daemon","time":86400,"state":"","info":"","system":true}]}}`))
		case "db.server_settings":
			_, _ = w.Write([]byte(`{"success":true,"data":{"changed":["max_connections"],"applied":["max_connections"],"restart_needed":[],"restarted":false}}`))
		case "db.server_restart":
			_, _ = w.Write([]byte(`{"success":true,"data":{"restarted":true,"seconds":2}}`))
		case "db.server_flush":
			_, _ = w.Write([]byte(`{"success":true,"data":{"flushed":"privileges"}}`))
		case "db.kill":
			_, _ = w.Write([]byte(`{"success":true,"data":{"killed":7,"query_only":false}}`))
		case "logs.sources":
			_, _ = w.Write([]byte(`{"success":true,"data":[{"id":"nginx:access","label":"nginx access","group":"Web server","kind":"file","size":1024,"exists":true},{"id":"system:mysql","label":"MySQL error log","group":"Database server","kind":"file","size":4096,"exists":true},{"id":"journal:mariadb.service","label":"mariadb.service journal","group":"Database server","kind":"journal","size":0,"exists":true},{"id":"system:auth","label":"Authentication log","group":"Security","kind":"file","size":512,"exists":true}]}`))
		case "files.list":
			_, _ = w.Write([]byte(`{"success":true,"data":{"path":"","entries":[{"name":"hello.txt","type":"file","size":11,"mtime":1700000000,"mode":"0644"},{"name":"app","type":"dir","size":0,"mtime":1700000000,"mode":"0755"}]}}`))
		case "files.read":
			_, _ = w.Write([]byte(`{"success":true,"data":{"path":"hello.txt","content":"hello world","mtime":1700000000}}`))
		case "files.write":
			_, _ = w.Write([]byte(`{"success":true,"data":{"path":"hello.txt","mtime":1700000100}}`))
		case "files.rename":
			_, _ = w.Write([]byte(`{"success":true,"data":{"from":"a.txt","to":"b.txt"}}`))
		case "files.extract":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"0123456789abcdef","path":"archive.zip"}}`))
		case "files.upload":
			data, _ := p["data"].(string)
			size := 0
			if b, err := base64.StdEncoding.DecodeString(data); err == nil {
				size = len(b)
			}
			finalStr := "false"
			if p["final"] == true {
				finalStr = "true"
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"path":"up/file.txt","offset":0,"final":` + finalStr + `,"size":` + strconv.Itoa(size) + `}}`))
		case "files.download":
			_, _ = w.Write([]byte(`{"success":true,"data":{"data":"aGVsbG8gd29ybGQ=","size":11,"eof":true}}`))
		case "sites.disk_usage":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"feedfacefeedface"}}`))
		case "jobs.get":
			sa.mu.Lock()
			reply, ok := sa.jobs[p["id"].(string)]
			sa.mu.Unlock()
			if !ok {
				_, _ = w.Write([]byte(`{"success":false,"error":{"code":"not_found","message":"Unknown job.","request_id":"t"}}`))
				return
			}
			_, _ = w.Write([]byte(reply))
		case "terminal.status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"enabled":true,"root_allowed":true,"simulation":false,` +
				`"sessions":[{"id":"abcdef0123456789","user":"u_bob","mode":"jailed","label":"bob (#2)","client_ip":"127.0.0.1",` +
				`"started":"2026-01-01T10:00:00Z","last_input":"2026-01-01T10:05:00Z","bytes_in":1024,"bytes_out":2048,"idle_minutes":15,"max_minutes":480}],` +
				`"max_sessions":20,"max_per_account":3}}`))
		case "shell.status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"accounts":[` +
				`{"user":"u_bob","folder":"bob.example","mode":"jailed","effective":"jailed","ssh_login":true,"keys":1,"password_login":true,"login_shell":"/bin/bash"},` +
				`{"user":"u_alice","folder":"alice.example","mode":"disabled","effective":"disabled","ssh_login":true,"keys":0,"password_login":true,"login_shell":"/bin/bash"},` +
				`{"user":"u_frank","folder":"frank.example","mode":"disabled","effective":"disabled","ssh_login":false,"keys":0,"password_login":false,"login_shell":"/usr/sbin/nologin"}],` +
				`"jail_problem":"","userns_restricted":false}}`))
		case "terminal.open":
			mode, user, pw := "jailed", "u_bob", "true"
			if p["root"] == true {
				mode, user, pw = "root", "root", "false"
			} else if u, _ := p["user"].(string); u != "" {
				user = u
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"ticket":"feedfacefeedfacefeedfacefeedface","expires_in":60,"session_id":"abcdef0123456789","mode":"` + mode + `","user":"` + user + `","password_required":` + pw + `}}`))
		case "terminal.kill":
			sa.mu.Lock()
			closed := sa.termKillClosed
			sa.mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true,"data":{"closed":` + strconv.Itoa(closed) + `}}`))
		case "av.status":
			sa.mu.Lock()
			installed, version, freshclam, updaterLog := sa.avInstalled, sa.avVersion, sa.avFreshclam, sa.avUpdaterLog
			sa.mu.Unlock()
			if version == "" {
				version = "ClamAV 1.4.3/27781/2026-09-27"
			}
			tail := ""
			if updaterLog != "" {
				tail = `,"updater_log":` + fmt.Sprintf("%q", updaterLog)
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"installed":` + boolJSON(installed) + `,"version":` + fmt.Sprintf("%q", version) + `,"freshclam":` + boolJSON(freshclam) + tail + `}}`))
		case "av.update":
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		case "av.scan":
			sa.mu.Lock()
			jobID := sa.avScanJobID
			sa.mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"` + jobID + `"}}`))
		case "av.quarantine_list":
			sa.mu.Lock()
			items := sa.avQuarantineItemsJSON
			sa.mu.Unlock()
			if items == "" {
				items = "[]"
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":` + items + `}}`))
		case "av.release":
			// The agent restores the file where it came from (the batch
			// stamp is the first path segment).
			restored := strOf(p["path"])
			if i := strings.Index(restored, "/"); i >= 0 && i < len(restored)-1 {
				restored = restored[i+1:]
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"released":true,"path":` + fmt.Sprintf("%q", restored) + `}}`))
		case "stats.status":
			sa.mu.Lock()
			installed, enabled, months, updatedAt := sa.statsInstalled, sa.statsEnabled, sa.statsMonthsJSON, sa.statsUpdatedAt
			sa.mu.Unlock()
			if months == "" {
				months = "[]"
			}
			updated := "null"
			if updatedAt != "" {
				updated = fmt.Sprintf("%q", updatedAt)
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"installed":` + boolJSON(installed) + `,"enabled":` + boolJSON(enabled) + `,"months":` + months + `,"updated_at":` + updated + `}}`))
		case "stats.enable":
			on := false
			if b, ok := p["enabled"].(bool); ok {
				on = b
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"enabled":` + boolJSON(on) + `}}`))
		case "stats.update":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"fedcba9876543210"}}`))
		case "stats.data":
			sa.mu.Lock()
			found, content := sa.statsDataFound, sa.statsDataContent
			sa.mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true,"data":{"found":` + boolJSON(found) + `,"content":` + fmt.Sprintf("%q", content) + `}}`))
		case "memcached.status":
			sa.mu.Lock()
			installed, active, port, merr, mstats := sa.mcInstalled, sa.mcActive, sa.mcPort, sa.mcError, sa.mcStats
			sa.mu.Unlock()
			if port == 0 {
				port = 11211
			}
			errField := ""
			if merr != "" {
				errField = `,"error":` + fmt.Sprintf("%q", merr)
			}
			statsField := ""
			if installed && active && merr == "" {
				if mstats != "" {
					statsField = `,"stats":` + mstats
				} else {
					statsField = `,"stats":{"version":"1.6.31","uptime":"86900","curr_items":"1200","evictions":"3","get_hits":"9000","get_misses":"500","limit_maxbytes":"67108864","bytes":"33554432","curr_connections":"4"},"runtime":{"maxconns":"1024"}`
				}
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"installed":` + boolJSON(installed) + `,"config_file":"/etc/memcached.conf","settings":{"memory_mb":64,"max_conns":1024,"port":` + fmt.Sprintf("%d", port) + `,"listen":["127.0.0.1"]},"active":` + boolJSON(active) + errField + statsField + `}}`))
		case "memcached.configure":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "memcached.flush":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "services.control":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "packages.install":
			sa.mu.Lock()
			jobID := sa.installJobID
			sa.mu.Unlock()
			if jobID == "" {
				jobID = "abcdef0123456789"
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"` + jobID + `"}}`))
		case "ftp.status":
			sa.mu.Lock()
			mode := sa.ftpStatus
			sa.mu.Unlock()
			switch mode {
			case "missing":
				_, _ = w.Write([]byte(`{"success":true,"data":{"installed":false,"configured":false,"active":false,"sftp_root":"/home/whacp","settings":{},"certificate":null,"other_ftp_servers":[],"accounts":[],"sftp":[]}}`))
			case "stopped":
				_, _ = w.Write([]byte(`{"success":true,"data":{"installed":true,"configured":true,"active":false,"sftp_root":"/home/whacp","settings":{"passive_min":40110,"passive_max":40210,"passive_ip":"","max_clients":50,"max_per_ip":8,"tls":"required"},"certificate":{"subject":"CN=whacp-ftp","not_after":"2027-10-23T00:00:00Z","sha256":"aabbccddeeff"},"other_ftp_servers":[],"accounts":[{"name":"designer@bob.example","user":"u_bob","dir":"/home/whacp/bob.example/public_html","quota_mb":100}],"sftp":[]}}`))
			default:
				_, _ = w.Write([]byte(`{"success":true,"data":{"installed":true,"configured":true,"active":true,"sftp_root":"/home/whacp","settings":{"passive_min":40110,"passive_max":40210,"passive_ip":"","max_clients":50,"max_per_ip":8,"tls":"required"},"certificate":{"subject":"CN=whacp-ftp","not_after":"2027-10-23T00:00:00Z","sha256":"aabbccddeeff"},"other_ftp_servers":[],"accounts":[{"name":"designer@bob.example","user":"u_bob","dir":"/home/whacp/bob.example/public_html","quota_mb":100}],"sftp":[{"user":"u_bob","shell":"disabled","password_login":false,"keys":[{"key":"ssh-ed25519 AAAACTEST bob@laptop","fingerprint":"SHA256:ftptest"}]}]}}`))
			}
		case "ftp.configure":
			_, _ = w.Write([]byte(`{"success":true,"data":{"notes":[],"stopped":[]}}`))
		case "ftp.disable":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "ftp.account_save":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "ftp.account_delete":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "sftp.configure":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "redis.info":
			sa.mu.Lock()
			infoJSON := sa.redisInfoJSON
			sa.mu.Unlock()
			if infoJSON == "" {
				infoJSON = `{"info":{"server":{"redis_version":"7.2.4","uptime_in_seconds":"90000"},"memory":{"used_memory_human":"5.20M"},"clients":{"connected_clients":"4"},"keyspace":{"0":"42 keys","1":"7 keys"},"stats":{"keyspace_hits":"1000","keyspace_misses":"50"}},"maxmemory":"1073741824","maxmemory_policy":"allkeys-lru","acl_users":["default","u_bob","u_carol"]}`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + infoJSON + `}`))
		case "redis.configure":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "redis.password":
			pw, _ := p["password"].(string)
			_, _ = w.Write([]byte(`{"success":true,"data":{"password_set":` + boolJSON(pw != "") + `}}`))
		case "redis.flush":
			db, _ := p["db"].(float64)
			_, _ = w.Write([]byte(`{"success":true,"data":{"flushed":` + fmt.Sprintf("%d", int(db)) + `}}`))
		case "redis.acl_set":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "redis.acl_del":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "redis.keys":
			prefix, _ := p["prefix"].(string)
			keys := `[]`
			if prefix == "u_bob:" || prefix == "" {
				keys = `[{"key":{"b64":"` + base64.StdEncoding.EncodeToString([]byte(prefix + "session:1")) + `","text":"` + prefix + `session:1","binary":false},"type":"string","ttl":300},{"key":{"b64":"` + base64.StdEncoding.EncodeToString([]byte(prefix + "cart:1")) + `","text":"` + prefix + `cart:1","binary":false},"type":"hash","ttl":-1}]`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"keys":` + keys + `,"cursor":"0","db_size":2}}`))
		case "redis.key_get":
			key, _ := p["key"].(string)
			_, _ = w.Write([]byte(`{"success":true,"data":{"key":{"b64":"` + key + `","text":"k","binary":false},"type":"string","value":{"b64":"` + key + `","text":"v","binary":false},"ttl":300}}`))
		case "redis.key_edit":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "redis.key_delete":
			keys := 1
			if ks, ok := p["keys"].([]any); ok {
				keys = len(ks)
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"deleted":` + fmt.Sprintf("%d", keys) + `}}`))
		case "security.pending":
			sa.mu.Lock()
			pending := sa.secPending
			sa.mu.Unlock()
			if pending == "" {
				pending = "null"
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"pending":` + pending + `}}`))
		case "security.confirm":
			_, _ = w.Write([]byte(`{"success":true,"data":{"confirmed":"` + strOf(p["id"]) + `"}}`))
		case "security.undo":
			_, _ = w.Write([]byte(`{"success":true,"data":{"undone":"` + strOf(p["id"]) + `"}}`))
		case "shell.set":
			sa.mu.Lock()
			eff := sa.shellSetEffective
			mode, _ := p["mode"].(string)
			sa.mu.Unlock()
			if eff == "" {
				eff = mode
			}
			out := ""
			if users, ok := p["users"].([]any); ok {
				for i, u := range users {
					if i > 0 {
						out += ","
					}
					out += `{"user":"` + strOf(u) + `","mode":"` + mode + `","effective":"` + eff + `"}`
				}
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"accounts":[` + out + `]}}`))
		case "firewall.status":
			sa.mu.Lock()
			active := sa.fwActive
			sa.mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true,"data":{"backend":"ufw","installed":true,"active":` + boolJSON(active) +
				`,"default_incoming":"deny","default_outgoing":"allow","ssh_port":22,"ssh_allowed":true,` +
				`"rules":[{"spec":"allow 22/tcp","action":"allow","ssh":true,"port":"22","proto":"tcp","from":"","direction":"in","comment":"SSH"},` +
				`{"spec":"deny from 203.0.113.99","action":"deny","ssh":false,"port":"","proto":"","from":"203.0.113.99","direction":"in","comment":"Bad guy"}],` +
				`"required":[{"port":"22","proto":"tcp","from":"","service":"SSH","critical":true,"open":true},` +
				`{"port":"8443","proto":"tcp","from":"","service":"Panel (HTTPS)","critical":true,"open":false}],` +
				`"listening":[{"proto":"tcp","port":22,"local":false},{"proto":"tcp","port":80,"local":false},{"proto":"tcp","port":3306,"local":true}]}}`))
		case "firewall.protection":
			_, _ = w.Write([]byte(`{"success":true,"data":{"available":true,"active":false,` +
				`"config":{"enabled":false,"invalid":false,"syn_rate":0,"icmp_rate":0,"conn_limits":[],"port_flood":[],"exempt":[]},"dropped":{}}}`))
		case "firewall.set_enabled":
			sa.mu.Lock()
			changed := sa.guardChanged
			sa.mu.Unlock()
			enabled := p["enabled"] == true
			summary := "Firewall disabled"
			if enabled {
				summary = "Firewall enabled"
			}
			_, _ = w.Write(guardedReply(changed, "fwpend-fw", "firewall", summary))
		case "firewall.rule_add":
			sa.mu.Lock()
			changed := sa.guardChanged
			sa.mu.Unlock()
			_, _ = w.Write(guardedReply(changed, "fwpend-rule", "firewall", "Firewall rule added"))
		case "firewall.rule_delete":
			sa.mu.Lock()
			changed := sa.guardChanged
			sa.mu.Unlock()
			_, _ = w.Write(guardedReply(changed, "fwpend-del", "firewall", "Firewall rule removed"))
		case "firewall.protection_set":
			sa.mu.Lock()
			changed := sa.guardChanged
			sa.mu.Unlock()
			_, _ = w.Write(guardedReply(changed, "fwpend-prot", "firewall", "Firewall protections applied"))
		case "fail2ban.status":
			sa.mu.Lock()
			managed, installed, running := sa.f2bManaged, sa.f2bInstalled, sa.f2bRunning
			sa.mu.Unlock()
			managedJSON := ""
			if managed != "" {
				managedJSON = `"managed_config":` + fmt.Sprintf("%q", managed) + `,`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"installed":` + boolJSON(installed) + `,"running":` + boolJSON(running) + `,` + managedJSON +
				`"jails":[{"name":"sshd","currently_banned":2,"total_banned":15,"currently_failed":3,"banned_ips":["203.0.113.66","198.51.100.9"]},` +
				`{"name":"recidive","currently_banned":0,"total_banned":4,"currently_failed":1,"banned_ips":[]}]}}`))
		case "fail2ban.configure":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "fail2ban.unban":
			_, _ = w.Write([]byte(`{"success":true,"data":{"unbanned":"` + strOf(p["ip"]) + `"}}`))
		case "security.fix_kernel":
			_, _ = w.Write([]byte(`{"success":true,"data":{"changed":["net.ipv4.conf.all.rp_filter"]}}`))
		case "security.hide_processes":
			_, _ = w.Write([]byte(`{"success":true,"data":{"enabled":true}}`))
		case "security.auto_updates":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"ab12cd34ef56ab78"}}`))
		case "security.host_known":
			_, _ = w.Write([]byte(`{"success":true,"data":{"marked":["cron/daily-backup","systemd/whacp-agent.service"]}}`))
		case "db.server_settings":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "ssh.status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"effective":{"port":"22","permitrootlogin":"prohibit-password","passwordauthentication":"no","pubkeyauthentication":"yes","maxauthtries":"4","x11forwarding":"no","kbdinteractiveauthentication":"no"},` +
				`"root_has_keys":true,"admins_with_keys":["root"],"managed_file":"/etc/ssh/sshd_config.d/50-whacp.conf","managed_config":"Port 22"}}`))
		case "ssh.keys":
			_, _ = w.Write([]byte(`{"success":true,"data":{"admins":[` +
				`{"user":"root","home":"/root","keys":[{"type":"ssh-ed25519","comment":"root@whacp","fingerprint":"SHA256:RootKeyFingerprint000000000000000000000"}]},` +
				`{"user":"wheel-admin","home":"/home/wheel-admin","keys":[],"other_lines":2}]}}`))
		case "ssh.configure":
			sa.mu.Lock()
			changed := sa.guardChanged
			sa.mu.Unlock()
			_, _ = w.Write(guardedReply(changed, "sshpend1", "ssh", "SSH settings changed"))
		case "ssh.key_add":
			_, _ = w.Write([]byte(`{"success":true,"data":{"user":"` + strOf(p["user"]) + `","key":{"type":"ssh-ed25519","comment":"you@laptop","fingerprint":"SHA256:AddedKeyFingerprint00000000000000000000000"}}}`))
		case "ssh.key_remove":
			_, _ = w.Write([]byte(`{"success":true,"data":{"removed":"` + strOf(p["fingerprint"]) + `"}}`))
		case "security.audit":
			_, _ = w.Write([]byte(`{"success":true,"data":[` +
				`{"id":"firewall","title":"Firewall","status":"pass","severity":"high","detail":"UFW is active with 3 rules (default incoming: deny)."},` +
				`{"id":"ssh_root","title":"SSH root login","status":"fail","severity":"high","detail":"root can log in with a password.","remedy":"Set root login to keys only or no under Security → SSH."},` +
				`{"id":"ssh_password","title":"SSH password logins","status":"warn","severity":"medium","detail":"Password logins are allowed, which invites brute-force attacks.","remedy":"Add an SSH key for an administrator, test it, then turn off password logins."},` +
				`{"id":"fail2ban","title":"Brute-force protection","status":"warn","severity":"medium","detail":"fail2ban is installed but not running.","remedy":"Start fail2ban under Services."},` +
				`{"id":"auto_updates","title":"Automatic security updates","status":"warn","severity":"medium","detail":"Security updates are not installed automatically.","remedy":"Turn on automatic security updates (unattended-upgrades installs security updates daily).","fix":"apply"},` +
				`{"id":"host_persistence","title":"Host persistence","status":"pass","severity":"medium","detail":"All scheduled tasks and services are known.","fix":"undo"},` +
				`{"id":"kernel_settings","title":"Recommended kernel settings","status":"warn","severity":"medium","detail":"Some kernel security settings are not recommended.","remedy":"Apply the recommended kernel security settings.","fix":"apply"},` +
				`{"id":"mysql_anonymous","title":"MySQL anonymous users","status":"fail","severity":"low","detail":"The database server has anonymous accounts.","remedy":"Remove the anonymous MySQL users."},` +
				`{"id":"updates","title":"Security updates","status":"fail","severity":"high","detail":"2 security update(s) are waiting.","remedy":"Install updates under Server → Packages."},` +
				`{"id":"ssh","title":"SSH server","status":"info","severity":"low","detail":"The SSH configuration could not be read."}]}}`))
		case "ssh.logins":
			_, _ = w.Write([]byte(`{"success":true,"data":{"available":true,"today":[2,17],"yesterday":[1,40],"week":[9,300],` +
				`"last_success":{"time":"2026-10-22T08:12:00Z","user":"admin","ip":"203.0.113.50","port":22,"method":"publickey"},` +
				`"last_failure":{"time":"2026-10-22T09:01:00Z","user":"root","ip":"198.51.100.77"}}}`))
		case "hardening.status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"forkbomb":{"enabled":false},"traceroute":{"enabled":true,"programs":["traceroute","traceroute6"]}}}`))
		case "db.drop_anonymous_users":
			sa.mu.Lock()
			none := sa.anonRemoved == "none"
			sa.mu.Unlock()
			removed := `["@localhost","@"]`
			if none {
				removed = `[]`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"removed":` + removed + `}}`))
		case "hardening.status":
			_, _ = w.Write([]byte(`{"success":true,"data":{` +
				`"compilers":{"restricted":true,"programs":[{"path":"/usr/bin/gcc","owner":"root","mode":"0750"},{"path":"/usr/bin/cc","owner":"root","mode":"0750"}],"group":"whacp-compiler","members":["u1-site"]},` +
				`"smtp":{"enabled":true,"active":true,"available":true,"allowed":["root","postfix","mailman","whacp"],"ports":[25,465,587]},` +
				`"egress":{"available":true,"active":true,"metadata_blocked":true,"block_private":false,"tcp_ports":[53,80,443],"udp_ports":[53],"accounts":3,"metadata":["169.254.169.254","fd00:ec2::254"]},` +
				`"forkbomb":{"enabled":false,"file":"/etc/security/limits.d/70-whacp.conf","soft":200,"hard":300},` +
				`"traceroute":{"enabled":true,"programs":[{"path":"/usr/bin/traceroute","mode":"0755"}]}}}`))
		case "hardening.compilers", "hardening.smtp", "hardening.egress", "hardening.forkbomb", "hardening.traceroute":
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		case "security.integrity":
			_, _ = w.Write([]byte(`{"success":true,"data":{"checked_at":"2026-10-22T00:00:00Z","package_tool":"dpkg","has_baseline":true,"findings":[{"path":"/usr/bin/passwd","class":"package","issue":"modified","severity":"medium","detail":"checksum changed"}]}}`))
		case "security.integrity_baseline":
			_, _ = w.Write([]byte(`{"success":true,"data":{"baselined":7}}`))
		case "tls.audit":
			_, _ = w.Write([]byte(`{"success":true,"data":{"results":[{"endpoint":"a.example.com:443","kind":"website","reachable":true,"best_version":"TLS 1.3","min_version":"TLS 1.0","findings":[{"issue":"TLS 1.0 accepted","severity":"high","detail":"an obsolete protocol is still accepted"}]},{"endpoint":"b.example.com:443","kind":"website","reachable":true,"best_version":"TLS 1.3"}]},"high":1,"medium":0}`))
		case "packages.advisories":
			_, _ = w.Write([]byte(`{"success":true,"data":{"advisories":[{"package":"openssl","installed":"3.0.2","fixed":"3.0.13","advisory":"USN-1234-1","severity":"important","cves":["CVE-2026-0001"],"kind":"security"},{"package":"vim","installed":"9.0","fixed":"9.1","kind":"normal"}],"count":2,"security":1,"normal":1,"unknown":0}}`))
		case "mail.status":
			sa.mu.Lock()
			data := sa.mailData
			sa.mu.Unlock()
			if data == "" {
				data = `{"installed":true,"configured":true,"hostname":"mail.example.com"}`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + data + `}`))
		case "mail.domain_add":
			_, _ = w.Write([]byte(`{"success":true,"data":{"dkim_selector":"whacp202601","dkim_txt":"v=DKIM1; k=rsa; p=MEow"}}`))
		case "mail.spam_configure":
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		case "mail.mailbox_save":
			_, _ = w.Write([]byte(`{"success":true,"data":{"address":"info@bob.example","created":true,"quota_mb":1024}}`))
		case "mail.mailbox_delete":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "mail.alias_save":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "mail.alias_delete":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "mail.routing_set":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "mail.domain_remove":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "mail.auth_check":
			_, _ = w.Write([]byte(`{"success":true,"data":{"checks":[{"id":"mx","label":"MX","status":"pass","found":"10 mail.example.com.","detail":"Mail goes to this server."},{"id":"spf","label":"SPF","status":"pass","found":"v=spf1 a mx ~all","detail":"This server may send for the domain."}],"simulated":false}}`))
		case "mail.filters_get":
			sa.mu.Lock()
			data := sa.mailFilters
			sa.mu.Unlock()
			if data == "" {
				data = `{"filters":[{"name":"To spam","enabled":true,"any":false,"rules":[{"part":"subject","match":"contains","value":"invoice"}],"actions":[{"type":"fileinto","value":"spam"}]}],"responder":{"enabled":false,"subject":"","body":"","days":1}}`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + data + `}`))
		case "mail.filters_save":
			sa.mu.Lock()
			filters, _ := json.Marshal(p["filters"])
			responder, _ := json.Marshal(p["responder"])
			sa.mailFilters = `{"filters":` + string(filters) + `,"responder":` + string(responder) + `}`
			sa.mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "mail.queue":
			sa.mu.Lock()
			data := sa.mailQueue
			sa.mu.Unlock()
			if data == "" {
				now := strconv.FormatInt(time.Now().Unix(), 10)
				data = `{"messages":[{"queue":"deferred","id":"A1B2C3D4E5F607","arrival":` + now + `,"size":20480,"sender":"bounce+job@example.com","recipients":["a@bob.example","b@bob.example"],"reason":"Connection timed out"},{"queue":"hold","id":"F0E1D2C3B4A506","arrival":` + now + `,"size":1024,"sender":"","recipients":["info@bob.example"]}],"total":2,"counts":{"deferred":1,"hold":1}}`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + data + `}`))
		case "mail.queue_message":
			id, _ := p["id"].(string)
			if !queueIDRe.MatchString(id) {
				_, _ = w.Write([]byte(`{"success":false,"error":{"code":"invalid_params","message":"id is not a queue ID","request_id":"t"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":` + fmt.Sprintf("%q", id) + `,"headers":{"from":"<bounce+job@example.com>","to":"a@bob.example","subject":"Job failed","date":"Thu, 23 Oct 2026 10:00:00 +0000","message-id":"<job-1@example.com>"}}`))
		case "mail.queue_action":
			_, _ = w.Write([]byte(`{"success":true,"data":{"action":"done","count":1}}`))
		case "mail.report":
			_, _ = w.Write([]byte(`{"success":true,"data":{"source":"journal","report":{"totals":{"received":12,"sent":9,"delivered":5,"deferred":2,"bounced":1,"rejected":0,"auth_ok":3,"auth_failed":1},"series":[{"start":"2026-10-22T12:00:00Z","received":4,"sent":3,"bounced":1,"rejected":0},{"start":"2026-10-22T13:00:00Z","received":8,"sent":6,"bounced":0,"rejected":0}],"deliveries":[{"time":"2026-10-22T13:05:00Z","id":"A1B2C3D4E5F607","from":"job@example.com","to":"a@bob.example","relay":"mail.example.com[10.0.0.2]:25","status":"sent","dsn":"2.0.0","delay":"0.4","reason":"sent (OK)"},{"time":"2026-10-22T13:06:00Z","id":"F0E1D2C3B4A506","from":"","to":"x@bob.example","relay":"delayed","status":"deferred","dsn":"4.4.1","delay":"3600.1","reason":"Connection timed out"}],"relayers":[{"kind":"local","name":"www-data","messages":7},{"kind":"sasl","name":"info@bob.example","messages":2}],"senders":[{"kind":"","name":"bob.example","messages":9}],"truncated":false}}}`))
		case "mail.server_status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"services":[{"unit":"postfix.service","name":"Postfix","active":true,"enabled":true,"version":"Postfix 3.8.0","installed":true},{"unit":"dovecot.service","name":"Dovecot","active":true,"enabled":true,"version":"2.4.1","installed":true},{"unit":"whacp-opendkim.service","name":"OpenDKIM","active":true,"enabled":true,"version":"","installed":true},{"unit":"rspamd.service","name":"Rspamd","active":false,"enabled":false,"version":"","installed":false}],"checks":{"postfix":"","dovecot":""},"values":{"postfix":{"message_size_limit":"10485760","smtpd_recipient_limit":"200","maximal_queue_lifetime":"5d","bounce_queue_lifetime":"5d","smtpd_client_connection_rate_limit":"0","smtpd_client_message_rate_limit":"0","smtpd_helo_required":"yes","disable_vrfy_command":"yes","smtp_tls_security_level":"may","inet_protocols":"all","relayhost":"","authorized_submit_users":"!nobody, static:anyone"},"dovecot":{"mail_max_userip_connections":"10","auth_failure_delay":"5s","ssl_min_protocol":"TLSv1.2","login_greeting":""}},"labels":{"postfix":{"message_size_limit":"Largest message (MB)","smtpd_recipient_limit":"Recipients per message","maximal_queue_lifetime":"Keep retrying undelivered mail (days)","bounce_queue_lifetime":"Keep retrying bounces (days)","smtpd_client_connection_rate_limit":"Connections per client per minute (0 = no limit)","smtpd_client_message_rate_limit":"Messages per client per minute (0 = no limit)","smtpd_helo_required":"Require HELO","disable_vrfy_command":"Disable VRFY","smtp_tls_security_level":"TLS for outgoing mail","inet_protocols":"IP versions","relayhost":"Send outgoing mail through (smart host)","authorized_submit_users":"Local accounts that may send mail with sendmail"},"dovecot":{"mail_max_userip_connections":"IMAP connections per user and address","auth_failure_delay":"Delay after a failed login (seconds)","ssl_min_protocol":"Oldest TLS version","login_greeting":"Login greeting"}},"configured":true}}`))
		case "mail.log_tail":
			_, _ = w.Write([]byte(`{"success":true,"data":{"lines":[{"time":"2026-10-23T10:00:01Z","ident":"postfix/smtpd","message":"connect from mail.example.com[10.0.0.2]"},{"time":"2026-10-23T10:00:02Z","ident":"dovecot/auth","message":"Login: user=<info@bob.example>"}],"source":"journal"}}`))
		case "mail.settings_set":
			_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
		case "mail.trace":
			_, _ = w.Write([]byte(`{"success":true,"data":{"address":"info@bob.example","steps":[{"depth":0,"address":"info@bob.example","result":"mailbox","detail":"A mailbox on this server."}]}}`))
		case "mail.repair_permissions":
			_, _ = w.Write([]byte(`{"success":true,"data":{"dirs":4,"files":9,"fixed":2,"skipped":["Maildir/info/cur"]}}`))
		case "hulk.status":
			sa.mu.Lock()
			available, active := sa.hulkAvailable, sa.hulkActive
			sa.mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true,"data":{"available":` + boolJSON(available) + `,"active":` + boolJSON(active) + `}}`))
		case "hulk.sync":
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		case "modsec.status":
			sa.mu.Lock()
			active := sa.modsecActive
			sa.mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true,"data":{
				"module_loaded":true,
				"connector":{"state":"functional","reason":"","package":"libnginx-mod-http-modsecurity","nginx_version":"1.24.0","modules":[{"name":"ngx_http_modsecurity_module","path":"/usr/lib/nginx/modules/ngx_http_modsecurity_module.so"}]},
				"crs":{"available":true,"version":"1.1"},
				"config":{"enabled":true,"crs":{"enabled":true,"paranoia":1,"inbound_threshold":5,"outbound_threshold":4},"custom":{"enabled":false},"detection_only":false},
				"sites":{"bob.example":{"mode":"on","exclude":["custom"],"disabled_rules":[942100]}},
				"active":`+boolJSON(active)+`,
				"vendors":[{"id":"crs","name":"OWASP Core Rule Set","description":"Generic protection against SQL injection, XSS, file inclusion, remote code execution and protocol abuse.","first_id":900000,"last_id":999999},{"id":"custom","name":"Custom rules","description":"Rules the server administrators write here (IDs 10000–99999).","first_id":10000,"last_id":99999}],
				"custom_rules":2,
				"audit_log_bytes":123456,
				"packages":["libnginx-mod-http-modsecurity","modsecurity-crs"],
				"packages_available":true
			}}`))
		case "modsec.custom_get":
			_, _ = w.Write([]byte(`{"success":true,"data":{"rules":"# block xmlrpc\nSecRule REQUEST_URI \"@beginsWith /xmlrpc.php\" \"id:10001,phase:1,deny,status:403,log,msg:'xmlrpc blocked'\"\n\nSecAction \"id:10002,phase:1,nolog,pass,secmark='EXECCMD'\""}}`))
		case "modsec.custom_set":
			_, _ = w.Write([]byte(`{"success":true,"data":{"rules":2}}`))
		case "modsec.site":
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		case "modsec.events":
			_, _ = w.Write([]byte(`{"success":true,"data":{"events":[
				{"time":"2026-10-22T10:00:00Z","host":"bob.example","client_ip":"203.0.113.9","method":"GET","uri":"/index.php?id=1%27%20UNION%20SELECT%201,2,3--","status":403,"engine":"ModSecurity: v0.102 (linux)","rules":[{"id":"942100","message":"SQL Injection Attack","severity":"CRITICAL","data":""},{"id":"949110","message":"Anomaly Inbound Score Threshold Exceeded","severity":"NOTICE","data":""}]},
				{"time":"2026-10-22T09:30:00Z","host":"unknown.example","client_ip":"198.51.100.7","method":"GET","uri":"/?q=%3Cscript%3Ealert(1)%3C/script%3E","status":200,"engine":"DetectionOnly","rules":[{"id":"941100","message":"XSS Attack","severity":"CRITICAL","data":""}]}
			]}}`))
		case "modsec.installs":
			_, _ = w.Write([]byte(`{"success":true,"data":{"installs":[
				{"id":"2026-10-01T08:00:00Z-a1b2c3d4","started_at":"2026-10-01T08:00:00Z","status":"committed","method":"package","nginx_version":"1.24.0","os":"Debian 12","module":"/usr/lib/nginx/modules/ngx_http_modsecurity_module.so","folder":"/var/lib/whacp/modsec-backups/2026-10-01T08:00:00Z","sources":[{"name":"libnginx-mod-http-modsecurity","version":"3.0.10-1","origin":"distribution"}],"can_roll_back":true,"error":""}
			]}}`))
		case "modsec.update":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"1111111111111111"}}`))
		case "modsec.install":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"2222222222222222"}}`))
		case "modsec.crs_install":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"3333333333333333"}}`))
		case "modsec.rollback":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"4444444444444444"}}`))
		case "ssl.inspect":
			if sa.sslInspectJSON != "" {
				_, _ = w.Write([]byte(`{"success":true,"data":` + sa.sslInspectJSON + `}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"file":"/etc/letsencrypt/live/bob.example/fullchain.pem","key_exists":true,"subject":"CN=bob.example","common_name":"bob.example","dns_names":["bob.example","www.bob.example"],"issuer":"R3, Let's Encrypt","not_before":"2026-07-01T00:00:00Z","not_after":"2026-12-30T00:00:00Z","days_remaining":129,"expired":false,"self_signed":false,"lets_encrypt":true,"fingerprint_sha256":"ab:cd:ef","chain_length":2}}`))
		case "ssl.renewal_status":
			if sa.sslRenewalJSON != "" {
				_, _ = w.Write([]byte(`{"success":true,"data":` + sa.sslRenewalJSON + `}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"certbot_installed":true,"timer_active":true,"method":"certbot.timer","deploy_hook":true,"can_enable":true}}`))
		case "ssl.issue", "ssl.custom", "ssl.renew":
			_, _ = w.Write([]byte(`{"success":true,"data":{"cert_file":"/etc/letsencrypt/live/bob.example/fullchain.pem","key_file":"/etc/letsencrypt/live/bob.example/privkey.pem","certificate":{"common_name":"bob.example"}}}`))
		case "ssl.delete":
			_, _ = w.Write([]byte(`{"success":true,"data":{"deleted":"bob.example"}}`))
		case "cron.apply":
			_, _ = w.Write([]byte(`{"success":true,"data":{"file":"/etc/cron.d/whacp-site-bob.example","changed":true}}`))
		case "cron.run":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"0123456789c0a001"}}`))
		case "deploy.key":
			key := sa.deployKey
			if key == "" {
				key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDeployKeyForTests whacp-test"
			}
			b, _ := json.Marshal(map[string]any{"public_key": key})
			_, _ = w.Write([]byte(`{"success":true,"data":` + string(b) + `}`))
		case "deploy.run":
			jobID := sa.deployRunJob
			if jobID == "" {
				jobID = "0123456789d0a001"
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"` + jobID + `"}}`))
		case "mongo.status":
			switch sa.mongoStatus {
			case "down":
				_, _ = w.Write([]byte(`{"success":true,"data":{"installed":true,"running":false,"version":"","auth":false}}`))
			case "missing":
				_, _ = w.Write([]byte(`{"success":true,"data":{"installed":false,"running":false,"version":"","auth":false}}`))
			default:
				_, _ = w.Write([]byte(`{"success":true,"data":{"installed":true,"running":true,"version":"7.0.14","auth":false}}`))
			}
		case "mongo.overview":
			ov := sa.mongoOverview
			if ov == "" {
				ov = `{"databases":[{"name":"u_bob_shop","size":123456,"empty":false},{"name":"u_bob_logs","size":0,"empty":true},{"name":"other_app","size":999,"empty":false}],"users":[{"user":"u_bob_admin","db":"u_bob_shop","roles":["dbOwner"]},{"user":"u_bob_ro","db":"u_bob_shop","roles":["read"]},{"user":"other_user","db":"other_app","roles":["readWrite"]}]}`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + ov + `}`))
		case "mongo.database_create", "mongo.database_drop", "mongo.user_create", "mongo.user_password", "mongo.user_drop":
			_, _ = w.Write([]byte(`{"success":true,"data":{},"request_id":"t"}`))
		case "mongo.backup":
			db, _ := p["db"].(string)
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"0123456789m0a001","file":"20261023-100000_` + db + `.archive.gz"}}`))
		case "mongo.backups":
			bs := sa.mongoBackups
			if bs == "" {
				bs = `{"backups":[{"file":"20261020-093000_u_bob_shop.archive.gz","db":"u_bob_shop","size":1048576,"created":"2026-10-20T09:30:00Z"},{"file":"20261019-120000_other_app.archive.gz","db":"other_app","size":555,"created":"2026-10-19T12:00:00Z"}]}`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + bs + `}`))
		case "mongo.restore":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"0123456789m0a002"}}`))
		case "mongo.backup_delete":
			_, _ = w.Write([]byte(`{"success":true,"data":{},"request_id":"t"}`))
		case "mongo.collections":
			_, _ = w.Write([]byte(`{"success":true,"data":{"collections":[{"name":"coll_a","count":2},{"name":"coll_b","count":0}]}}`))
		case "mongo.collection_create", "mongo.collection_drop", "mongo.collection_rename", "mongo.index_drop":
			_, _ = w.Write([]byte(`{"success":true,"data":{},"request_id":"t"}`))
		case "mongo.collection_empty":
			_, _ = w.Write([]byte(`{"success":true,"data":{"deleted":12}}`))
		case "mongo.documents":
			_, _ = w.Write([]byte(`{"success":true,"data":{"documents":[{"_id":"1","name":"a"},{"_id":"2","name":"b"}],"total":2,"skip":0,"limit":25}}`))
		case "mongo.document_delete":
			_, _ = w.Write([]byte(`{"success":true,"data":{"deleted":1}}`))
		case "mongo.document_insert":
			_, _ = w.Write([]byte(`{"success":true,"data":{"inserted_id":"1"}}`))
		case "mongo.document_update":
			_, _ = w.Write([]byte(`{"success":true,"data":{"matched":1,"modified":1}}`))
		case "mongo.aggregate":
			_, _ = w.Write([]byte(`{"success":true,"data":{"documents":[{"_id":"1","n":2}],"ms":1}}`))
		case "mongo.explain":
			_, _ = w.Write([]byte(`{"success":true,"data":{"explain":{"stages":[]}}}`))
		case "mongo.export":
			format := "json"
			if f, ok := p["format"].(string); ok && f != "" {
				format = f
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"content":"[{}]","count":1,"format":"` + format + `"}}`))
		case "mongo.import":
			_, _ = w.Write([]byte(`{"success":true,"data":{"inserted":2,"errors":0,"total":2}}`))
		case "mongo.schema":
			_, _ = w.Write([]byte(`{"success":true,"data":{"sampled":2,"fields":[{"name":"_id","type":"objectId"}]}}`))
		case "mongo.database_users":
			_, _ = w.Write([]byte(`{"success":true,"data":{"users":[{"user":"u_bob_admin","roles":["dbOwner"]}]}}`))
		case "mongo.indexes":
			_, _ = w.Write([]byte(`{"success":true,"data":{"indexes":[{"name":"_id_"}]}}`))
		case "mongo.collection_stats":
			_, _ = w.Write([]byte(`{"success":true,"data":{"count":2,"size":1234}}`))
		case "mongo.index_create":
			_, _ = w.Write([]byte(`{"success":true,"data":{"name":"idx_1"}}`))
		case "wordpress.info":
			res := sa.wpInfoReply
			if res == "" {
				res = defaultWpInfoReply
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + res + `}`))
		case "protect.status":
			res := sa.wpProtectionReply
			if res == "" {
				res = `{"password":[{"path":"/","user":"u_bob"}],"hotlink":{"allowed":["partner.com"]},"wordpress":{}}`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + res + `}`))
		case "wordpress.security":
			res := sa.wpSecurityReply
			if res == "" {
				res = `{"measures":[{"id":"block_sensitive_files","title":"Block direct access to sensitive files","kind":"web","help":"wp-config.php and such stay hidden.","applied":false,"can_rollback":false},
				 {"id":"block_author_scans","title":"Block author scans","kind":"web","help":"?author= no longer reveals user names.","applied":false,"can_rollback":true},
				 {"id":"no_php_in_uploads","title":"No PHP in the uploads folder","kind":"web","help":"PHP files in uploads stop executing.","applied":true,"can_rollback":true},
				 {"id":"no_php_in_includes","title":"No PHP in the includes folder","kind":"web","help":"PHP files in includes stop executing.","applied":false,"can_rollback":false},
				 {"id":"block_bad_bots","title":"Block bad bots","kind":"web","help":"Known attack user agents are refused.","applied":false,"can_rollback":true},
				 {"id":"disable_file_edit","title":"Disable the file editor","kind":"wordpress","help":"WP-CLI sets DISALLOW_FILE_EDIT.","applied":false,"can_rollback":true},
				 {"id":"disable_pingbacks","title":"Disable pingbacks","kind":"wordpress","help":"Pingbacks are turned off in the settings.","applied":false,"can_rollback":true},
				 {"id":"disable_script_concat","title":"Disable script concatenation","kind":"wordpress","help":"CONCATENATE_SCRIPTS is off.","applied":false,"can_rollback":true},
				 {"id":"config_permissions","title":"Tighten wp-config.php permissions","kind":"wordpress","help":"Only the website account can read the file.","applied":false,"can_rollback":true}],
				 "problems":[]}`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + res + `}`))
		case "wordpress.restore_points":
			res := sa.wpRestoreReply
			if res == "" {
				res = `{"restore_points":[{"id":"20260101T010100Z","path":"public_html","reason":"Before the sync","database":true},{"id":"20260102T020200Z","path":"other","reason":"Before the clone","database":false}]}`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + res + `}`))
		case "wordpress.login":
			file := sa.wpLoginFile
			if file == "" {
				file = "whacp-login-a1b2c3d4e5f60718293a4b5c6d7e8f90.php"
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"file":"` + file + `"}}`))
		case "wordpress.copy":
			sa.mu.Lock()
			copyJob := sa.wpCopyJobID
			sa.mu.Unlock()
			if copyJob == "" {
				copyJob = "0123456789c0a001"
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"` + copyJob + `"}}`))
		case "wordpress.manage":
			switch strOf(p["op"]) {
			case "debug_log":
				res := sa.wpDebugLog
				switch res {
				case "missing":
					_, _ = w.Write([]byte(`{"success":true,"data":{"path":"/home/whacp/bob.example/public_html/wp-content/debug.log","exists":false,"lines":[],"size":0}}`))
				case "empty":
					_, _ = w.Write([]byte(`{"success":true,"data":{"path":"/home/whacp/bob.example/public_html/wp-content/debug.log","exists":true,"lines":[],"size":0}}`))
				default:
					_, _ = w.Write([]byte(`{"success":true,"data":{"path":"/home/whacp/bob.example/public_html/wp-content/debug.log","exists":true,"lines":["[2026-10-20 09:00:00] PHP Notice:  Undefined index foo","[2026-10-20 09:05:00] PHP Warning:  Something"],"size":2048}}`))
				}
			case "verify_core":
				if sa.wpVerifyReply == "ok" {
					_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true,"files":[]}}`))
				} else {
					_, _ = w.Write([]byte(`{"success":true,"data":{"ok":false,"files":["wp-includes/theme-compat/class-wp-theme-compat.php","wp-content/uploads/evil.php"]}}`))
				}
			case "core_update", "plugin_update", "theme_update":
				id := sa.wpManageJobID
				if id == "" {
					id = "0123456789ab0001"
				}
				_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"` + id + `"}}`))
			case "admin_password":
				_, _ = w.Write([]byte(`{"success":true,"data":{"login":"bob"}}`))
			case "db_password":
				_, _ = w.Write([]byte(`{"success":true,"data":{"user":"u_bob_wp"}}`))
			case "cron_takeover_on":
				_, _ = w.Write([]byte(`{"success":true,"data":{"command":"/usr/bin/php8.3.1 /usr/local/lib/whacp/wp-cli.phar --path=/home/whacp/bob.example/public_html cron event run --due-now --quiet"}}`))
			default:
				_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
			}
		case "wordpress.scan":
			res := sa.wpScanReply
			if res == "" {
				res = `{"installs":[{"domain":"bob.example","path":"","version":"6.7.1","db_name":"u_bob_wp","db_host":"localhost","table_prefix":"wp_","debug":true,"file_edit_disabled":false,"config_mode":"0644","maintenance":false}],"errors":[],"latest":"6.8"}`
			}
			_, _ = w.Write([]byte(`{"success":true,"data":` + res + `}`))
		case "cron.log":
			content := sa.cronLogContent
			if content == "" {
				content = defaultCronLogContent
			}
			b, _ := json.Marshal(map[string]any{"content": content, "size": len(content), "truncated": len(content) > 512<<10})
			_, _ = w.Write([]byte(`{"success":true,"data":` + string(b) + `}`))
		case "backup.create":
			jobID := sa.backupJobID
			if jobID == "" {
				jobID = "0123456789ba0001"
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"` + jobID + `"}}`))
		case "backup.restore":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"0123456789ba0002"}}`))
		case "backup.delete":
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		case "backup.download":
			// Two chunks ("hello" then "world") so the streaming loop runs.
			offset := int64(0)
			if o, ok := p["offset"].(float64); ok {
				offset = int64(o)
			}
			if offset == 0 {
				_, _ = w.Write([]byte(`{"success":true,"data":{"data":"aGVsbG8=","size":11,"eof":false}}`))
			} else {
				_, _ = w.Write([]byte(`{"success":true,"data":{"data":"d29ybGQ=","size":11,"eof":true}}`))
			}
		default:
			_, _ = w.Write([]byte(`{"success":true,"data":{},"request_id":"t"}`))
		}
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return sa, socket, tokenFile
}

func (sa *sitesAgent) called() []string {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	return append([]string(nil), sa.actions...)
}

func (sa *sitesAgent) param(i int, key string) any {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	if i >= len(sa.params) {
		return nil
	}
	return sa.params[i][key]
}

// sitesEnv builds a server with a recording agent, a customer (bob) who owns
// website 10 (bob.example), and — when isolation — a second customer (alice)
// who owns website 20. Returns the handler, bob's session cookie and DB.
func sitesEnv(t *testing.T, isolation, failAgent bool) (http.Handler, *http.Cookie, *sitesAgent, *sql.DB) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES
		(2,'customer','Customer','own',''),(5,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),(3,'databases.view','dv','db'),(4,'workers.manage','wm','auto')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (2,1),(2,2),(2,3),(2,4),(5,1)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(2,'alice','a@x','Alice',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'carol','c@x','Carol',?,5,1,'2026-01-01T00:00:00Z','','',NULL)`,
		loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (10,1,1,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,10,'bob.example',1)`)
	if isolation {
		exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
			VALUES (20,1,2,'alice.example','u_alice','/home/whacp/alice.example','/home/whacp/alice.example/public_html','php','8.3.1','active','','')`)
		exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,20,'alice.example',1)`)
	}

	sa, socket, tokenFile := startSitesAgent(t, failAgent)
	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()

	cookie, token := getLogin(t, h)
	// The signed-in state (user + rotated CSRF) is sealed into the cookie the
	// login POST response sets — that one, not the pre-login form cookie.
	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {"bob"}, "password": {loginPassword}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login = %d", rr.Code)
	}
	cookie = rr.Result().Cookies()[0]
	return h, cookie, sa, db
}

func siteFormToken(t *testing.T, h http.Handler, cookie *http.Cookie, path string) (string, *http.Cookie) {
	t.Helper()
	rr := get(t, h, path, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, rr.Code, rr.Body.String())
	}
	m := csrfRe.FindStringSubmatch(rr.Body.String())
	if m == nil {
		t.Fatalf("no CSRF token on %s", path)
	}
	refreshed := cookie
	if cs := rr.Result().Cookies(); len(cs) > 0 {
		refreshed = cs[0]
	}
	return m[1], refreshed
}

func TestWebsitesNewForm(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	rr := get(t, h, "/websites/new", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"Create website", `name="domain"`, `name="php_version"`, "8.3.1", "PHP application"} {
		if !strings.Contains(body, want) {
			t.Errorf("form missing %q", want)
		}
	}
}

func TestWebsitesStoreValidationRerendersWithOldInput(t *testing.T) {
	h, cookie, sa, _ := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/websites/new")
	rr := postForm(h, "/websites/new", url.Values{
		"_token": {token}, "domain": {"no-dots"}, "kind": {"php"},
		"system_user": {"u_x"}, "new_system_user": {"1"},
		"php_version": {"8.3.1"}, "harden": {"on"}, "php_profile": {"recommended"},
		"memory_limit_mb": {"256"}, "upload_max_mb": {"64"}, "max_execution_seconds": {"60"},
		"max_children": {"5"}, "idle_timeout_seconds": {"10"}, "max_requests": {"500"},
		"pm": {"ondemand"}, "client_max_body_mb": {"64"},
	}, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("validation re-render = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Enter a valid domain name") {
		t.Error("field error not rendered")
	}
	if !strings.Contains(body, `value="no-dots"`) {
		t.Error("old input not preserved")
	}
	for _, a := range sa.called() {
		if a == "sites.apply" {
			t.Errorf("agent apply must not be called on a rejected form: %v", sa.called())
		}
	}
}

func TestWebsitesStoreRequiresCSRF(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	rr := postForm(h, "/websites/new", url.Values{"domain": {"x.example"}}, cookie)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing CSRF = %d, want 400", rr.Code)
	}
}

func TestWebsitesStoreHappyPath(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/websites/new")
	rr := postForm(h, "/websites/new", url.Values{
		"_token": {token}, "domain": {"new.example"}, "kind": {"php"},
		"system_user": {"u_new"}, "new_system_user": {"1"},
		"php_version": {"8.3.1"}, "harden": {"on"}, "php_profile": {"recommended"},
		"memory_limit_mb": {"256"}, "upload_max_mb": {"64"}, "max_execution_seconds": {"60"},
		"max_children": {"5"}, "idle_timeout_seconds": {"10"}, "max_requests": {"500"},
		"pm": {"ondemand"}, "client_max_body_mb": {"64"},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/11" {
		t.Fatalf("store = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	acts := sa.called()
	found := false
	for i, a := range acts {
		if a == "sites.apply" {
			found = true
			if sa.param(i, "new_user") != true {
				t.Errorf("apply must carry new_user: %v", sa.params[i])
			}
			if sa.param(i, "domain") != "new.example" {
				t.Errorf("apply domain = %v", sa.param(i, "domain"))
			}
		}
	}
	if !found {
		t.Fatalf("sites.apply never called: %v", acts)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM websites WHERE id = 11`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "active" {
		t.Errorf("status = %q", status)
	}
	var action, result string
	if err := db.QueryRow(`SELECT action, result FROM audit_logs WHERE action = 'website.create'`).Scan(&action, &result); err != nil {
		t.Fatalf("no website.create audit row: %v", err)
	}
	if result != "success" {
		t.Errorf("audit result = %q", result)
	}
}

func TestWebsitesStorePlanLimitRerenders(t *testing.T) {
	// bob's plan is max_websites=5 and he owns 1 site — tighten it to 1.
	h, cookie, sa, dbh := sitesEnv(t, false, false)
	if _, err := dbh.Exec(`UPDATE plans SET max_websites = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	token, cookie := siteFormToken(t, h, cookie, "/websites/new")
	rr := postForm(h, "/websites/new", url.Values{
		"_token": {token}, "domain": {"second.example"}, "kind": {"php"},
		"system_user": {"u_s"}, "new_system_user": {"1"}, "php_version": {"8.3.1"},
		"memory_limit_mb": {"256"}, "upload_max_mb": {"64"}, "max_execution_seconds": {"60"},
		"max_children": {"5"}, "pm": {"ondemand"}, "client_max_body_mb": {"64"},
	}, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("limit re-render = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "allows at most 1 websites") {
		t.Errorf("limit message missing: %s", rr.Body.String())
	}
	for _, a := range sa.called() {
		if a == "sites.apply" {
			t.Errorf("agent apply must not be called when the plan limit blocks: %v", sa.called())
		}
	}
}

func TestWebsitesStoreAgentFailureFlashes(t *testing.T) {
	h, cookie, _, db := sitesEnv(t, false, true)
	token, cookie := siteFormToken(t, h, cookie, "/websites/new")
	rr := postForm(h, "/websites/new", url.Values{
		"_token": {token}, "domain": {"x.example"}, "kind": {"php"},
		"system_user": {"u_x"}, "new_system_user": {"1"}, "php_version": {"8.3.1"},
		"memory_limit_mb": {"256"}, "upload_max_mb": {"64"}, "max_execution_seconds": {"60"},
		"max_children": {"5"}, "pm": {"ondemand"}, "client_max_body_mb": {"64"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("agent failure = %d, want redirect with flash", rr.Code)
	}
	// The failed create must not leave a row behind.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM websites`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("websites = %d after failed create (want 1)", n)
	}
	var result string
	if err := db.QueryRow(`SELECT result FROM audit_logs WHERE action = 'website.create'`).Scan(&result); err != nil {
		t.Fatalf("no failure audit row: %v", err)
	}
	if result != "failure" {
		t.Errorf("audit = %q", result)
	}
}

func TestWebsiteRoutesAreTenantScoped(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, true, false) // alice owns website 20
	for _, path := range []string{
		"/sites/20",
		"/sites/20/settings",
		"/sites/20/subdomains",
		"/sites/20/addons",
		"/sites/999",
	} {
		if rr := get(t, h, path, cookie); rr.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 (other tenant / missing)", path, rr.Code)
		}
	}
	// bob's own site is visible.
	if rr := get(t, h, "/sites/10", cookie); rr.Code != http.StatusOK {
		t.Errorf("GET /sites/10 = %d", rr.Code)
	}
}

func TestWebsiteToggle(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/sites/10")
	rr := postForm(h, "/sites/10/toggle", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("toggle = %d", rr.Code)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM websites WHERE id = 10`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "disabled" {
		t.Errorf("status = %q, want disabled", status)
	}
	last := sa.called()
	i := len(last) - 1
	if last[i] != "sites.apply" || sa.param(i, "enabled") != false {
		t.Errorf("agent apply wrong: %s %v", last[i], sa.param(i, "enabled"))
	}
	var action string
	if err := db.QueryRow(`SELECT action FROM audit_logs WHERE action = 'website.disable'`).Scan(&action); err != nil {
		t.Fatalf("no website.disable audit row: %v", err)
	}
	// Toggle back on.
	token2, cookie2 := siteFormToken(t, h, cookie, "/sites/10")
	if rr := postForm(h, "/sites/10/toggle", url.Values{"_token": {token2}}, cookie2); rr.Code != http.StatusSeeOther {
		t.Fatalf("toggle-on = %d", rr.Code)
	}
	if err := db.QueryRow(`SELECT status FROM websites WHERE id = 10`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "active" {
		t.Errorf("status = %q, want active", status)
	}
}

func TestWebsiteToggleRequiresCSRF(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	if rr := postForm(h, "/sites/10/toggle", url.Values{}, cookie); rr.Code != http.StatusBadRequest {
		t.Fatalf("toggle without CSRF = %d, want 400", rr.Code)
	}
}

func TestWebsiteDeleteRequiresTypedConfirmation(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/sites/10")
	// Wrong confirmation: the site survives.
	rr := postForm(h, "/sites/10/delete", url.Values{"_token": {token}, "confirm_domain": {"nope"}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("bad confirm = %d", rr.Code)
	}
	if n, _ := countRows(t, db, `SELECT COUNT(*) FROM websites WHERE id = 10`); n != 1 {
		t.Fatal("site deleted without correct confirmation")
	}
	for _, a := range sa.called() {
		if a == "sites.delete" {
			t.Errorf("agent must not be called on a bad confirm: %v", sa.called())
		}
	}
	// Correct confirmation: the site goes.
	token2, cookie2 := siteFormToken(t, h, cookie, "/sites/10")
	rr = postForm(h, "/sites/10/delete", url.Values{
		"_token": {token2}, "confirm_domain": {"bob.example"}, "remove_files": {"1"},
	}, cookie2)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/websites" {
		t.Fatalf("delete = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	if n, _ := countRows(t, db, `SELECT COUNT(*) FROM websites WHERE id = 10`); n != 0 {
		t.Fatal("site row still present")
	}
	acts := sa.called()
	if len(acts) != 1 || acts[0] != "sites.delete" {
		t.Fatalf("agent actions = %v", acts)
	}
	if sa.param(0, "remove_files") != true || sa.param(0, "remove_user") != false {
		t.Errorf("agent delete params wrong: %v", sa.params[0])
	}
	var result string
	if err := db.QueryRow(`SELECT result FROM audit_logs WHERE action = 'website.delete'`).Scan(&result); err != nil {
		t.Fatalf("no website.delete audit row: %v", err)
	}
}

func TestSubdomainStore(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/sites/10/subdomains")
	rr := postForm(h, "/sites/10/subdomains", url.Values{"_token": {token}, "subdomain": {"blog"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/subdomains" {
		t.Fatalf("store subdomain = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	var domain string
	if err := db.QueryRow(`SELECT domain FROM websites WHERE parent_id = 10`).Scan(&domain); err != nil {
		t.Fatalf("child missing: %v", err)
	}
	if domain != "blog.bob.example" {
		t.Errorf("child domain = %q", domain)
	}
	// "www" is the website's own alias, never a subdomain.
	token2, cookie2 := siteFormToken(t, h, cookie, "/sites/10/subdomains")
	rr = postForm(h, "/sites/10/subdomains", url.Values{"_token": {token2}, "subdomain": {"www"}}, cookie2)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Use letters, digits and hyphens") {
		t.Errorf("www subdomain must be rejected: %d", rr.Code)
	}
	apply := 0
	for _, a := range sa.called() {
		if a == "sites.apply" {
			apply++
		}
	}
	if apply != 1 {
		t.Errorf("agent apply count = %d (want 1 for the first child)", apply)
	}
}

func TestSubdomainStoreRequiresCSRF(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	if rr := postForm(h, "/sites/10/subdomains", url.Values{"subdomain": {"blog"}}, cookie); rr.Code != http.StatusBadRequest {
		t.Fatalf("subdomain store without CSRF = %d, want 400", rr.Code)
	}
}

func TestAddonStore(t *testing.T) {
	h, cookie, _, db := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/sites/10/addons")
	rr := postForm(h, "/sites/10/addons", url.Values{"_token": {token}, "domain": {"ADDON.example"}, "www": {"on"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/addons" {
		t.Fatalf("store addon = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	var domain, alias string
	if err := db.QueryRow(`SELECT domain FROM websites WHERE parent_id = 10`).Scan(&domain); err != nil {
		t.Fatalf("addon missing: %v", err)
	}
	if domain != "addon.example" {
		t.Errorf("addon domain = %q", domain)
	}
	if err := db.QueryRow(`SELECT name FROM domains WHERE name = 'www.addon.example'`).Scan(&alias); err != nil {
		t.Fatalf("www alias missing: %v", err)
	}
	// A subdomain of the parent is not an addon.
	token2, cookie2 := siteFormToken(t, h, cookie, "/sites/10/addons")
	rr = postForm(h, "/sites/10/addons", url.Values{"_token": {token2}, "domain": {"x.bob.example"}}, cookie2)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "add it as a subdomain") {
		t.Errorf("addon under parent must be rejected: %d", rr.Code)
	}
}

func TestWebsitesNewRequiresManagePermission(t *testing.T) {
	// carol has websites.view only: the create form and its POST are denied.
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES (5,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES (1,'websites.view','v','web')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (5,1)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at)
		VALUES (1,'carol','c@x','Carol',?,5,1,'2026-01-01T00:00:00Z','','')`, loginHash)

	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(config.Config{Production: true, KeyFile: kf}, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	cookie, token := getLogin(t, h)
	if rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {"carol"}, "password": {loginPassword}}, cookie); rr.Code != http.StatusSeeOther {
		t.Fatalf("login = %d", rr.Code)
	}
	if rr := get(t, h, "/websites/new", cookie); rr.Code != http.StatusForbidden {
		t.Errorf("GET /websites/new = %d, want 403", rr.Code)
	}
	token2, _ := csrfTokenFromLogin(t, h, cookie)
	rr := postForm(h, "/websites/new", url.Values{"_token": {token2}, "domain": {"x.example"}}, cookie)
	if rr.Code != http.StatusForbidden {
		t.Errorf("POST /websites/new = %d, want 403", rr.Code)
	}
}

func countRows(t *testing.T, db *sql.DB, q string) (int, error) {
	t.Helper()
	var n int
	err := db.QueryRow(q).Scan(&n)
	return n, err
}

// csrfTokenFromLogin returns a CSRF token for an authenticated session by
// loading the website list (any form page mints one).
func csrfTokenFromLogin(t *testing.T, h http.Handler, cookie *http.Cookie) (string, *http.Cookie) {
	t.Helper()
	return siteFormToken(t, h, cookie, "/websites")
}

