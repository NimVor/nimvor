// Package platform is the OS abstraction layer.
//
// Distribution-specific knowledge (file locations, service names, package
// naming) lives only here. The rest of the agent asks the Platform where
// things are instead of hard-coding paths.
package platform

import (
	"bufio"
	"bytes"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
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
	LetsEncryptDir      string   `json:"letsencrypt_dir"`
	MySQLConfigs        []string `json:"mysql_configs"`
	MySQLDataDir        string   `json:"mysql_data_dir"`
	RedisConfigs        []string `json:"redis_configs"`
	SystemCrontab       string   `json:"system_crontab"`
	CronDDir            string   `json:"cron_d_dir"`
	UserCrontabDir      string   `json:"user_crontab_dir"`
	PasswdFile          string   `json:"passwd_file"`
	UFWConfig           string   `json:"ufw_config"`
	SystemdUnitDirs     []string `json:"systemd_unit_dirs"`
}

// LiveDir is where certbot keeps current certificates.
func (m Managed) LiveDir() string { return "/etc/letsencrypt/live" }

// Platform is implemented per distribution family.
type Platform interface {
	Info() OSInfo
	Paths() Paths
	// CoreServices returns systemd units WhaCP monitors by default.
	// Units that do not exist on the host are filtered out later.
	CoreServices() []string
	// PHPFPMUnitPatterns returns globs for per-version PHP-FPM unit files.
	PHPFPMUnitPatterns() []string
	// Managed returns where WhaCP writes the files it owns.
	Managed() Managed
	// PHPPackages returns packages that install PHP-FPM version v with the
	// given extensions (e.g. "mysql", "curl").
	PHPPackages(v string, exts []string) []string
}

// Managed describes the layout of WhaCP-owned configuration.
type Managed struct {
	// NginxAvailable holds generated site files; NginxEnabled holds the
	// symlinks Nginx actually includes.
	NginxAvailable string `json:"nginx_available"`
	NginxEnabled   string `json:"nginx_enabled"`
	// PHPPoolDir returns the pool directory for a version (%s = version).
	PHPPoolDir string `json:"php_pool_dir"`
	// PHPSocketDir is where per-site FPM sockets are created.
	PHPSocketDir string `json:"php_socket_dir"`
	// PHPFPMUnit is the unit name pattern (%s = version).
	PHPFPMUnit string `json:"php_fpm_unit"`
	// PHPFPMBinary is the FPM binary command ID pattern (%s = version).
	PHPFPMBinary string   `json:"php_fpm_binary"`
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
}

// supportedUbuntu lists the Ubuntu releases WhaCP officially targets.
var supportedUbuntu = map[string]bool{"22.04": true, "24.04": true, "26.04": true}

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
	info.Supported = info.ID == "ubuntu" && supportedUbuntu[info.VersionID]
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
