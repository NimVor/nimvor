package pgmgr

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// AdminFile holds the credentials NimVor provisions the local PostgreSQL with: root-only, never returned.
const AdminFile = "/etc/nimvor/pg-admin.json"

// Target is one PostgreSQL server to talk to. The zero value means the local server NimVor manages. A remote
// target is only ever built by the panel from a registered, administrator-approved server: it is never taken
// from a customer request.
type Target struct {
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	// SSLMode is disable, require (encrypted, not verified), verify-ca or verify-full.
	SSLMode string `json:"ssl_mode,omitempty"`
	// CAPEM is the CA certificate (PEM) the server's certificate is verified against.
	CAPEM string `json:"ca_pem,omitempty"`
}

// remote reports whether t names a server other than this machine: only those need TLS.
func (t Target) remote() bool {
	switch strings.ToLower(t.Host) {
	case "", "127.0.0.1", "::1", "localhost":
		return false
	}
	return true
}

// local loads the credentials of the local server.
func local(env *host.Env) (Target, error) {
	p, err := env.Root.Path(AdminFile)
	if err != nil {
		return Target{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Target{}, actions.Conflict("PostgreSQL is not set up for NimVor yet. Install it from the PostgreSQL page.")
	}
	var t Target
	if err := json.Unmarshal(b, &t); err != nil || t.User == "" || t.Password == "" {
		return Target{}, actions.Failed("NimVor's PostgreSQL credentials could not be read.", "The file "+AdminFile+" is damaged; reinstall or repair the PostgreSQL set-up.")
	}
	return t, nil
}

func (t Target) config(database string) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig("")
	if err != nil {
		return nil, err
	}
	cfg.Host, cfg.Port = t.Host, uint16(t.Port)
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = 5432
	}
	cfg.User, cfg.Password, cfg.Database = t.User, t.Password, database
	cfg.ConnectTimeout = 10 * time.Second
	cfg.RuntimeParams["application_name"] = "nimvor"
	cfg.TLSConfig = nil
	cfg.Fallbacks = nil
	switch strings.ToLower(t.SSLMode) {
	case "", "disable":
		if t.remote() && t.SSLMode == "" {
			return nil, actions.InvalidParams("ssl_mode: a remote server needs require, verify-ca or verify-full.")
		}
	case "require":
		cfg.TLSConfig = &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} //nolint:gosec // "require" encrypts without verifying, by the administrator's choice
	case "verify-ca", "verify-full":
		pool := x509.NewCertPool()
		if t.CAPEM != "" {
			if !pool.AppendCertsFromPEM([]byte(t.CAPEM)) {
				return nil, actions.InvalidParams("ca_pem: the CA certificate could not be read.")
			}
		} else if sys, err := x509.SystemCertPool(); err == nil {
			pool = sys
		}
		c := &tls.Config{RootCAs: pool, ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
		if strings.EqualFold(t.SSLMode, "verify-ca") {
			// Verify the chain but not the name, as libpq's verify-ca does.
			c.InsecureSkipVerify = true //nolint:gosec // the chain is verified below
			c.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
				certs := make([]*x509.Certificate, 0, len(raw))
				for _, r := range raw {
					x, err := x509.ParseCertificate(r)
					if err != nil {
						return err
					}
					certs = append(certs, x)
				}
				if len(certs) == 0 {
					return fmt.Errorf("the server sent no certificate")
				}
				inter := x509.NewCertPool()
				for _, x := range certs[1:] {
					inter.AddCert(x)
				}
				_, err := certs[0].Verify(x509.VerifyOptions{Roots: pool, Intermediates: inter})
				return err
			}
		}
		cfg.TLSConfig = c
	default:
		return nil, actions.InvalidParams("ssl_mode: choose disable, require, verify-ca or verify-full.")
	}
	return cfg, nil
}

// connect opens one connection. A remote server may only be reached with a verified or encrypted connection.
func connect(ctx context.Context, t Target, database string) (*pgx.Conn, error) {
	cfg, err := t.config(database)
	if err != nil {
		return nil, err
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, actions.Failed("PostgreSQL could not be reached.", friendly(err))
	}
	return conn, nil
}

// friendly turns a connection error into a message without credentials (pgx does not include the password,
// this keeps it that way whatever a driver update does).
func friendly(err error) string {
	return strings.TrimSpace(err.Error())
}

// session is an admin connection to one database of a server.
type session struct {
	*pgx.Conn
	super   bool
	version int // server_version_num
	target  Target
}

// resolve is the server a request is about: the registered remote target it names, or the local server.
func resolve(env *host.Env, t *Target) (Target, error) {
	if t != nil && t.remote() {
		return *t, nil
	}
	return local(env)
}

func open(ctx context.Context, env *host.Env, t *Target, database string) (*session, error) {
	tgt, err := resolve(env, t)
	if err != nil {
		return nil, err
	}
	conn, err := connect(ctx, tgt, database)
	if err != nil {
		return nil, err
	}
	s := &session{Conn: conn, target: tgt}
	var super string
	var ver string
	if err := conn.QueryRow(ctx, `SELECT current_setting('is_superuser'), current_setting('server_version_num')`).Scan(&super, &ver); err != nil {
		conn.Close(ctx)
		return nil, actions.Failed("PostgreSQL did not answer.", friendly(err))
	}
	s.super = super == "on"
	fmt.Sscanf(ver, "%d", &s.version)
	return s, nil
}

// execf runs a statement whose identifiers and literals are quoted by the server's own format(): the template
// uses %I for identifiers and %L for literals, the arguments are passed as parameters.
func (s *session) execf(ctx context.Context, tmpl string, args ...any) error {
	var stmt string
	all := append([]any{tmpl}, args...)
	ph := make([]string, len(all))
	for i := range all {
		ph[i] = fmt.Sprintf("$%d::text", i+1)
	}
	// All arguments are text; numbers are formatted by the caller.
	if err := s.QueryRow(ctx, "SELECT format("+strings.Join(ph, ", ")+")", all...).Scan(&stmt); err != nil {
		return err
	}
	_, err := s.Exec(ctx, stmt)
	return err
}
