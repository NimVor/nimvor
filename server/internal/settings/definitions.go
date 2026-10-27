// Code generated from SettingsService::definitions() — keys, types, defaults,
// bounds, labels and help text of every panel setting. Edit with care: the
// PHP panel and this one read the same table.

package settings

// Def describes one setting.
type Def struct {
	Key, Category, Type, Label, Help, Pattern string
	Default                                   any
	Min, Max, MaxLength                       int
	HasMin, HasMax, Optional                  bool
	Options                                   []Option
}

// Option is one choice of a setting with fixed options.
type Option struct{ Value, Label string }

// Definitions lists every setting in the PHP panel's order.
var Definitions = []Def{
	{Key: "panel_name", Category: "general", Type: "string", Label: "Panel name", Help: "Shown in the header and browser title.", Default: "WhaCP", MaxLength: 60},
	{Key: "panel_url", Category: "general", Type: "url", Label: "Public panel URL", Help: "e.g. https://panel.example.com. Used for Git webhook URLs, password reset links and Google Drive; when empty the address you are browsing is used.", Default: "", MaxLength: 200},
	{Key: "session_idle_minutes", Category: "security", Type: "int", Label: "Session idle timeout (minutes)", Help: "Users are signed out after this period of inactivity.", Default: 30, Min: 5, HasMin: true, Max: 1440, HasMax: true},
	{Key: "discovery_cache_seconds", Category: "general", Type: "int", Label: "Discovery cache (seconds)", Help: "How long a discovery snapshot is reused before the agent is asked again. 0 always asks the agent.", Default: 300, Min: 0, HasMin: true, Max: 86400, HasMax: true},
	{Key: "dashboard_refresh_seconds", Category: "general", Type: "int", Label: "Dashboard refresh interval (seconds)", Help: "How often the dashboard polls live metrics.", Default: 10, Min: 3, HasMin: true, Max: 300, HasMax: true},
	{Key: "alert_disk_percent", Category: "monitoring", Type: "int", Label: "Disk usage alert (%)", Help: "Alert when any disk is fuller than this.", Default: 90, Min: 50, HasMin: true, Max: 99, HasMax: true},
	{Key: "alert_memory_percent", Category: "monitoring", Type: "int", Label: "Memory usage alert (%)", Help: "Alert when memory use stays above this for two samples in a row.", Default: 95, Min: 50, HasMin: true, Max: 100, HasMax: true},
	{Key: "alert_load_per_core", Category: "monitoring", Type: "int", Label: "Load alert (per CPU core)", Help: "Alert when the 1-minute load exceeds this many times the number of cores.", Default: 2, Min: 1, HasMin: true, Max: 20, HasMax: true},
	{Key: "alert_webhook_url", Category: "monitoring", Type: "url", Label: "Alert webhook URL", Help: "Optional https:// URL that receives alerts as JSON ({\"text\": …}); works with Slack, Mattermost, Discord (/slack) and ntfy.", Default: "", MaxLength: 500},
	{Key: "dns_nameservers", Category: "dns", Type: "string", Label: "DNS name servers", Help: "Host names of the name servers for hosted zones, separated by commas (e.g. ns1.example.com, ns2.example.com). New zones get NS records for them.", Default: "", MaxLength: 300, Optional: true},
	{Key: "dns_hostmaster", Category: "dns", Type: "string", Label: "DNS hostmaster email", Help: "Contact address in the SOA record of new zones. Empty uses hostmaster@<zone>.", Default: "", MaxLength: 200, Optional: true},
	{Key: "dns_default_ipv4", Category: "dns", Type: "string", Label: "Default IPv4 for new zones", Help: "The public address new zones point @ and www to. Empty leaves the A records out.", Default: "", MaxLength: 15, Optional: true},
	{Key: "acme_provider", Category: "ssl", Type: "string", Label: "Certificate authority", Help: "The ACME provider used to issue Let's Encrypt-style certificates (for new and automatic SSL, the host name and services). Register the chosen provider's account on SSL/TLS → Services SSL first; ZeroSSL needs External Account Binding credentials.", Default: "letsencrypt", Options: []Option{{"letsencrypt", "Let's Encrypt"}, {"zerossl", "ZeroSSL"}, {"buypass", "Buypass Go"}}},
	{Key: "auto_ssl", Category: "ssl", Type: "bool", Label: "Automatic SSL for new websites", Help: "Issue a certificate from the configured provider for a new website once its domain points to this server (its name servers are this panel's, or its address is this server's). Checked every few minutes for up to 3 days.", Default: true},
	{Key: "auto_ssl_replace", Category: "ssl", Type: "bool", Label: "Replace expired or invalid certificates", Help: "Every few hours, a website whose certificate has expired, expires within 7 days without being renewed, does not cover its domain or is self-signed gets a new Let's Encrypt certificate once its domain points to this server. Every attempt is logged on SSL/TLS → AutoSSL.", Default: true},
	{Key: "av_scan_days", Category: "security", Type: "int", Label: "Scheduled malware scans (every N days)", Help: "Scan every website with ClamAV every this many days, as the website's account, at night. 0 turns scheduled scans off.", Default: 0, Min: 0, HasMin: true, Max: 30, HasMax: true},
	{Key: "av_quarantine", Category: "security", Type: "bool", Label: "Quarantine infected files", Help: "Move files a malware scan finds into the website's .whacp-quarantine folder (unreadable) instead of only reporting them.", Default: false},
	{Key: "av_scan_on_upload", Category: "security", Type: "bool", Label: "Scan files uploaded in the File Manager", Help: "When ClamAV is installed, scan each file uploaded through the File Manager as soon as it finishes, as the website's account. Infected files are quarantined (made unreadable) so they cannot be served.", Default: true},
	{Key: "av_scan_on_extract", Category: "security", Type: "bool", Label: "Scan archives extracted in the File Manager", Help: "When ClamAV is installed, scan the files written by a .zip/.tar extraction. Infected files are quarantined.", Default: true},
	{Key: "av_scan_max_mb", Category: "security", Type: "int", Label: "Maximum size to scan on upload (MB)", Help: "Skip scanning an uploaded file larger than this, to bound resource use. 0 scans files of any size. Scheduled and manual whole-site scans are not affected.", Default: 512, Min: 0, HasMin: true, Max: 10240, HasMax: true},
	{Key: "apps_installer_enabled", Category: "general", Type: "bool", Label: "Application installer", Help: "Let customers install applications (WordPress, Joomla, …) from the Apps page. Off hides the installer for everyone.", Default: true},
	{Key: "nginx_client_max_body_mb", Category: "server", Type: "int", Label: "Max upload size (MB)", Help: "Nginx client_max_body_size, server-wide.", Default: 128, Min: 1, HasMin: true, Max: 4096, HasMax: true},
	{Key: "nginx_keepalive", Category: "server", Type: "int", Label: "Keep-alive timeout (s)", Help: "Nginx keepalive_timeout.", Default: 65, Min: 1, HasMin: true, Max: 3600, HasMax: true},
	{Key: "nginx_body_timeout", Category: "server", Type: "int", Label: "Request body timeout (s)", Help: "Nginx client_body_timeout.", Default: 60, Min: 1, HasMin: true, Max: 3600, HasMax: true},
	{Key: "nginx_send_timeout", Category: "server", Type: "int", Label: "Send timeout (s)", Help: "Nginx send_timeout.", Default: 60, Min: 1, HasMin: true, Max: 3600, HasMax: true},
	{Key: "nginx_gzip", Category: "server", Type: "bool", Label: "Gzip compression", Help: "Compress text responses (HTML, CSS, JS, JSON, SVG).", Default: true},
	{Key: "nginx_hide_version", Category: "server", Type: "bool", Label: "Hide Nginx version", Help: "server_tokens off — do not reveal the Nginx version in responses and error pages.", Default: true},
	{Key: "terminal_idle_minutes", Category: "security", Type: "int", Label: "Web terminal idle timeout (minutes)", Help: "A web terminal closes after this many minutes without input.", Default: 15, Min: 1, HasMin: true, Max: 240, HasMax: true},
	{Key: "terminal_max_minutes", Category: "security", Type: "int", Label: "Web terminal maximum session (minutes)", Help: "A web terminal closes after this long, active or not.", Default: 480, Min: 15, HasMin: true, Max: 1440, HasMax: true},
	{Key: "modsec_backup_retention_days", Category: "security", Type: "int", Label: "ModSecurity installation backups: keep (days)", Help: "Backups and records of ModSecurity connector and rule installations are kept this long. The one in effect (the last known-good) is always kept.", Default: 30, Min: 1, HasMin: true, Max: 3650, HasMax: true},
	{Key: "db_customer_remote_hosts", Category: "security", Type: "bool", Label: "Hosting customers may allow remote database connections", Help: "Customers and resellers may let their database users connect from other hosts. Off: from this server only (administrators still can). Existing remote logins are kept.", Default: true},
	{Key: "php_harden_new_sites", Category: "security", Type: "bool", Label: "Disable shell functions for new websites", Help: "New websites start with exec, system, shell_exec, passthru, proc_open, popen and pcntl_exec disabled. Some applications (for example Composer or queue workers run from PHP) need them; an administrator can turn this off per website.", Default: false},
	{Key: "default_php_security_profile", Category: "security", Type: "string", Label: "PHP security profile for new websites", Help: "Recommended disables the dangerous functions (exec, system, shell_exec, passthru, popen, proc_open, proc_get_status, proc_terminate and pcntl_*). Balanced keeps process-control functions that queue workers may need. Compatibility disables nothing. The profile applies to new websites only; existing websites keep their own, and an administrator can change any website's profile or set a custom list.", Default: "recommended", Options: []Option{{"recommended", "Recommended"}, {"balanced", "Balanced"}, {"compatibility", "Compatibility"}}},
	{Key: "wordpress_auto_harden", Category: "security", Type: "bool", Label: "Harden new WordPress installations automatically", Help: "WordPress installed with the app installer gets the recommended security measures (web server rules and WordPress settings) as soon as it is installed. Each measure can be rolled back in the WordPress toolkit.", Default: true},
	{Key: "policy_password_strength", Category: "policy", Type: "bool", Label: "Password Strength", Help: "New passwords must meet the Password Strength Configuration.", Default: true},
	{Key: "policy_password_age_days", Category: "policy", Type: "int", Label: "Password Age (days)", Help: "Users must choose a new password after this many days. 0 turns it off.", Default: 0, Min: 0, HasMin: true, Max: 3650, HasMax: true},
	{Key: "policy_verified_ips", Category: "policy", Type: "bool", Label: "Limit logins to verified IP addresses", Help: "A sign-in from an IP address the user has not used before needs a code sent to the user's email address.", Default: false},
	{Key: "password_min_strength", Category: "policy", Type: "int", Label: "Minimum password strength", Help: "0–100, from the length and the kinds of characters used.", Default: 65, Min: 0, HasMin: true, Max: 100, HasMax: true},
	{Key: "password_min_length", Category: "policy", Type: "int", Label: "Total characters", Help: "At least 12.", Default: 12, Min: 12, HasMin: true, Max: 128, HasMax: true},
	{Key: "password_min_special", Category: "policy", Type: "int", Label: "Special characters", Help: "Characters other than letters and digits.", Default: 0, Min: 0, HasMin: true, Max: 32, HasMax: true},
	{Key: "password_min_numeric", Category: "policy", Type: "int", Label: "Numeric characters", Help: "0–9.", Default: 0, Min: 0, HasMin: true, Max: 32, HasMax: true},
	{Key: "password_min_alpha", Category: "policy", Type: "int", Label: "Alphabet characters", Help: "Letters, upper or lower case.", Default: 0, Min: 0, HasMin: true, Max: 32, HasMax: true},
	{Key: "password_min_upper", Category: "policy", Type: "int", Label: "Uppercase characters", Help: "A–Z.", Default: 0, Min: 0, HasMin: true, Max: 32, HasMax: true},
	{Key: "password_min_lower", Category: "policy", Type: "int", Label: "Lowercase characters", Help: "a–z.", Default: 0, Min: 0, HasMin: true, Max: 32, HasMax: true},
	{Key: "hulk_enabled", Category: "hulk", Type: "bool", Label: "cPHulk Brute Force Protection", Help: "", Default: false},
	{Key: "hulk_user_enabled", Category: "hulk", Type: "bool", Label: "Username-based Protection", Help: "", Default: true},
	{Key: "hulk_user_period", Category: "hulk", Type: "int", Label: "Brute Force Protection Period (in minutes)", Help: "", Default: 5, Min: 1, HasMin: true, Max: 1440, HasMax: true},
	{Key: "hulk_user_max", Category: "hulk", Type: "int", Label: "Maximum Failures by Account", Help: "", Default: 5, Min: 1, HasMin: true, Max: 1000, HasMax: true},
	{Key: "hulk_user_scope", Category: "hulk", Type: "string", Label: "Protection type", Help: "local or all", Default: "all", MaxLength: 5, Options: []Option{{"local", "Apply protection to local addresses only"}, {"all", "Apply protection to local and remote addresses"}}},
	{Key: "hulk_user_lock_root", Category: "hulk", Type: "bool", Label: "Allow username protection to lock the “root” user", Help: "", Default: false},
	{Key: "hulk_ip_enabled", Category: "hulk", Type: "bool", Label: "IP Address-based Protection", Help: "", Default: true},
	{Key: "hulk_ip_period", Category: "hulk", Type: "int", Label: "IP Address-based Brute Force Protection Period (in minutes)", Help: "", Default: 15, Min: 1, HasMin: true, Max: 1440, HasMax: true},
	{Key: "hulk_ip_max", Category: "hulk", Type: "int", Label: "Maximum Failures per IP Address", Help: "", Default: 15, Min: 1, HasMin: true, Max: 1000, HasMax: true},
	{Key: "hulk_ip_firewall", Category: "hulk", Type: "bool", Label: "Block IP addresses at the firewall level if they trigger brute force protection", Help: "", Default: false},
	{Key: "hulk_oneday_max", Category: "hulk", Type: "int", Label: "Maximum Failures per IP Address before the IP Address is Blocked for One Day", Help: "", Default: 30, Min: 1, HasMin: true, Max: 10000, HasMax: true},
	{Key: "hulk_oneday_firewall", Category: "hulk", Type: "bool", Label: "Block IP addresses at the firewall level if they trigger a one-day block", Help: "", Default: false},
	{Key: "hulk_retention", Category: "hulk", Type: "int", Label: "Duration for Retaining Failed Logins (in minutes)", Help: "", Default: 360, Min: 5, HasMin: true, Max: 10080, HasMax: true},
	{Key: "hulk_notify_root", Category: "hulk", Type: "bool", Label: "Send a notification upon successful root login when the IP address is not on the whitelist", Help: "", Default: true},
	{Key: "hulk_notify_root_netblock", Category: "hulk", Type: "bool", Label: "Send a notification upon successful root login when the IP address is not on the whitelist, but from a known netblock", Help: "", Default: false},
	{Key: "hulk_notify_brute", Category: "hulk", Type: "bool", Label: "Send a notification when the system detects a brute force user", Help: "", Default: true},
	{Key: "php_profile", Category: "server", Type: "string", Label: "PHP profile", Help: "PHP Manager: the PHP versions and extensions last applied (JSON).", Default: "", MaxLength: 2000, Optional: true},
	{Key: "setup_wizard_seen", Category: "server", Type: "bool", Label: "Setup wizard opened", Help: "The dashboard opens the setup wizard once, for the first administrator visit.", Default: false},
	{Key: "setup_wizard_done", Category: "server", Type: "bool", Label: "Setup wizard closed", Help: "The dashboard no longer offers the setup wizard.", Default: false},
	{Key: "server_setup", Category: "server", Type: "string", Label: "Server setup", Help: "The last run of the server setup (profile, options, each step's result; JSON).", Default: "", MaxLength: 12000, Optional: true},
	{Key: "components_expected", Category: "server", Type: "string", Label: "Components this server should have", Help: "Server → Components. Empty: the installed ones.", Default: "", MaxLength: 300, Optional: true},
	{Key: "hulk_countries_all", Category: "hulk", Type: "bool", Label: "Block blacklisted countries on every port", Help: "Like CSF's CC_DENY: blacklisted countries cannot reach any service, websites included. Otherwise only WhaCP and the login services.", Default: false},
	{Key: "hulk_whitelist", Category: "hulk", Type: "string", Label: "Whitelisted IP addresses", Help: "", Default: "", MaxLength: 4000, Optional: true},
	{Key: "hulk_synced", Category: "hulk", Type: "string", Label: "Brute force protection rules applied", Help: "", Default: "", MaxLength: 5, Optional: true},
	{Key: "hulk_geo_updated", Category: "hulk", Type: "string", Label: "Country database date", Help: "", Default: "", MaxLength: 20, Optional: true},
	{Key: "hulk_geo_tried", Category: "hulk", Type: "string", Label: "Country database download attempt", Help: "", Default: "", MaxLength: 20, Optional: true},
	{Key: "default_plan_id", Category: "accounts", Type: "int", Label: "Default hosting plan", Help: "Chosen first in the form for a new account. None: no plan (unlimited).", Default: 0, Min: 0, HasMin: true, Max: 1000000, HasMax: true},
	{Key: "default_php_version", Category: "accounts", Type: "string", Label: "Default PHP version for new websites", Help: "Chosen first for a new account or website when installed. Empty: the newest installed.", Default: "", MaxLength: 5, Optional: true, Pattern: "^[5-9]\\.[0-9]{1,2}$"},
	{Key: "account_dns_dkim", Category: "accounts", Type: "bool", Label: "New accounts: DKIM record", Help: "The new account's DNS zone gets a DKIM record (signed outgoing email).", Default: true},
	{Key: "account_dns_spf", Category: "accounts", Type: "bool", Label: "New accounts: SPF record", Help: "The new account's DNS zone gets an SPF record naming this server.", Default: true},
	{Key: "account_dns_dmarc", Category: "accounts", Type: "bool", Label: "New accounts: DMARC record", Help: "The new account's DNS zone gets a DMARC record.", Default: true},
	{Key: "account_mail_routing", Category: "accounts", Type: "string", Label: "New accounts: email routing", Help: "Whether this server accepts the new domain's email.", Default: "auto", MaxLength: 6, Options: []Option{{"auto", "Automatically detect configuration"}, {"local", "Local mail exchanger"}, {"backup", "Backup mail exchanger"}, {"remote", "Remote mail exchanger"}}},
	{Key: "account_spam_filter", Category: "accounts", Type: "bool", Label: "New accounts: spam filter", Help: "Incoming email of the new account is checked for spam.", Default: true},
	{Key: "account_spam_box", Category: "accounts", Type: "bool", Label: "New accounts: move spam to the Spam folder", Help: "Instead of only marking it.", Default: true},
	{Key: "trash_retention_days", Category: "general", Type: "int", Label: "File Manager trash: keep deleted files (days)", Help: "Files deleted in the File Manager can be restored from its Trash for this many days, then they are deleted for good. 0 keeps them until the trash is emptied.", Default: 30, Min: 0, HasMin: true, Max: 365, HasMax: true},
	{Key: "ssl_warning_days", Category: "ssl", Type: "int", Label: "Certificate warning threshold (days)", Help: "Certificates expiring within this many days are highlighted.", Default: 14, Min: 1, HasMin: true, Max: 90, HasMax: true},
}

var defIndex = func() map[string]*Def {
	m := make(map[string]*Def, len(Definitions))
	for i := range Definitions {
		m[Definitions[i].Key] = &Definitions[i]
	}
	return m
}()

// Lookup returns the definition of a key.
func Lookup(key string) (*Def, bool) { d, ok := defIndex[key]; return d, ok }
