package platform

import "strings"

// Locations used by other control panels that keep their own Nginx and PHP
// builds (aaPanel under /www/server, custom builds under /usr/local and /opt).
var (
	otherPanelNginx = []string{"/www/server/nginx/conf/nginx.conf", "/usr/local/nginx/conf/nginx.conf"}
	// includeRoots bound where Nginx "include" directives may be followed.
	// HestiaCP includes per-user vhosts from /home/<user>/conf/web.
	includeRoots          = []string{"/etc/nginx", "/www/server", "/usr/local/nginx", "/usr/local/hestia", "/home", "/opt"}
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
		LetsEncryptDir:      "/etc/letsencrypt",
		MySQLConfigs:        []string{"/etc/mysql/my.cnf", "/etc/mysql/mariadb.conf.d/50-server.cnf", "/etc/mysql/mysql.conf.d/mysqld.cnf"},
		MySQLDataDir:        "/var/lib/mysql",
		RedisConfigs:        []string{"/etc/redis/redis.conf"},
		SystemCrontab:       "/etc/crontab",
		CronDDir:            "/etc/cron.d",
		UserCrontabDir:      "/var/spool/cron/crontabs",
		PasswdFile:          "/etc/passwd",
		UFWConfig:           "/etc/ufw/ufw.conf",
		SystemdUnitDirs:     []string{"/etc/systemd/system", "/lib/systemd/system", "/usr/lib/systemd/system"},
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
	}
}

func (d debianFamily) PHPPackages(v string, exts []string) []string {
	pkgs := []string{"php" + v + "-fpm", "php" + v + "-cli"}
	for _, e := range exts {
		pkgs = append(pkgs, "php"+v+"-"+e)
	}
	return pkgs
}

// rhelFamily covers AlmaLinux, Rocky Linux and other RHEL derivatives.
// It is a scaffold: paths are defined, but the family is not yet supported.
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
		PHPIniGlobs:         []string{"/etc/php.ini"},
		LetsEncryptDir:      "/etc/letsencrypt",
		MySQLConfigs:        []string{"/etc/my.cnf", "/etc/my.cnf.d/mariadb-server.cnf"},
		MySQLDataDir:        "/var/lib/mysql",
		RedisConfigs:        []string{"/etc/redis/redis.conf", "/etc/redis.conf"},
		SystemCrontab:       "/etc/crontab",
		CronDDir:            "/etc/cron.d",
		UserCrontabDir:      "/var/spool/cron",
		PasswdFile:          "/etc/passwd",
		SystemdUnitDirs:     []string{"/etc/systemd/system", "/usr/lib/systemd/system"},
	}
}

func (r rhelFamily) CoreServices() []string {
	return []string{"nginx.service", "mariadb.service", "mysqld.service", "redis.service", "crond.service", "sshd.service", "fail2ban.service", "firewalld.service", "php-fpm.service", "whacp-agent.service", "whacp-panel.service"}
}

func (r rhelFamily) PHPFPMUnitPatterns() []string {
	return []string{"php-fpm.service", "php[0-9][0-9]-php-fpm.service"}
}

// Managed for RHEL is a scaffold (the family is not yet supported).
func (r rhelFamily) Managed() Managed {
	return Managed{
		NginxAvailable:  "/etc/nginx/whacp-available",
		NginxEnabled:    "/etc/nginx/conf.d",
		PHPPoolDir:      "/etc/opt/remi/php%s/php-fpm.d",
		PHPSocketDir:    "/run/php-fpm",
		PHPFPMUnit:      "php%s-php-fpm.service",
		PHPFPMBinary:    "php-fpm%s",
		WebUser:         "nginx",
		NginxUnit:       "nginx.service",
		CronDir:         "/etc/cron.d",
		UnitDir:         "/etc/systemd/system",
		ACMERoot:        "/var/www/whacp-acme",
		SSHDropIn:       "/etc/ssh/sshd_config.d/00-whacp.conf",
		Fail2banJail:    "/etc/fail2ban/jail.d/whacp.local",
		MySQLUnit:       []string{"mariadb.service", "mysqld.service"},
		RedisUnit:       "redis.service",
		RedisConfig:     "/etc/redis/redis.conf",
		MemcachedConfig: "/etc/sysconfig/memcached",
		MemcachedUnit:   "memcached.service",
		FTPDir:          "/etc/whacp-ftp",
		SFTPDropIn:      "/etc/ssh/sshd_config.d/50-whacp-sftp.conf",
		SFTPKeysDir:     "/etc/ssh/whacp-keys",
		DNSConfFile:     "/etc/named.conf",
		DNSZoneDir:      "/var/named/whacp",
		DNSUser:         "named",
		DNSUnit:         "named.service",
		MailDir:         "/etc/whacp-mail",
		VmailDir:        "/var/vmail",
		PostfixDir:      "/etc/postfix",
		DovecotAuthConf: "/etc/dovecot/conf.d/10-auth.conf",
		DovecotDropIn:   "/etc/dovecot/conf.d/99-whacp.conf",
	}
}

func (r rhelFamily) PHPPackages(v string, exts []string) []string {
	n := strings.ReplaceAll(v, ".", "")
	pkgs := []string{"php" + n + "-php-fpm", "php" + n + "-php-cli"}
	for _, e := range exts {
		pkgs = append(pkgs, "php"+n+"-php-"+e)
	}
	return pkgs
}
