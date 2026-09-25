package mailmgr

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// DKIMUnit runs OpenDKIM with WhaCP's configuration.
const DKIMUnit = "whacp-opendkim.service"

// dkimPort is where OpenDKIM listens for Postfix (loopback only).
const dkimPort = 8891

// tls names the certificate files Postfix and Dovecot use.
type tls struct{ cert, key string }

func renderDovecot(dir, vmail string, vmailUID int, hostname string, t tls, ipv6 bool) string {
	listen := "*, ::"
	if !ipv6 {
		listen = "*"
	}
	return fmt.Sprintf(`# Managed by WhaCP (email). Do not edit: changes are overwritten.
protocols = imap lmtp
listen = %[7]s
mail_location = maildir:~/Maildir
mail_uid = vmail
mail_gid = vmail
first_valid_uid = %[3]d
last_valid_uid = %[3]d
disable_plaintext_auth = yes
auth_mechanisms = plain login
ssl = required
ssl_cert = <%[4]s
ssl_key = <%[5]s
ssl_min_protocol = TLSv1.2

passdb {
  driver = passwd-file
  args = scheme=SHA512-CRYPT username_format=%%Lu %[1]s/users
}
userdb {
  driver = passwd-file
  args = username_format=%%Lu %[1]s/users
  default_fields = uid=vmail gid=vmail home=%[2]s/%%Ld/%%Ln
}

mail_plugins = $mail_plugins quota
protocol imap {
  mail_plugins = $mail_plugins imap_quota
}
protocol lmtp {
  postmaster_address = postmaster@%[6]s
}
plugin {
  quota = maildir:User quota
  quota_rule = *:storage=0
  quota_grace = 10%%%%
}

namespace inbox {
  inbox = yes
  mailbox Drafts {
    auto = subscribe
    special_use = \Drafts
  }
  mailbox Sent {
    auto = subscribe
    special_use = \Sent
  }
  mailbox Junk {
    auto = subscribe
    special_use = \Junk
  }
  mailbox Trash {
    auto = subscribe
    special_use = \Trash
  }
}

# IMAP only over TLS (993); plain 143 is closed.
service imap-login {
  inet_listener imap {
    port = 0
  }
}
service lmtp {
  unix_listener /var/spool/postfix/private/dovecot-lmtp {
    mode = 0600
    user = postfix
    group = postfix
  }
}
service auth {
  unix_listener /var/spool/postfix/private/auth {
    mode = 0660
    user = postfix
    group = postfix
  }
}
`, dir, vmail, vmailUID, t.cert, t.key, hostname, listen)
}

// hasIPv6 reports whether this machine can listen on IPv6.
func hasIPv6() bool {
	l, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// postfixSettings are applied with postconf -e.
func postfixSettings(dir, hostname string, t tls, ipv6 bool) [][2]string {
	// Only this machine may relay without signing in (applications here).
	nets := "127.0.0.0/8"
	if ipv6 {
		nets += " [::1]/128"
	}
	return [][2]string{
		{"myhostname", hostname},
		{"mydestination", "localhost"},
		{"mynetworks", nets},
		{"virtual_mailbox_domains", "texthash:" + dir + "/domains"},
		{"virtual_mailbox_maps", "texthash:" + dir + "/mailboxes"},
		{"virtual_alias_maps", "texthash:" + dir + "/aliases"},
		{"virtual_transport", "lmtp:unix:private/dovecot-lmtp"},
		{"smtpd_sender_login_maps", "texthash:" + dir + "/senders"},
		{"smtpd_tls_cert_file", t.cert},
		{"smtpd_tls_key_file", t.key},
		{"smtpd_tls_security_level", "may"},
		{"smtpd_tls_mandatory_protocols", ">=TLSv1.2"},
		{"smtp_tls_security_level", "may"},
		{"smtpd_sasl_type", "dovecot"},
		{"smtpd_sasl_path", "private/auth"},
		{"smtpd_sasl_auth_enable", "no"},
		{"smtpd_helo_required", "yes"},
		{"disable_vrfy_command", "yes"},
		{"smtpd_relay_restrictions", "permit_mynetworks, permit_sasl_authenticated, defer_unauth_destination"},
		{"message_size_limit", "52428800"},
		{"milter_default_action", "accept"},
		{"milter_protocol", "6"},
		{"smtpd_milters", fmt.Sprintf("inet:127.0.0.1:%d", dkimPort)},
		{"non_smtpd_milters", fmt.Sprintf("inet:127.0.0.1:%d", dkimPort)},
	}
}

// submission is the authenticated mail submission service (port 587).
var submission = [][2]string{
	{"syslog_name", "postfix/submission"},
	{"smtpd_tls_security_level", "encrypt"},
	{"smtpd_tls_auth_only", "yes"},
	{"smtpd_sasl_auth_enable", "yes"},
	{"smtpd_client_restrictions", "permit_sasl_authenticated,reject"},
	{"smtpd_sender_restrictions", "reject_sender_login_mismatch"},
	{"smtpd_relay_restrictions", "permit_sasl_authenticated,reject"},
	{"milter_macro_daemon_name", "ORIGINATING"},
}

func renderDKIMConf(dir string) string {
	return fmt.Sprintf(`# Managed by WhaCP (email). Do not edit: changes are overwritten.
Syslog                  yes
Canonicalization        relaxed/simple
OversignHeaders         From
Mode                    sv
UserID                  opendkim
UMask                   007
Socket                  inet:%d@127.0.0.1
KeyTable                refile:%[2]s/dkim/KeyTable
SigningTable            refile:%[2]s/dkim/SigningTable
InternalHosts           refile:%[2]s/dkim/TrustedHosts
ExternalIgnoreList      refile:%[2]s/dkim/TrustedHosts
`, dkimPort, dir)
}

func renderDKIMUnit(dir string) string {
	return "# Managed by WhaCP (email).\n[Unit]\nDescription=WhaCP DKIM signing (OpenDKIM)\nAfter=network.target\nBefore=postfix.service\n\n" +
		"[Service]\nType=simple\nExecStart=/usr/sbin/opendkim -f -x " + dir + "/opendkim.conf\nExecReload=/bin/kill -USR1 $MAINPID\nRestart=on-failure\nRestartSec=3\n\n" +
		"[Install]\nWantedBy=multi-user.target\n"
}

var systemAuthRe = regexp.MustCompile(`(?m)^(\s*)!include auth-system\.conf\.ext\s*$`)

// disableSystemAuth comments out Dovecot's system-account login (PAM), so
// only WhaCP's mailboxes can sign in to IMAP.
func disableSystemAuth(conf string) (string, bool) {
	if !systemAuthRe.MatchString(conf) {
		return conf, false
	}
	return systemAuthRe.ReplaceAllString(conf, "${1}# Disabled by WhaCP: only WhaCP mailboxes may sign in.\n${1}#!include auth-system.conf.ext"), true
}

// configured reports whether Postfix points at WhaCP's tables.
func configuredFrom(postconfN, dir string) bool {
	return strings.Contains(postconfN, "texthash:"+dir+"/domains")
}
