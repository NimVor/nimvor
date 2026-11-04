package pgmgr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// NimVor reaches the local server over 127.0.0.1 with a password, and so do the database users it creates (their
// websites connect the same way). Debian and Ubuntu allow that out of the box. The RHEL family's default
// pg_hba.conf answers 127.0.0.1 with "ident", which refuses every password login ("Ident authentication failed").
// When that is what stops NimVor's own login, one clearly marked block is put at the top of pg_hba.conf.
const (
	hbaBegin = "# BEGIN NIMVOR (added by NimVor so password logins work over 127.0.0.1; delete these lines to undo)"
	hbaEnd   = "# END NIMVOR"
	// hbaBackupSuffix names the copy kept of the file as it was before NimVor touched it.
	hbaBackupSuffix = ".nimvor-backup"
)

// hbaBlock is the managed block. scram-sha-256 needs a server that stores SCRAM passwords (the default from
// PostgreSQL 14); "md5" also accepts a password stored as SCRAM, so older servers use it.
func hbaBlock(major int) string {
	method := "md5"
	if major >= 14 {
		method = "scram-sha-256"
	}
	return hbaBegin + "\n" +
		"host    all    all    127.0.0.1/32    " + method + "\n" +
		"host    all    all    ::1/128         " + method + "\n" +
		hbaEnd + "\n"
}

// withHBABlock puts block at the top of an hba file, replacing an earlier NimVor block. changed is false when the
// file already holds exactly that block.
func withHBABlock(content, block string) (string, bool) {
	rest := withoutHBABlock(content)
	next := block + rest
	return next, next != content
}

// withoutHBABlock removes NimVor's block (and nothing else).
func withoutHBABlock(content string) string {
	b := strings.Index(content, hbaBegin)
	if b < 0 {
		return content
	}
	e := strings.Index(content[b:], hbaEnd)
	if e < 0 {
		return content // a block without an end: leave the file for a person to look at
	}
	end := b + e + len(hbaEnd)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return content[:b] + content[end:]
}

// loginError is nil when t can log in, otherwise the connection error unchanged (so its SQLSTATE can be read).
func loginError(ctx context.Context, t Target) error {
	cfg, err := t.config("postgres")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	return conn.Close(ctx)
}

// refusedByHBA reports a refusal that pg_hba.conf causes: SQLSTATE 28000 (no matching rule, or ident/peer
// failed). A wrong password is 28P01 and is not this.
func refusedByHBA(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "28000"
}

// psqlAsPostgres runs SQL through the postgres account's own socket access, as NimVor's set-up does.
func psqlAsPostgres(ctx context.Context, env *host.Env, database, sql string) (string, error) {
	cred, err := postgresCred()
	if err != nil {
		return "", err
	}
	out, err := env.Runner.RunWith(ctx, "psql", execx.Opts{As: cred, Stdin: strings.NewReader(sql), Dir: "/", Timeout: 30 * time.Second},
		"-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-d", database)
	if err != nil {
		return "", fmt.Errorf("%s", host.Output(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// ensureLoopbackLogin makes sure NimVor's own login over 127.0.0.1 works, repairing pg_hba.conf only when the
// failure is the one pg_hba.conf causes. Detect, back up, change, reload, verify: when the login still fails the
// file goes back exactly as it was.
func ensureLoopbackLogin(ctx context.Context, env *host.Env, log io.Writer, t Target) error {
	err := loginError(ctx, t)
	if err == nil {
		return nil
	}
	if !refusedByHBA(err) {
		// Not something this can repair (the server may still be starting): leave it to the first real request.
		fmt.Fprintln(log, "  ! the login over 127.0.0.1 could not be tested: "+friendly(err))
		return nil
	}
	hba, err := psqlAsPostgres(ctx, env, "postgres", "SHOW hba_file;")
	if err != nil || hba == "" {
		return fmt.Errorf("PostgreSQL refuses NimVor's login over 127.0.0.1 and its pg_hba.conf could not be located: %v", err)
	}
	real, err := env.Root.Path(hba)
	if err != nil {
		return err
	}
	info, err := os.Stat(real)
	if err != nil {
		return fmt.Errorf("pg_hba.conf (%s) could not be read: %w", hba, err)
	}
	orig, err := os.ReadFile(real)
	if err != nil {
		return fmt.Errorf("pg_hba.conf (%s) could not be read: %w", hba, err)
	}
	next, changed := withHBABlock(string(orig), hbaBlock(serverMajor(ctx, env)))
	if !changed {
		return fmt.Errorf("PostgreSQL still refuses the login although %s already has NimVor's rule; check the rules above it", hba)
	}
	if _, err := os.Stat(real + hbaBackupSuffix); err != nil {
		if _, err := env.Writer.WriteFile(hba+hbaBackupSuffix, orig, info.Mode().Perm(), agentfs.Keep); err != nil {
			return fmt.Errorf("the backup of %s could not be written: %w", hba, err)
		}
	}
	restore := func() {
		_, _ = env.Writer.WriteFile(hba, orig, info.Mode().Perm(), agentfs.Keep)
		_, _ = psqlAsPostgres(ctx, env, "postgres", "SELECT pg_reload_conf();")
	}
	if _, err := env.Writer.WriteFile(hba, []byte(next), info.Mode().Perm(), agentfs.Keep); err != nil {
		return fmt.Errorf("%s could not be changed: %w", hba, err)
	}
	if _, err := psqlAsPostgres(ctx, env, "postgres", "SELECT pg_reload_conf();"); err != nil {
		restore()
		return fmt.Errorf("PostgreSQL did not reload its configuration: %v", err)
	}
	var last error
	for i := 0; i < 20; i++ {
		if last = loginError(ctx, t); last == nil {
			fmt.Fprintln(log, "  ✓ pg_hba.conf: password logins over 127.0.0.1 are allowed (a marked block at the top; the original is kept as "+hba+hbaBackupSuffix+")")
			return nil
		}
		select {
		case <-ctx.Done():
			restore()
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	restore()
	return actions.Failed("PostgreSQL still refuses NimVor's login after the change; pg_hba.conf was put back as it was.",
		friendly(last)+" - allow password logins from 127.0.0.1 for the account "+AdminRole+" in pg_hba.conf, above any 'ident' line.")
}
