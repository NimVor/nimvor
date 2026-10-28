package backup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// fileRemote stores backups on an SFTP or FTP(S) server through curl.
// Credentials go to curl on standard input (--config -), never in its
// arguments. SFTP host keys are pinned: curl only connects when the
// server's key matches the one saved when the storage was added, through
// a private known_hosts file. FTP requires TLS unless explicitly disabled,
// and certificates are always verified.
type fileRemote struct {
	R   Remote
	Env *host.Env
}

var knownKeyRe = keyMatcher{regexp.MustCompile(`^(ssh-ed25519|ecdsa-sha2-nistp(256|384|521)|ssh-rsa) [A-Za-z0-9+/]{40,}={0,2}$`)}

// keyMatcher also bounds the length (RSA keys can be long).
type keyMatcher struct{ re *regexp.Regexp }

func (k keyMatcher) MatchString(s string) bool { return len(s) <= 2100 && k.re.MatchString(s) }

func (f *fileRemote) hostPattern() string {
	h := f.R.Host
	if f.R.port() != 22 || strings.Contains(h, ":") {
		return "[" + h + "]:" + strconv.Itoa(f.R.port())
	}
	return h
}

// url builds the URL of a key (relative to the login folder).
func (f *fileRemote) url(key string) string {
	h := f.R.Host
	if strings.Contains(h, ":") {
		h = "[" + h + "]"
	}
	hp := net.JoinHostPort(strings.Trim(h, "[]"), strconv.Itoa(f.R.port()))
	switch {
	case f.R.Type == "sftp":
		return "sftp://" + hp + "/~/" + key
	case f.R.TLS == "implicit":
		return "ftps://" + hp + "/" + key
	}
	return "ftp://" + hp + "/" + key
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// run executes curl for one operation. extra are the operation's own
// arguments; the URL and credentials are passed on stdin.
func (f *fileRemote) run(ctx context.Context, key string, stdout io.Writer, extra ...string) ([]byte, error) {
	tmp, err := os.MkdirTemp(f.tmpBase(), "curl-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	cfg := "url = " + quote(f.url(key)) + "\n"
	if f.R.Password != "" || f.R.Type == "ftp" {
		cfg += "user = " + quote(f.R.Username+":"+f.R.Password) + "\n"
	} else {
		cfg += "user = " + quote(f.R.Username+":") + "\n"
	}
	// --disable (ignore any .curlrc) must be the first argument.
	args := []string{"--disable", "--config", "-", "--silent", "--show-error", "--connect-timeout", "20"}
	env := []string{"HOME=" + tmp}
	if f.R.Type == "sftp" {
		if !knownKeyRe.MatchString(f.R.HostKey) {
			return nil, fmt.Errorf("the server's host key is not pinned; test the storage again to trust it")
		}
		os.MkdirAll(filepath.Join(tmp, ".ssh"), 0o700)
		if err := os.WriteFile(filepath.Join(tmp, ".ssh", "known_hosts"), []byte(f.hostPattern()+" "+f.R.HostKey+"\n"), 0o600); err != nil {
			return nil, err
		}
		if f.R.PrivateKey != "" {
			kp := filepath.Join(tmp, "key")
			if err := os.WriteFile(kp, []byte(strings.TrimSpace(f.R.PrivateKey)+"\n"), 0o600); err != nil {
				return nil, err
			}
			args = append(args, "--key", kp)
		}
		args = append(args, "--proto", "=sftp")
	} else {
		args = append(args, "--proto", "=ftp,ftps")
		// Anything but an explicit "none" requires TLS: an unset value
		// must never downgrade to plain FTP.
		if f.R.TLS != "none" && f.R.TLS != "implicit" {
			args = append(args, "--ssl-reqd")
		}
		if f.R.TLS != "none" && f.R.TLSPin != "" {
			// A pinned key replaces CA verification (self-signed server);
			// curl still refuses any other key.
			args = append(args, "--pinnedpubkey", f.R.TLSPin, "--insecure")
		}
	}
	args = append(args, extra...)
	out, err := f.Env.Runner.RunWith(ctx, "curl", execx.Opts{Stdin: strings.NewReader(cfg), Env: env, Stdout: stdout, Timeout: -1}, args...)
	if err != nil {
		msg := strings.TrimSpace(host.Output(out))
		if f.R.Password != "" {
			msg = strings.ReplaceAll(msg, f.R.Password, "***")
		}
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("%s", msg)
	}
	return out, nil
}

func (f *fileRemote) tmpBase() string {
	real, err := f.Env.Root.Path(filepath.Join(f.Env.Cfg.StateDir, "tmp"))
	if err == nil && os.MkdirAll(real, 0o700) == nil {
		return real
	}
	return ""
}

func (f *fileRemote) Put(ctx context.Context, key, localPath string) error {
	_, err := f.run(ctx, key, nil, "--ftp-create-dirs", "--upload-file", localPath)
	return err
}

func (f *fileRemote) Get(ctx context.Context, key string, w io.Writer) error {
	_, err := f.run(ctx, key, w)
	return err
}

// command runs server-side commands (delete, rmdir) relative to the login
// folder, with a listing of it as the transfer curl needs.
func (f *fileRemote) command(ctx context.Context, cmds ...string) error {
	var extra []string
	for _, c := range cmds {
		extra = append(extra, "--quote", c)
	}
	_, err := f.run(ctx, "", io.Discard, append([]string{"--list-only"}, extra...)...)
	return err
}

func (f *fileRemote) Delete(ctx context.Context, key string) error {
	if f.R.Type == "sftp" {
		return f.command(ctx, "rm "+key)
	}
	return f.command(ctx, "DELE "+key)
}

func (f *fileRemote) RemoveDir(ctx context.Context, prefix string) error {
	p := strings.TrimSuffix(prefix, "/")
	if f.R.Type == "sftp" {
		return f.command(ctx, "rmdir "+p)
	}
	return f.command(ctx, "RMD "+p)
}

func (f *fileRemote) List(ctx context.Context, prefix string) ([]string, error) {
	var buf bytes.Buffer
	dir := strings.TrimSuffix(prefix, "/")
	u := dir + "/"
	if dir == "" {
		u = ""
	}
	if _, err := f.run(ctx, u, &buf, "--list-only"); err != nil {
		if strings.Contains(err.Error(), "No such file") || strings.Contains(err.Error(), "(78)") || strings.Contains(err.Error(), "(9)") || strings.Contains(err.Error(), "550") {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(buf.String(), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
			continue
		}
		if dir != "" {
			name = dir + "/" + name
		}
		out = append(out, name)
	}
	return out, nil
}

// ScanHostKeys asks the server for its host keys (ssh-keyscan).
func ScanHostKeys(ctx context.Context, env *host.Env, r Remote) ([]string, error) {
	out, err := env.Runner.RunWith(ctx, "ssh-keyscan", execx.Opts{Timeout: 20 * time.Second}, "-T", "10", "-p", strconv.Itoa(r.port()), "-t", "ed25519,ecdsa,rsa", "--", r.Host)
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("the server did not answer on port %d", r.port())
	}
	var keys []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && !strings.HasPrefix(f[0], "#") && knownKeyRe.MatchString(f[1]+" "+f[2]) {
			keys = append(keys, f[1]+" "+f[2])
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("the server on port %d did not present an SSH host key", r.port())
	}
	return keys, nil
}

// Fingerprint returns the OpenSSH SHA256 fingerprint of a known_hosts key.
func Fingerprint(key string) string {
	f := strings.Fields(key)
	if len(f) != 2 {
		return ""
	}
	blob, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
}

// ScanTLSPin connects to an FTPS server (implicit, or explicit via AUTH
// TLS) and returns the pin of its certificate's public key and subject.
func ScanTLSPin(ctx context.Context, r Remote) (pin, subject string, err error) {
	d := net.Dialer{Timeout: 15 * time.Second}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(r.Host, strconv.Itoa(r.port())))
	if err != nil {
		return "", "", err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(20 * time.Second))
	if r.TLS == "explicit" {
		br := bufio.NewReader(c)
		readReply := func() (string, error) {
			for {
				line, err := br.ReadString('\n')
				if err != nil {
					return "", err
				}
				if len(line) >= 4 && line[3] == ' ' {
					return line, nil
				}
			}
		}
		if _, err := readReply(); err != nil {
			return "", "", err
		}
		fmt.Fprintf(c, "AUTH TLS\r\n")
		line, err := readReply()
		if err != nil {
			return "", "", err
		}
		if !strings.HasPrefix(line, "234") {
			return "", "", fmt.Errorf("the server does not offer TLS (AUTH TLS: %s)", strings.TrimSpace(line))
		}
	}
	tc := tls.Client(c, &tls.Config{ServerName: r.Host, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}) //nolint:gosec // only reads the certificate to pin it
	if err := tc.HandshakeContext(ctx); err != nil {
		return "", "", err
	}
	certs := tc.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", "", errors.New("no certificate")
	}
	sum := sha256.Sum256(certs[0].RawSubjectPublicKeyInfo)
	return "sha256//" + base64.StdEncoding.EncodeToString(sum[:]), certs[0].Subject.String(), nil
}
