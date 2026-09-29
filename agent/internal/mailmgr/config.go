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

// dovecotParams are the values WhaCP's Dovecot drop-in needs.
type dovecotParams struct {
	Dir, Vmail, Hostname string
	VmailUID             int
	TLS                  tls
	IPv6                 bool
	// V24 selects Dovecot 2.4's configuration syntax (2.3 otherwise).
	V24 bool
}

// SpamSieve files messages the spam filter marked into Junk; it runs before
// any personal filters.
const SpamSieve = `require ["fileinto", "mailbox"];
if header :contains "X-Spam" "Yes" {
  fileinto :create "Junk";
  stop;
}
`

// RenderSpamSieve is SpamSieve, except for domains whose accounts keep
// marked mail in the inbox (Spam Box off). The domains are validated.
func RenderSpamSieve(noJunk []string) string {
	if len(noJunk) == 0 {
		return SpamSieve
	}
	quoted := make([]string, len(noJunk))
	for i, d := range noJunk {
		quoted[i] = `"` + d + `"`
	}
	return `require ["fileinto", "mailbox", "envelope"];
if allof (header :contains "X-Spam" "Yes", not envelope :domain :is "to" [` + strings.Join(quoted, ", ") + `]) {
  fileinto :create "Junk";
  stop;
}
`
}

func renderDovecot(p dovecotParams) string {
	listen := "*, ::"
	if !p.IPv6 {
		listen = "*"
	}
	if p.V24 {
		return renderDovecot24(p, listen)
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
  mail_plugins = $mail_plugins sieve
}
plugin {
  quota = maildir:User quota
  quota_rule = *:storage=0
  quota_grace = 10%%%%
  sieve_before = %[1]s/sieve/spam.sieve
}
%[8]s`, p.Dir, p.Vmail, p.VmailUID, p.TLS.cert, p.TLS.key, p.Hostname, listen, dovecotCommon())
}

func renderDovecot24(p dovecotParams, listen string) string {
	return fmt.Sprintf(`# Managed by WhaCP (email). Do not edit: changes are overwritten.
protocols = imap lmtp
listen = %[6]s
mail_driver = maildir
mail_path = ~/Maildir
mail_inbox_path = ~/Maildir
mail_uid = vmail
mail_gid = vmail
first_valid_uid = %[2]d
last_valid_uid = %[2]d
auth_allow_cleartext = no
auth_mechanisms = plain login
ssl = required
ssl_server_cert_file = %[3]s
ssl_server_key_file = %[4]s
ssl_min_protocol = TLSv1.2

passdb passwd-file {
  passwd_file_path = %[1]s/users
  default_password_scheme = SHA512-CRYPT
  auth_username_format = %%{user | lower}
}
userdb passwd-file {
  passwd_file_path = %[1]s/users
  auth_username_format = %%{user | lower}
}

mail_plugins {
  quota = yes
}
protocol imap {
  mail_plugins {
    imap_quota = yes
  }
}
protocol lmtp {
  postmaster_address = postmaster@%[5]s
  # The distribution's default keeps only the local part (system users).
  auth_username_format = %%{user | lower}
  mail_plugins {
    sieve = yes
  }
}
quota "User quota" {
  driver = count
}
sieve_script whacp-spam {
  type = before
  path = %[1]s/sieve/spam.sieve
}
%[7]s`, p.Dir, p.VmailUID, p.TLS.cert, p.TLS.key, p.Hostname, listen, dovecotCommon())
}

// dovecotCommon holds the parts both versions write the same way.
func dovecotCommon() string {
	return `
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
`
}

// dovecotV24 reports whether "dovecot --version" output is 2.4 or later.
func dovecotV24(version string) bool {
	v := strings.TrimSpace(version)
	return !strings.HasPrefix(v, "2.3.") && !strings.HasPrefix(v, "2.2.")
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
func postfixSettings(dir, hostname string, t tls, ipv6 bool, spam bool) [][2]string {
	// Only this machine may relay without signing in (applications here).
	nets := "127.0.0.0/8"
	if ipv6 {
		nets += " [::1]/128"
	}
	return [][2]string{
		{"myhostname", hostname},
		// RHEL's default is localhost only.
		{"inet_interfaces", "all"},
		{"mydestination", "localhost"},
		{"mynetworks", nets},
		{"virtual_mailbox_domains", "texthash:" + dir + "/domains"},
		// Backup MX domains (queued for their primary MX).
		{"relay_domains", "texthash:" + dir + "/relay_domains"},
		{"virtual_mailbox_maps", "texthash:" + dir + "/mailboxes"},
		{"virtual_alias_maps", "texthash:" + dir + "/aliases"},
		{"virtual_transport", "lmtp:unix:private/dovecot-lmtp"},
		{"smtpd_sender_login_maps", "texthash:" + dir + "/senders"},
		{"smtpd_tls_cert_file", t.cert},
		{"smtpd_tls_key_file", t.key},
		{"smtpd_tls_security_level", "may"},
		// TLS 1.2 or later. The ">=TLSv1.2" form needs Postfix 3.6; RHEL 9
		// ships 3.5, which then disables TLS entirely.
		{"smtpd_tls_mandatory_protocols", "!SSLv2, !SSLv3, !TLSv1, !TLSv1.1"},
		{"smtp_tls_security_level", "may"},
		{"smtpd_sasl_type", "dovecot"},
		{"smtpd_sasl_path", "private/auth"},
		{"smtpd_sasl_auth_enable", "no"},
		{"smtpd_helo_required", "yes"},
		{"disable_vrfy_command", "yes"},
		// The pseudo-user "nobody" may not send mail with sendmail.
		{"authorized_submit_users", "!nobody, static:anyone"},
		{"smtpd_relay_restrictions", "permit_mynetworks, permit_sasl_authenticated, defer_unauth_destination"},
		{"message_size_limit", "52428800"},
		{"milter_default_action", "accept"},
		{"milter_protocol", "6"},
		{"smtpd_milters", smtpdMilters(spam)},
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

// smtpdMilters lists the milters for received mail: the spam filter first
// (so it can refuse), then DKIM signing and checking.
func smtpdMilters(spam bool) string {
	dkim := fmt.Sprintf("inet:127.0.0.1:%d", dkimPort)
	if spam {
		return fmt.Sprintf("inet:127.0.0.1:%d, %s", rspamdPort, dkim)
	}
	return dkim
}
