//go:build realhost

package pgmgr

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

// Installs PostgreSQL when it is missing (adopts it when the image already has it), then provisions, isolates and
// removes a customer's database through the real service, as the agent does in production.
func TestRealHostPostgres(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := filesystem.Host()
	cfg := config.Default()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(120*time.Second), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)
	ctx := context.Background()
	do := func(name string, params map[string]any) any {
		t.Helper()
		b, _ := json.Marshal(params)
		a, err := r.Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		res, err := a.Handler(ctx, b)
		if err != nil {
			if e, ok := err.(*actions.Error); ok {
				t.Fatalf("%s: %v (%s)", name, err, e.Details)
			}
			t.Fatalf("%s: %v", name, err)
		}
		return res
	}

	if !serverBinary(env) {
		res := do("pg.install", map[string]any{}).(map[string]any)
		job, err := jm.Wait(ctx, res["job_id"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != jobs.Succeeded {
			l, _, _ := jm.Log(job.ID, 0)
			t.Fatalf("pg.install %s: %s\n%s", job.Status, job.Error, l)
		}
	} else {
		do("pg.service", map[string]any{"verb": "start"})
		if err := waitReady(ctx, env); err != nil {
			t.Fatal(err)
		}
		do("pg.adopt", map[string]any{"confirm": true, "harden": true})
	}
	st := status(ctx, env)
	if !st.Installed || !st.Running || !st.Managed {
		t.Fatalf("status after set-up: %+v", st)
	}

	// "nimvor_" names are reserved for NimVor itself, so the test uses its own prefix.
	owner, reader, db := "rt_owner", "rt_reader", "rt_db"
	t.Cleanup(func() {
		for _, n := range []string{db} {
			b, _ := json.Marshal(map[string]any{"name": n})
			a, _ := r.Lookup("pg.database_delete")
			a.Handler(ctx, b)
		}
		for _, n := range []string{owner, reader} {
			b, _ := json.Marshal(map[string]any{"name": n})
			a, _ := r.Lookup("pg.role_delete")
			a.Handler(ctx, b)
		}
	})
	do("pg.role_create", map[string]any{"name": owner, "password": "Owner-pass-for-the-test-1"})
	do("pg.role_create", map[string]any{"name": reader, "password": "Reader-pass-for-the-test-1"})
	do("pg.database_create", map[string]any{"name": db, "owner": owner})
	do("pg.grant_set", map[string]any{"database": db, "role": reader, "level": "readonly"})

	l, err := local(env)
	if err != nil {
		t.Fatal(err)
	}
	as := func(user, pw, database string) (*session, error) {
		conn, err := connect(ctx, Target{Host: "127.0.0.1", Port: l.Port, User: user, Password: pw}, database)
		if err != nil {
			return nil, err
		}
		return &session{Conn: conn}, nil
	}
	o, err := as(owner, "Owner-pass-for-the-test-1", db)
	if err != nil {
		t.Fatalf("owner cannot connect: %v", err)
	}
	if _, err := o.Exec(ctx, `CREATE TABLE t (v int); INSERT INTO t VALUES (1)`); err != nil {
		t.Fatalf("owner cannot create a table: %v", err)
	}
	o.Close(ctx)
	rd, err := as(reader, "Reader-pass-for-the-test-1", db)
	if err != nil {
		t.Fatalf("reader cannot connect: %v", err)
	}
	var n int
	if err := rd.QueryRow(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("reader cannot read: %v %d", err, n)
	}
	if _, err := rd.Exec(ctx, `INSERT INTO t VALUES (2)`); err == nil {
		t.Error("the read-only user wrote")
	}
	rd.Close(ctx)
	// Another database user cannot enter, nor can anyone enter the maintenance databases.
	do("pg.role_create", map[string]any{"name": "rt_other", "password": "Other-pass-for-the-test-1"})
	t.Cleanup(func() {
		b, _ := json.Marshal(map[string]any{"name": "rt_other"})
		a, _ := r.Lookup("pg.role_delete")
		a.Handler(ctx, b)
	})
	for _, d := range []string{db, "postgres", "template1"} {
		if s, err := as("rt_other", "Other-pass-for-the-test-1", d); err == nil {
			s.Close(ctx)
			t.Errorf("an unrelated user entered %s", d)
		}
	}
	do("pg.database_delete", map[string]any{"name": db})
	do("pg.role_delete", map[string]any{"name": reader})
	do("pg.role_delete", map[string]any{"name": owner})
	ov := do("pg.overview", map[string]any{}).(map[string]any)
	b, _ := json.Marshal(ov)
	if string(b) == "" {
		t.Fatal("empty overview")
	}
}
