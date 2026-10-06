package platform

import (
	"strconv"
	"strings"
)

// Locations used by other control panels that keep their own Nginx and PHP
// builds (aaPanel under /www/server, custom builds under /usr/local and /opt).
var (
	otherPanelNginx = []string{"/www/server/nginx/conf/nginx.conf", "/usr/local/nginx/conf/nginx.conf"}
	// includeRoots bound where Nginx "include" directives may be followed.
	// HestiaCP includes per-user vhosts from /home/<user>/conf/web.
	// Distribution packages keep their load_module stubs in
	// /usr/share/nginx: modules-available (Debian, linked from
	// /etc/nginx/modules-enabled) and modules (RHEL, included directly).
	includeRoots          = []string{"/etc/nginx", "/usr/share/nginx/modules-available", "/usr/share/nginx/modules", "/www/server", "/usr/local/nginx", "/usr/local/hestia", "/home", "/opt"}
	otherPanelPHPBinaries = []string{"/www/server/php/[0-9][0-9]/bin/php", "/opt/php[0-9][0-9]/bin/php"}
	otherPanelFPMBinaries = []string{"/www/server/php/[0-9][0-9]/sbin/php-fpm", "/opt/php[0-9][0-9]/sbin/php-fpm"}
	otherPanelPools       = []string{"/www/server/php/[0-9][0-9]/etc/php-fpm.conf", "/opt/php[0-9][0-9]/etc/php-fpm.d/*.conf"}
)

// debianFamily covers Ubuntu and Debian (and unknown systems as a fallback).
type debianFamily struct{ info OSInfo }

func (d debianFamily) Info() OSInfo { return d.info }

func (d debianFamily) Paths() Paths {
	return Paths{
		NginxMainConfig:     "/etc/nginx/nginx.conf",
		NginxMainCandidates: otherPanelNginx,
		NginxSitesAvail:     "/etc/nginx/sites-available",
		NginxSitesEnabled:   "/etc/nginx/sites-enabled",
		NginxConfD:          "/etc/nginx/conf.d",
		NginxIncludeRoots:   includeRoots,
		PHPBinaryGlobs:      append([]string{"/usr/bin/php[0-9].[0-9]", "/usr/bin/php[0-9].[0-9][0-9]"}, otherPanelPHPBinaries...),
		PHPFPMBinaryGlobs:   append([]string{"/usr/sbin/php-fpm[0-9].[0-9]", "/usr/sbin/php-fpm[0-9].[0-9][0-9]"}, otherPanelFPMBinaries...),
		PHPFPMPoolGlobs:     append([]string{"/etc/php/*/fpm/pool.d/*.conf"}, otherPanelPools...),
		PHPIniGlobs:         []string{"/etc/php/*/fpm/php.ini"},
		PHPFPMService:       "php%s-fpm.service",
		LetsEncryptDir:      "/etc/letsencrypt",
		MySQLConfigs:        []string{"/etc/mysql/my.cnf", "/etc/mysql/mariadb.conf.d/50-server.cnf", "/etc/mysql/mysql.conf.d/mysqld.cnf"},
		MySQLDataDir:        "/var/lib/mysql",
		RedisConfigs:        []string{"/etc/redis/redis.conf"},
		SystemCrontab:       "/etc/crontab",
		CronDDir:            "/etc/cron.d",
		UserCrontabDir:      "/var/spool/cron/crontabs",
		PasswdFile:          "/etc/passwd",
		UFWConfig:           "/etc/ufw/ufw.conf",
		SystemLogs: map[string]string{"auth": "/var/log/auth.log", "syslog": "/var/log/syslog", "mysql": "/var/log/mysql/error.log", "fail2ban": "/var/log/fail2ban.log",
			"ufw": "/var/log/ufw.log", "letsencrypt": "/var/log/letsencrypt/letsencrypt.log", "mail": "/var/log/mail.log"},
		PHPFPMLog:       "/var/log/php%s-fpm.log",
		SystemdUnitDirs: []string{"/etc/systemd/system", "/lib/systemd/system", "/usr/lib/systemd/system"},
	}
}

func (d debianFamily) CoreServices() []string {
	return []string{"nginx.service", "mariadb.service", "mysql.service", "redis-server.service", "cron.service", "ssh.service", "fail2ban.service", "ufw.service", "whacp-agent.service", "whacp-panel.service"}
}

func (d debianFamily) PHPFPMUnitPatterns() []string {
	return []string{"php*-fpm.service"}
}

func (d debianFamily) Managed() Managed {
	return Managed{
		NginxAvailable:  "/etc/nginx/sites-available",
		NginxEnabled:    "/etc/nginx/sites-enabled",
		PHPPoolDir:      "/etc/php/%s/fpm/pool.d",
		PHPSocketDir:    "/run/php",
		PHPFPMUnit:      "php%s-fpm.service",
		PHPFPMBinary:    "php-fpm%s",
		WebUser:         "www-data",
		NginxUnit:       "nginx.service",
		CronDir:         "/etc/cron.d",
		UnitDir:         "/etc/systemd/system",
		ACMERoot:        "/var/www/whacp-acme",
		SSHDropIn:       "/etc/ssh/sshd_config.d/00-whacp.conf",
		Fail2banJail:    "/etc/fail2ban/jail.d/whacp.local",
		MySQLUnit:       []string{"mariadb.service", "mysql.service"},
		RedisUnit:       "redis-server.service",
		RedisConfig:     "/etc/redis/redis.conf",
		MemcachedConfig: "/etc/memcached.conf",
		MemcachedUnit:   "memcached.service",
		FTPDir:          "/etc/whacp-ftp",
		SFTPDropIn:      "/etc/ssh/sshd_config.d/50-whacp-sftp.conf",
		SFTPKeysDir:     "/etc/ssh/whacp-keys",
		DNSConfFile:     "/etc/bind/named.conf.local",
		DNSZoneDir:      "/etc/bind/whacp",
		DNSUser:         "bind",
		DNSUnit:         "named.service",
		MailDir:         "/etc/whacp-mail",
		VmailDir:        "/var/vmail",
		PostfixDir:      "/etc/postfix",
		DovecotAuthConf: "/etc/dovecot/conf.d/10-auth.conf",
		DovecotDropIn:   "/etc/dovecot/conf.d/99-whacp.conf",
		ServerConfig: map[string]string{"nginx": "/etc/nginx", "php": "/etc/php", "mysql": "/etc/mysql", "redis": "/etc/redis",
			"letsencrypt": "/etc/letsencrypt", "fail2ban": "/etc/fail2ban", "cron": "/etc/cron.d", "firewall": "/etc/ufw"},
		WebmailDir:        "/etc/whacp-webmail",
		WebmailData:       "/var/lib/whacp-webmail",
		RoundcubeDir:      "/usr/share/roundcube",
		RoundcubePublic:   "/var/lib/roundcube/public_html",
		RoundcubeDefaults: "/etc/roundcube/defaults.inc.php",
	}
}

func (d debianFamily) PackageManager() string { return "apt" }

func (d debianFamily) PackageName(name string) (string, bool) { return name, true }

func (d debianFamily) PHPPackages(v string, exts []string) []string {
	pkgs := []string{"php" + v + "-fpm", "php" + v + "-cli"}
	for _, e := range exts {
		pkgs = append(pkgs, "php"+v+"-"+e)
	}
	return pkgs
}

// rhelFamily covers AlmaLinux, Rocky Linux and other RHEL derivatives.
// PHP versions come from Remi's repository (php83-php-fpm and so on,
// installed side by side); most other packages from the base repositories
// and EPEL.
type rhelFamily struct{ info OSInfo }

func (r rhelFamily) Info() OSInfo { return r.info }

func (r rhelFamily) Paths() Paths {
	return Paths{
		NginxMainConfig:     "/etc/nginx/nginx.conf",
		NginxMainCandidates: otherPanelNginx,
		NginxConfD:          "/etc/nginx/conf.d",
		NginxIncludeRoots:   includeRoots,
		PHPBinaryGlobs:      []string{"/usr/bin/php", "/opt/remi/php[0-9][0-9]/root/usr/bin/php"},
		PHPFPMBinaryGlobs:   []string{"/usr/sbin/php-fpm", "/opt/remi/php[0-9][0-9]/root/usr/sbin/php-fpm"},
		PHPFPMPoolGlobs:     []string{"/etc/php-fpm.d/*.conf", "/etc/opt/remi/php[0-9][0-9]/php-fpm.d/*.conf"},
		PHPIniGlobs:         []string{"/etc/opt/remi/php[0-9][0-9]/php.ini"},
		PHPFPMService:       "php%s-php-fpm.service",
		PHPCompact:          true,
		LetsEncryptDir:      "/etc/letsencrypt",
		MySQLConfigs:        []string{"/etc/my.cnf", "/etc/my.cnf.d/mariadb-server.cnf"},
		MySQLDataDir:        "/var/lib/mysql",
		RedisConfigs:        []string{"/etc/redis/redis.conf", "/etc/redis.conf", "/etc/valkey/valkey.conf"},
		SystemCrontab:       "/etc/crontab",
		CronDDir:            "/etc/cron.d",
		UserCrontabDir:      "/var/spool/cron",
		SystemLogs: map[string]string{"auth": "/var/log/secure", "syslog": "/var/log/messages", "mysql": "/var/log/mariadb/mariadb.log", "fail2ban": "/var/log/fail2ban.log",
			"letsencrypt": "/var/log/letsencrypt/letsencrypt.log", "mail": "/var/log/maillog"},
		PHPFPMLog:       "/var/opt/remi/php%s/log/php-fpm/error.log",
		PasswdFile:      "/etc/passwd",
		SystemdUnitDirs: []string{"/etc/systemd/system", "/usr/lib/systemd/system"},
	}
}

// valkey reports whether this release ships Valkey instead of Redis: RHEL
// 10 and its rebuilds have no redis package, Valkey (the Redis 7.2 fork,
// same protocol and configuration) replaces it with its own names.
func (r rhelFamily) valkey() bool {
	major, _, _ := strings.Cut(r.info.VersionID, ".")
	n, err := strconv.Atoi(major)
	return err == nil && n >= 10
}

func (r rhelFamily) redisUnit() string {
	if r.valkey() {
		return "valkey.service"
	}
	return "redis.service"
}

func (r rhelFamily) redisDir() string {
	if r.valkey() {
		return "/etc/valkey"
	}
	return "/etc/redis"
}

func (r rhelFamily) redisConfig() string {
	if r.valkey() {
		return "/etc/valkey/valkey.conf"
	}
	return "/etc/redis/redis.conf"
}

func (r rhelFamily) CoreServices() []string {
	return []string{"nginx.service", "mariadb.service", "mysqld.service", r.redisUnit(), "crond.service", "sshd.service", "fail2ban.service", "firewalld.service", "php-fpm.service", "whacp-agent.service", "whacp-panel.service"}
}

func (r rhelFamily) PHPFPMUnitPatterns() []string {
	return []string{"php-fpm.service", "php[0-9][0-9]-php-fpm.service"}
}

// Managed for RHEL: Nginx from the distribution, PHP versions from Remi.
func (r rhelFamily) Managed() Managed {
	return Managed{
		NginxAvailable: "/etc/nginx/whacp-available",
		NginxEnabled:   "/etc/nginx/conf.d",
		PHPPoolDir:     "/etc/opt/remi/php%s/php-fpm.d",
		PHPCompact:     true,
		PHPSocketDir:   "/run/php-fpm",
		// Only RHEL's own php-fpm package creates /run/php-fpm; Remi's
		// parallel versions do not.
		PHPSocketTmpfiles: "/etc/tmpfiles.d/whacp-php-fpm.conf",
		PHPFPMUnit:        "php%s-php-fpm.service",
		PHPFPMBinary:      "php-fpm%s",
		WebUser:           "nginx",
		NginxUnit:         "nginx.service",
		CronDir:           "/etc/cron.d",
		UnitDir:           "/etc/systemd/system",
		ACMERoot:          "/var/www/whacp-acme",
		SSHDropIn:         "/etc/ssh/sshd_config.d/00-whacp.conf",
		Fail2banJail:      "/etc/fail2ban/jail.d/whacp.local",
		MySQLUnit:         []string{"mariadb.service", "mysqld.service"},
		RedisUnit:         r.redisUnit(),
		RedisConfig:       r.redisConfig(),
		MemcachedConfig:   "/etc/sysconfig/memcached",
		MemcachedUnit:     "memcached.service",
		FTPDir:            "/etc/whacp-ftp",
		SFTPDropIn:        "/etc/ssh/sshd_config.d/50-whacp-sftp.conf",
		SFTPKeysDir:       "/etc/ssh/whacp-keys",
		DNSConfFile:       "/etc/named.conf",
		DNSZoneDir:        "/var/named/whacp",
		DNSUser:           "named",
		DNSUnit:           "named.service",
		MailDir:           "/etc/whacp-mail",
		VmailDir:          "/var/vmail",
		PostfixDir:        "/etc/postfix",
		DovecotAuthConf:   "/etc/dovecot/conf.d/10-auth.conf",
		DovecotDropIn:     "/etc/dovecot/conf.d/99-whacp.conf",
		ServerConfig: map[string]string{"nginx": "/etc/nginx", "php": "/etc/opt/remi", "mysql": "/etc/my.cnf.d", "redis": r.redisDir(),
			"letsencrypt": "/etc/letsencrypt", "fail2ban": "/etc/fail2ban", "cron": "/etc/cron.d", "firewall": "/etc/firewalld"},
		WebmailDir:        "/etc/whacp-webmail",
		WebmailData:       "/var/lib/whacp-webmail",
		RoundcubeDir:      "/usr/share/roundcubemail",
		RoundcubePublic:   "/usr/share/roundcubemail/public_html",
		RoundcubeDefaults: "/etc/roundcubemail/defaults.inc.php",
	}
}

func (r rhelFamily) PackageManager() string { return "dnf" }

// rhelPackages maps Debian package names to AlmaLinux/Rocky/RHEL ones
// (base repositories, EPEL and, for PHP, Remi). Names not listed are the
// same on both; "" means there is no equivalent.
var rhelPackages = map[string]string{
	"redis-server":        "redis",
	"mariadb-client":      "mariadb",
	"bind9":               "bind",
	"bind9utils":          "bind-utils",
	"dovecot-imapd":       "dovecot",
	"dovecot-lmtpd":       "dovecot",
	"dovecot-sieve":       "dovecot-pigeonhole",
	"ufw":                 "firewalld",
	"python3-venv":        "python3",
	"unattended-upgrades": "dnf-automatic",
	"imagemagick":         "ImageMagick",
	"libnginx-mod-brotli": "",
	// No Nginx ModSecurity connector in BaseOS, AppStream or EPEL: an
	// administrator may install one from a third-party repository, which
	// WhaCP then detects.
	"libnginx-mod-http-modsecurity": "",
	"modsecurity-crs":               "",
	"roundcube-core":                "roundcubemail",
	"clamav-freshclam":              "clamav-freshclam", // EPEL 9+ (clamav-update before)
	// EPEL's build (no patent-encumbered codecs); RPM Fusion's is not used.
	"ffmpeg":            "ffmpeg-free",
	"roundcube-sqlite3": "",
	"roundcube-plugins": "",
	"php-fpm":           "",
	"php-sqlite3":       "",
	"php-intl":          "",
	"php-mbstring":      "",
	"php-xml":           "",
}

func (r rhelFamily) PackageName(name string) (string, bool) {
	if name == "redis-server" && r.valkey() {
		return "valkey", true
	}
	if p, ok := rhelPackages[name]; ok {
		return p, p != ""
	}
	return name, true
}

func (r rhelFamily) PHPPackages(v string, exts []string) []string {
	n := strings.ReplaceAll(v, ".", "")
	pkgs := []string{"php" + n + "-php-fpm", "php" + n + "-php-cli"}
	for _, e := range exts {
		pkgs = append(pkgs, "php"+n+"-php-"+e)
	}
	return pkgs
}
