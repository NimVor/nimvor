// Package ftpmgr manages FTP(S) and SFTP access to websites.
//
// FTP runs pure-ftpd from its own systemd unit (nimvor-ftpd.service) with a
// configuration NimVor owns (in FTPDir); the distribution's pure-ftpd
// configuration is never edited. Accounts are pure-ftpd virtual users: each
// maps to a website's system user and is chrooted to the website folder or
// a sub-folder of it. TLS is required unless explicitly relaxed.
//
// SFTP lets a website's own system user in through OpenSSH, confined with
// ChrootDirectory and internal-sftp (see sftp.go).
package ftpmgr

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/users"
)

// Unit is NimVor's FTP service.
const Unit = "nimvor-ftpd.service"

// Other FTP servers that would hold port 21.
var otherFTPUnits = []string{"pure-ftpd.service", "vsftpd.service", "proftpd.service"}

var mu sync.Mutex

// Settings are the server options NimVor manages.
type Settings struct {
	PassiveMin int    `json:"passive_min"`
	PassiveMax int    `json:"passive_max"`
	PassiveIP  string `json:"passive_ip,omitempty"`
	MaxClients int    `json:"max_clients"`
	MaxPerIP   int    `json:"max_per_ip"`
	// TLS: "required" (default) or "optional" (plain FTP also allowed).
	TLS string `json:"tls"`
}

// Defaults returns the default settings.
func Defaults() Settings {
	return Settings{PassiveMin: 40110, PassiveMax: 40210, MaxClients: 50, MaxPerIP: 8, TLS: "required"}
}

func (s *Settings) validate() error {
	if s.PassiveMin < 1024 || s.PassiveMax > 65535 || s.PassiveMin > s.PassiveMax || s.PassiveMax-s.PassiveMin > 5000 {
		return fmt.Errorf("the passive port range must be within 1024–65535 and at most 5000 ports wide")
	}
	if s.PassiveIP != "" {
		if ip := net.ParseIP(s.PassiveIP); ip == nil || ip.To4() == nil {
			return fmt.Errorf("passive_ip must be an IPv4 address")
		}
	}
	if s.MaxClients < 1 || s.MaxClients > 1000 || s.MaxPerIP < 1 || s.MaxPerIP > s.MaxClients {
		return fmt.Errorf("max_clients must be 1–1000 and max_per_ip 1–max_clients")
	}
	switch s.TLS {
	case "":
		s.TLS = "required"
	case "required", "optional":
	default:
		return fmt.Errorf("tls must be required or optional")
	}
	return nil
}

// paths of NimVor's FTP files.
type paths struct{ dir, conf, passwd, pdb, cert, unit string }

func ftpPaths(env *host.Env) paths {
	d := env.M.FTPDir
	return paths{dir: d, conf: d + "/pure-ftpd.conf", passwd: d + "/accounts.passwd", pdb: d + "/accounts.pdb", cert: d + "/pure-ftpd.pem", unit: filepath.Join(env.M.UnitDir, Unit)}
}

// Render produces pure-ftpd's configuration.
func (s Settings) Render(p paths) string {
	tls := "2"
	if s.TLS == "optional" {
		tls = "1"
	}
	var b strings.Builder
	b.WriteString("# Managed by NimVor (FTP). Do not edit: changes are overwritten.\n")
	for _, kv := range [][2]string{
		{"ChrootEveryone", "yes"}, {"Daemonize", "no"}, {"NoAnonymous", "yes"}, {"AnonymousOnly", "no"},
		{"PAMAuthentication", "no"}, {"UnixAuthentication", "no"}, {"PureDB", p.pdb},
		{"MinUID", "1000"}, {"DontResolve", "yes"}, {"MaxIdleTime", "15"},
		{"MaxClientsNumber", strconv.Itoa(s.MaxClients)}, {"MaxClientsPerIP", strconv.Itoa(s.MaxPerIP)},
		{"PassivePortRange", fmt.Sprintf("%d %d", s.PassiveMin, s.PassiveMax)},
		{"LimitRecursion", "10000 8"}, {"AllowUserFXP", "no"}, {"AllowAnonymousFXP", "no"},
		{"DisplayDotFiles", "yes"}, {"ProhibitDotFilesWrite", "no"}, {"CustomerProof", "yes"},
		{"Umask", "137:027"}, {"MaxDiskUsage", "99"}, {"SyslogFacility", "ftp"},
		{"TLS", tls}, {"TLSCipherSuite", "HIGH:!aNULL:!MD5:!RC4"}, {"CertFile", p.cert},
	} {
		fmt.Fprintf(&b, "%-22s %s\n", kv[0], kv[1])
	}
	if s.PassiveIP != "" {
		fmt.Fprintf(&b, "%-22s %s\n", "ForcePassiveIP", s.PassiveIP)
	}
	return b.String()
}

// Parse reads settings back from the managed configuration.
func Parse(data string) Settings {
	s := Defaults()
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(f[0], "#") {
			continue
		}
		switch f[0] {
		case "PassivePortRange":
			if len(f) == 3 {
				s.PassiveMin, _ = strconv.Atoi(f[1])
				s.PassiveMax, _ = strconv.Atoi(f[2])
			}
		case "ForcePassiveIP":
			s.PassiveIP = f[1]
		case "MaxClientsNumber":
			s.MaxClients, _ = strconv.Atoi(f[1])
		case "MaxClientsPerIP":
			s.MaxPerIP, _ = strconv.Atoi(f[1])
		case "TLS":
			if f[1] == "1" {
				s.TLS = "optional"
			}
		}
	}
	return s
}

func renderUnit(p paths) string {
	return "# Managed by NimVor (FTP).\n[Unit]\nDescription=NimVor FTP server (pure-ftpd)\nAfter=network.target\n\n" +
		"[Service]\nType=simple\nExecStart=/usr/sbin/pure-ftpd " + p.conf + "\nRestart=on-failure\nRestartSec=3\n\n" +
		"[Install]\nWantedBy=multi-user.target\n"
}

// selfSigned creates a certificate and key (pure-ftpd reads both from one
// PEM file). Clients see a self-signed certificate; the panel shows its
// fingerprint.
func selfSigned(hostname string) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: hostname, Organization: []string{"NimVor FTP"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{hostname},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	kd, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return append(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...), nil
}

// ---- accounts -------------------------------------------------------------

// Account is one virtual FTP user (never includes the password hash).
type Account struct {
	Name    string `json:"name"`
	User    string `json:"user"`
	UID     int    `json:"uid"`
	Dir     string `json:"dir"`
	QuotaMB int64  `json:"quota_mb"`
	hash    string
	gid     int
}

var accountRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._@-]{0,63}$`)

// line renders the pure-ftpd passwd line (18 colon-separated fields; the
// "/./" marks the chroot).
func (a Account) line() string {
	quota := ""
	if a.QuotaMB > 0 {
		quota = strconv.FormatInt(a.QuotaMB*1024*1024, 10)
	}
	f := make([]string, 18)
	f[0], f[1], f[2], f[3], f[5], f[12] = a.Name, a.hash, strconv.Itoa(a.UID), strconv.Itoa(a.gid), strings.TrimSuffix(a.Dir, "/")+"/./", quota
	return strings.Join(f, ":")
}

func parseAccounts(data string, accounts []users.User) []Account {
	byUID := map[int]string{}
	for _, u := range accounts {
		byUID[u.UID] = u.Name
	}
	var out []Account
	sc := bufio.NewScanner(strings.NewReader(data))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) < 13 {
			continue
		}
		uid, _ := strconv.Atoi(f[2])
		gid, _ := strconv.Atoi(f[3])
		q, _ := strconv.ParseInt(f[12], 10, 64)
		out = append(out, Account{Name: f[0], hash: f[1], UID: uid, gid: gid, User: byUID[uid], Dir: strings.TrimSuffix(f[5], "/./"), QuotaMB: q / (1024 * 1024)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func renderAccounts(list []Account) []byte {
	var b strings.Builder
	for _, a := range list {
		b.WriteString(a.line() + "\n")
	}
	return []byte(b.String())
}

// siteUser returns a website's system user: an existing account with
// UID ≥ 1000 whose home is <sites_root>/<domain>.
func siteUser(env *host.Env, name string) (users.User, error) {
	all, err := env.Accounts()
	if err != nil {
		return users.User{}, err
	}
	for _, u := range all {
		if u.Name == name {
			if u.UID < 1000 || filepath.Dir(filepath.Clean(u.Home)) != filepath.Clean(env.Cfg.SitesRoot) {
				return users.User{}, actions.InvalidParams(name + " is not a website account")
			}
			// SSH/SFTP keys, passwords and shells are never given to an
			// account with administrator rights.
			if why := env.PrivilegedAccount(u.Name, u.GID); why != "" {
				return users.User{}, actions.InvalidParams(name + " has administrator rights (" + why + "); NimVor never grants it access")
			}
			return u, nil
		}
	}
	return users.User{}, actions.NotFound("no system user " + name)
}

// siteDir resolves dir (relative to the website folder) to an existing
// folder inside it with no symbolic links on the way.
func siteDir(env *host.Env, u users.User, rel string) (string, error) {
	rel = strings.Trim(path.Clean("/"+rel), "/")
	if strings.ContainsAny(rel, ":\x00\n") || len(rel) > 400 {
		return "", actions.InvalidParams("dir is not a valid folder")
	}
	cur := filepath.Clean(u.Home)
	check := []string{cur}
	if rel != "" {
		for _, part := range strings.Split(rel, "/") {
			cur = filepath.Join(cur, part)
			check = append(check, cur)
		}
	}
	for _, p := range check {
		real, err := env.Root.Path(p)
		if err != nil {
			return "", err
		}
		info, err := os.Lstat(real)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", actions.InvalidParams(strings.TrimPrefix(p, filepath.Clean(u.Home)+"/") + " is not a folder of the website")
		}
	}
	return cur, nil
}

func validPassword(pw string) error {
	if len(pw) < 8 || len(pw) > 128 || strings.ContainsAny(pw, "\x00\r\n") {
		return fmt.Errorf("password must be 8–128 characters")
	}
	return nil
}

// probe checks that a server answers with an FTP greeting (tests replace it).
var probe = func(ctx context.Context, addr string) error {
	d := net.Dialer{Timeout: 3 * time.Second}
	var last error
	for i := 0; i < 10; i++ {
		c, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			c.SetDeadline(time.Now().Add(5 * time.Second))
			line, rerr := bufio.NewReader(c).ReadString('\n')
			c.Close()
			if rerr == nil && strings.HasPrefix(line, "220") {
				return nil
			}
			last = fmt.Errorf("unexpected greeting %q", strings.TrimSpace(line))
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return last
}

// Configured reports whether NimVor's FTP server was set up.
func Configured(env *host.Env) bool { return env.Root.Exists(ftpPaths(env).conf) }

// PassiveRange is the configured passive port range ("30000:30049"; "" when
// there is none).
func PassiveRange(env *host.Env) string {
	b, err := env.Root.ReadFile(ftpPaths(env).conf)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) == 3 && f[0] == "PassivePortRange" {
			return f[1] + ":" + f[2]
		}
	}
	return ""
}
