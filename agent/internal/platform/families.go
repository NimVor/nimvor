package platform

// debianFamily covers Ubuntu and Debian (and unknown systems as a fallback).
type debianFamily struct{ info OSInfo }

func (d debianFamily) Info() OSInfo { return d.info }

func (d debianFamily) Paths() Paths {
	return Paths{
		NginxMainConfig:   "/etc/nginx/nginx.conf",
		NginxSitesAvail:   "/etc/nginx/sites-available",
		NginxSitesEnabled: "/etc/nginx/sites-enabled",
		NginxConfD:        "/etc/nginx/conf.d",
		NginxIncludeRoots: []string{"/etc/nginx"},
		PHPBinaryGlobs:    []string{"/usr/bin/php[0-9].[0-9]", "/usr/bin/php[0-9].[0-9][0-9]"},
		PHPFPMBinaryGlobs: []string{"/usr/sbin/php-fpm[0-9].[0-9]", "/usr/sbin/php-fpm[0-9].[0-9][0-9]"},
		PHPFPMPoolGlobs:   []string{"/etc/php/*/fpm/pool.d/*.conf"},
		PHPIniGlobs:       []string{"/etc/php/*/fpm/php.ini"},
		LetsEncryptDir:    "/etc/letsencrypt",
		MySQLConfigs:      []string{"/etc/mysql/my.cnf", "/etc/mysql/mariadb.conf.d/50-server.cnf", "/etc/mysql/mysql.conf.d/mysqld.cnf"},
		MySQLDataDir:      "/var/lib/mysql",
		RedisConfigs:      []string{"/etc/redis/redis.conf"},
		SystemCrontab:     "/etc/crontab",
		CronDDir:          "/etc/cron.d",
		UserCrontabDir:    "/var/spool/cron/crontabs",
		PasswdFile:        "/etc/passwd",
		UFWConfig:         "/etc/ufw/ufw.conf",
		SystemdUnitDirs:   []string{"/etc/systemd/system", "/lib/systemd/system", "/usr/lib/systemd/system"},
	}
}

func (d debianFamily) CoreServices() []string {
	return []string{"nginx.service", "mariadb.service", "mysql.service", "redis-server.service", "cron.service", "ssh.service", "fail2ban.service", "ufw.service", "whacp-agent.service", "whacp-panel.service"}
}

func (d debianFamily) PHPFPMUnitPatterns() []string {
	return []string{"php*-fpm.service"}
}

// rhelFamily covers AlmaLinux, Rocky Linux and other RHEL derivatives.
// It is a scaffold: paths are defined, but the family is not yet supported.
type rhelFamily struct{ info OSInfo }

func (r rhelFamily) Info() OSInfo { return r.info }

func (r rhelFamily) Paths() Paths {
	return Paths{
		NginxMainConfig:   "/etc/nginx/nginx.conf",
		NginxConfD:        "/etc/nginx/conf.d",
		NginxIncludeRoots: []string{"/etc/nginx"},
		PHPBinaryGlobs:    []string{"/usr/bin/php", "/opt/remi/php[0-9][0-9]/root/usr/bin/php"},
		PHPFPMBinaryGlobs: []string{"/usr/sbin/php-fpm", "/opt/remi/php[0-9][0-9]/root/usr/sbin/php-fpm"},
		PHPFPMPoolGlobs:   []string{"/etc/php-fpm.d/*.conf", "/etc/opt/remi/php[0-9][0-9]/php-fpm.d/*.conf"},
		PHPIniGlobs:       []string{"/etc/php.ini"},
		LetsEncryptDir:    "/etc/letsencrypt",
		MySQLConfigs:      []string{"/etc/my.cnf", "/etc/my.cnf.d/mariadb-server.cnf"},
		MySQLDataDir:      "/var/lib/mysql",
		RedisConfigs:      []string{"/etc/redis/redis.conf", "/etc/redis.conf"},
		SystemCrontab:     "/etc/crontab",
		CronDDir:          "/etc/cron.d",
		UserCrontabDir:    "/var/spool/cron",
		PasswdFile:        "/etc/passwd",
		SystemdUnitDirs:   []string{"/etc/systemd/system", "/usr/lib/systemd/system"},
	}
}

func (r rhelFamily) CoreServices() []string {
	return []string{"nginx.service", "mariadb.service", "mysqld.service", "redis.service", "crond.service", "sshd.service", "fail2ban.service", "firewalld.service", "php-fpm.service", "whacp-agent.service", "whacp-panel.service"}
}

func (r rhelFamily) PHPFPMUnitPatterns() []string {
	return []string{"php-fpm.service", "php[0-9][0-9]-php-fpm.service"}
}
