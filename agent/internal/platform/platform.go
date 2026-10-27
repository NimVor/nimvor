// Package platform is the OS abstraction layer.
//
// Distribution-specific knowledge (file locations, service names, package
// naming) lives only here. The rest of the agent asks the Platform where
// things are instead of hard-coding paths.
package platform

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/filesystem"
)

// OSInfo describes the detected operating system (from /etc/os-release).
type OSInfo struct {
	ID         string   `json:"id"`
	IDLike     []string `json:"id_like"`
	Name       string   `json:"name"`
	PrettyName string   `json:"pretty_name"`
	VersionID  string   `json:"version_id"`
	Codename   string   `json:"codename"`
	Family     string   `json:"family"`
	Supported  bool     `json:"supported"`
	// Preview: supported and tested, but not yet recommended for
	// production (newest releases).
	Preview bool `json:"preview"`
}

// Paths lists well-known locations for a distribution.
type Paths struct {
	NginxMainConfig string `json:"nginx_main_config"`
	// NginxMainCandidates are tried in order when NginxMainConfig is absent
	// (other control panels install Nginx in their own prefix).
	NginxMainCandidates []string `json:"nginx_main_candidates"`
	NginxSitesAvail     string   `json:"nginx_sites_available"`
	NginxSitesEnabled   string   `json:"nginx_sites_enabled"`
	NginxConfD          string   `json:"nginx_conf_d"`
	NginxIncludeRoots   []string `json:"nginx_include_roots"`
	PHPBinaryGlobs      []string `json:"php_binary_globs"`
	PHPFPMBinaryGlobs   []string `json:"php_fpm_binary_globs"`
	PHPFPMPoolGlobs     []string `json:"php_fpm_pool_globs"`
	PHPIniGlobs         []string `json:"php_ini_globs"`
	// PHPFPMService is the FPM unit name pattern (%s = PHPTag of the version).
	PHPFPMService  string   `json:"php_fpm_service"`
	PHPCompact     bool     `json:"php_compact"`
	LetsEncryptDir string   `json:"letsencrypt_dir"`
	MySQLConfigs   []string `json:"mysql_configs"`
	MySQLDataDir   string   `json:"mysql_data_dir"`
	RedisConfigs   []string `json:"redis_configs"`
	SystemCrontab  string   `json:"system_crontab"`
	CronDDir       string   `json:"cron_d_dir"`
	UserCrontabDir string   `json:"user_crontab_dir"`
	PasswdFile     string   `json:"passwd_file"`
	UFWConfig      string   `json:"ufw_config"`
	// SystemLogs maps log names (auth, syslog, mysql, …) to files.
	SystemLogs map[string]string `json:"system_logs"`
	// PHPFPMLog is the FPM log of a version (%s = PHPTag).
	PHPFPMLog       string   `json:"php_fpm_log"`
	SystemdUnitDirs []string `json:"systemd_unit_dirs"`
}

// PHPTag is how a PHP version ("8.3") appears in paths and unit names:
// unchanged, or without the dot when compact ("83"). Globs pass through.
func PHPTag(compact bool, v string) string {
	if compact {
		return strings.ReplaceAll(v, ".", "")
	}
	return v
}

// PoolDir is the FPM pool folder of PHP version v ("*" for a glob).
func (m Managed) PoolDir(v string) string { return fmt.Sprintf(m.PHPPoolDir, PHPTag(m.PHPCompact, v)) }

// FPMUnit is the FPM service of PHP version v.
func (m Managed) FPMUnit(v string) string { return fmt.Sprintf(m.PHPFPMUnit, PHPTag(m.PHPCompact, v)) }

// LiveDir is where certbot keeps current certificates.
func (m Managed) LiveDir() string { return "/etc/letsencrypt/live" }

// Platform is implemented per distribution family.
type Platform interface {
	Info() OSInfo
	// PackageManager is "apt" or "dnf".
	PackageManager() string
	// PackageName maps a package name as NimVor knows it (Debian's) to this
	// distribution's; ok is false when it has no equivalent.
	PackageName(name string) (pkg string, ok bool)
	Paths() Paths
	// CoreServices returns systemd units NimVor monitors by default.
	// Units that do not exist on the host are filtered out later.
	CoreServices() []string
	// PHPFPMUnitPatterns returns globs for per-version PHP-FPM unit files.
	PHPFPMUnitPatterns() []string
	// Managed returns where NimVor writes the files it owns.
	Managed() Managed
	// PHPPackages returns packages that install PHP-FPM version v with the
	// given extensions (e.g. "mysql", "curl").
	PHPPackages(v string, exts []string) []string
}

// Managed describes the layout of NimVor-owned configuration.
type Managed struct {
	// NginxAvailable holds generated site files; NginxEnabled holds the
	// symlinks Nginx actually includes.
	NginxAvailable string `json:"nginx_available"`
	NginxEnabled   string `json:"nginx_enabled"`
	// PHPPoolDir returns the pool directory for a version (%s = version).
	PHPPoolDir string `json:"php_pool_dir"`
	// PHPSocketDir is where per-site FPM sockets are created.
	PHPSocketDir string `json:"php_socket_dir"`
	// PHPSocketTmpfiles, when set, is NimVor's tmpfiles.d entry that
	// recreates PHPSocketDir at boot (no PHP package does on RHEL with
	// Remi's parallel versions).
	PHPSocketTmpfiles string `json:"php_socket_tmpfiles,omitempty"`
	// PHPFPMUnit is the unit name pattern (%s = version).
	PHPFPMUnit string `json:"php_fpm_unit"`
	// PHPFPMBinary is the FPM binary command ID pattern (%s = version).
	PHPFPMBinary string `json:"php_fpm_binary"`
	// PHPCompact writes versions without the dot in PHPPoolDir and
	// PHPFPMUnit (Remi's "php83" on RHEL; "8.3" on Debian).
	PHPCompact   bool     `json:"php_compact"`
	WebUser      string   `json:"web_user"`
	NginxUnit    string   `json:"nginx_unit"`
	CronDir      string   `json:"cron_dir"`
	UnitDir      string   `json:"unit_dir"`
	ACMERoot     string   `json:"acme_root"`
	SSHDropIn    string   `json:"ssh_drop_in"`
	Fail2banJail string   `json:"fail2ban_jail"`
	MySQLUnit    []string `json:"mysql_units"`
	RedisUnit    string   `json:"redis_unit"`
	RedisConfig  string   `json:"redis_config"`
	// Memcached: Debian's option file (-m 64 …) or RHEL's sysconfig file.
	MemcachedConfig string `json:"memcached_config"`
	MemcachedUnit   string `json:"memcached_unit"`
	// FTPDir holds NimVor's own pure-ftpd configuration, account list and
	// certificate (the distribution's pure-ftpd configuration is not used).
	FTPDir string `json:"ftp_dir"`
	// SFTPDropIn holds the per-website SFTP Match blocks; SFTPKeysDir the
	// root-owned authorized keys of website accounts.
	SFTPDropIn  string `json:"sftp_drop_in"`
	SFTPKeysDir string `json:"sftp_keys_dir"`
	// DNS (BIND): DNSConfFile is the package file that gets NimVor's one
	// include line (after confirmation); zones and their list live in
	// DNSZoneDir. DNSUser is the account named runs as.
	DNSConfFile string `json:"dns_conf_file"`
	DNSZoneDir  string `json:"dns_zone_dir"`
	DNSUser     string `json:"dns_user"`
	DNSUnit     string `json:"dns_unit"`
	// Email: NimVor's own files (MailDir), mail storage (VmailDir), the
	// Postfix and Dovecot package files changed after confirmation, and
	// NimVor's Dovecot drop-in.
	MailDir         string `json:"mail_dir"`
	VmailDir        string `json:"vmail_dir"`
	PostfixDir      string `json:"postfix_dir"`
	DovecotAuthConf string `json:"dovecot_auth_conf"`
	DovecotDropIn   string `json:"dovecot_drop_in"`
	// Webmail (Roundcube from the distribution's packages): NimVor keeps
	// its own configuration (WebmailDir) and data (WebmailData) and never
	// edits the package's files. RoundcubeDir holds SQL/ and bin/,
	// RoundcubePublic is the web root, RoundcubeDefaults the package's
	// defaults.inc.php.
	// ServerConfig maps server-backup components to their folders (nginx,
	// php, mysql, redis, letsencrypt, fail2ban, cron, firewall).
	ServerConfig      map[string]string `json:"server_config"`
	WebmailDir        string            `json:"webmail_dir"`
	WebmailData       string            `json:"webmail_data"`
	RoundcubeDir      string            `json:"roundcube_dir"`
	RoundcubePublic   string            `json:"roundcube_public"`
	RoundcubeDefaults string            `json:"roundcube_defaults"`
}

// supportedUbuntu lists the Ubuntu releases NimVor officially targets;
// previewUbuntu those among them not yet recommended for production.
var (
	supportedUbuntu = map[string]bool{"22.04": true, "24.04": true, "26.04": true}
	previewUbuntu   = map[string]bool{"26.04": true}
)

// supportedRHEL lists the RHEL-compatible distributions and major
// releases NimVor targets.
var (
	supportedRHEL      = map[string]bool{"almalinux": true, "rocky": true, "rhel": true}
	supportedRHELMajor = map[string]bool{"9": true, "10": true}
	previewRHELMajor   = map[string]bool{"10": true}
)

// Detect inspects os-release under root and returns the matching Platform.
// Unknown systems fall back to the Debian family layout with Supported=false.
func Detect(root filesystem.Root) Platform {
	info := ReadOSInfo(root)
	switch info.Family {
	case "rhel":
		return rhelFamily{info: info}
	default:
		return debianFamily{info: info}
	}
}

// ReadOSInfo parses /etc/os-release (or /usr/lib/os-release).
func ReadOSInfo(root filesystem.Root) OSInfo {
	var data []byte
	for _, p := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		if b, err := root.ReadFile(p); err == nil {
			data = b
			break
		}
	}
	return ParseOSRelease(data)
}

// ParseOSRelease parses os-release content.
func ParseOSRelease(data []byte) OSInfo {
	kv := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		kv[k] = strings.Trim(v, `"'`)
	}
	info := OSInfo{
		ID:         strings.ToLower(kv["ID"]),
		Name:       kv["NAME"],
		PrettyName: kv["PRETTY_NAME"],
		VersionID:  kv["VERSION_ID"],
		Codename:   kv["VERSION_CODENAME"],
	}
	if like := strings.TrimSpace(kv["ID_LIKE"]); like != "" {
		info.IDLike = strings.Fields(strings.ToLower(like))
	}
	if info.ID == "" {
		info.ID = "unknown"
	}
	info.Family = family(info)
	major, _, _ := strings.Cut(info.VersionID, ".")
	info.Supported = (info.ID == "ubuntu" && supportedUbuntu[info.VersionID]) || (supportedRHEL[info.ID] && supportedRHELMajor[major])
	info.Preview = info.Supported && ((info.ID == "ubuntu" && previewUbuntu[info.VersionID]) || (supportedRHEL[info.ID] && previewRHELMajor[major]))
	return info
}

func family(info OSInfo) string {
	ids := append([]string{info.ID}, info.IDLike...)
	for _, id := range ids {
		switch id {
		case "debian", "ubuntu":
			return "debian"
		case "rhel", "centos", "fedora", "almalinux", "rocky":
			return "rhel"
		}
	}
	return "unknown"
}
