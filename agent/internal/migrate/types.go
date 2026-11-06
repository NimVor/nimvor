// Package migrate moves websites from another server into NimVor.
//
// The agent reaches the source server over SSH only (host key pinned, key
// or password login; the password goes to ssh through a one-shot
// SSH_ASKPASS helper, never on a command line), detects what panel it runs,
// discovers its accounts, domains, databases and mailboxes, checks that the
// move is possible, and then runs a checkpointed list of tasks (files by
// rsync, MySQL/MariaDB databases by dump and import) that survives an agent
// restart: the state is a file without any credential, and a stopped
// migration is continued by giving the credentials again.
//
// Nothing is ever deleted on the source. What the migration does on this
// server is limited to the website folders and databases it is told to fill.
package migrate

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// Support is how far a source panel's adapter has been proven.
type Support string

const (
	// Experimental: written from the panel's documented file layout and
	// tested only against synthetic data, not against a real server.
	Experimental Support = "experimental"
	// Partial: proven against a real server for part of what it discovers.
	Partial Support = "partial"
	// Full: every listed capability proven against real servers.
	Full Support = "full"
)

// Source is how to reach the source server. It is held in memory for one
// request; it is never written to the state file or to a log.
type Source struct {
	Panel      string `json:"panel"` // adapter id, "" = detect
	Host       string `json:"host"`
	Port       int    `json:"port"`
	User       string `json:"user"`
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"`
	HostKey    string `json:"host_key"` // pinned "ssh-ed25519 AAAA…"
}

var (
	hostRe    = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	userRe    = regexp.MustCompile(`^[A-Za-z0-9._@+-]{1,100}$`)
	hostKeyRe = regexp.MustCompile(`^(ssh-ed25519|ecdsa-sha2-nistp(256|384|521)|ssh-rsa) [A-Za-z0-9+/]{40,}={0,2}$`)
)

// Validate checks the address and the login. needKey requires a pinned
// host key (everything but the first contact).
func (s *Source) Validate(needKey bool) error {
	h := strings.Trim(s.Host, "[]")
	if !hostRe.MatchString(h) && net.ParseIP(h) == nil {
		return fmt.Errorf("host must be a host name or an IP address")
	}
	s.Host = h
	if s.Port == 0 {
		s.Port = 22
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("port must be 1–65535")
	}
	if !userRe.MatchString(s.User) {
		return fmt.Errorf("user name may contain letters, digits and . _ @ + -")
	}
	if strings.ContainsAny(s.Password, "\r\n\x00") || len(s.Password) > 500 {
		return fmt.Errorf("the password cannot be used")
	}
	if s.PrivateKey != "" {
		if !strings.Contains(s.PrivateKey, "PRIVATE KEY-----") || len(s.PrivateKey) > 16384 || strings.Contains(s.PrivateKey, "ENCRYPTED") {
			return fmt.Errorf("the private key must be an unencrypted key in OpenSSH or PEM format")
		}
	}
	if s.Password == "" && s.PrivateKey == "" {
		return fmt.Errorf("a password or a private key is required")
	}
	if s.HostKey != "" && (len(s.HostKey) > 2100 || !hostKeyRe.MatchString(s.HostKey)) {
		return fmt.Errorf("host key must be an SSH public key (type and base64)")
	}
	if needKey && s.HostKey == "" {
		return fmt.Errorf("the source server's SSH host key must be confirmed first")
	}
	return nil
}

// ServerInfo is what is learned about the source server.
type ServerInfo struct {
	Hostname  string            `json:"hostname"`
	OS        string            `json:"os"`
	Root      bool              `json:"root"` // the login is root
	PHP       []string          `json:"php"`  // versions found, e.g. "8.1"
	MySQL     string            `json:"mysql"`
	Engine    string            `json:"engine"` // mysql, mariadb or ""
	Disk      []Disk            `json:"disk"`
	Services  map[string]string `json:"services"` // name → active|inactive
	Rsync     bool              `json:"rsync"`    // rsync exists on the source
	Mysqldump bool              `json:"mysqldump"`
}

// Disk is the free space of one mount of the source.
type Disk struct {
	Mount  string `json:"mount"`
	FreeKB int64  `json:"free_kb"`
	UsedKB int64  `json:"used_kb"`
}

// Detection is a panel adapter's answer about a source server.
type Detection struct {
	Panel   string  `json:"panel"`
	Name    string  `json:"name"`
	Version string  `json:"version"`
	Support Support `json:"support"`
}

// Domain is a website of the source.
type Domain struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"` // main, sub, addon, parked, site
	DocRoot string `json:"doc_root"`
	SizeKB  int64  `json:"size_kb"` // -1 unknown
}

// Database is a database of the source.
type Database struct {
	Name   string `json:"name"`
	Engine string `json:"engine"`
	SizeKB int64  `json:"size_kb"`
}

// EmailAccount is a mailbox of the source. Its messages can be moved into an existing NimVor mailbox.
type EmailAccount struct {
	Address string `json:"address"`
	Maildir string `json:"maildir"`
	SizeKB  int64  `json:"size_kb"` // -1 when it could not be measured
}

// CronJob is a scheduled command of the source (discovered; not moved by this version).
type CronJob struct {
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
}

// Account is one hosting account of the source.
type Account struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Home      string         `json:"home"`
	PHP       string         `json:"php,omitempty"`
	Domains   []Domain       `json:"domains"`
	Databases []Database     `json:"databases"`
	Email     []EmailAccount `json:"email"`
	Cron      []CronJob      `json:"cron"`
}

// Discovery is everything found on a source.
type Discovery struct {
	Panel    Detection  `json:"panel"`
	Server   ServerInfo `json:"server"`
	Accounts []Account  `json:"accounts"`
	Warnings []string   `json:"warnings"`
	// NotMigrated lists what was discovered (or exists) but this version of the
	// agent does not move: it is shown so nobody assumes it was.
	NotMigrated []string `json:"not_migrated"`
}
