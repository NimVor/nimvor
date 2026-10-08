package backup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// target identifies a website's backups, or the server's when Domain is "".
type target struct {
	Domain string  `json:"domain"`
	Kind   string  `json:"kind"` // "server" for server backups
	ID     string  `json:"id"`
	Remote *Remote `json:"remote"`
}

func (t *target) check(needID bool) error {
	if t.Kind == "server" {
		t.Domain = ""
	} else if err := validate.Domain("domain", t.Domain); err != nil {
		return host.Invalid(err)
	}
	if needID && !idRe.MatchString(t.ID) {
		return actions.InvalidParams("id: invalid backup id")
	}
	if t.Remote != nil {
		if err := t.Remote.Validate(); err != nil {
			return actions.InvalidParams("remote: " + err.Error())
		}
	}
	return nil
}

func jobTarget(domain string) string {
	if domain == "" {
		return "server:backup"
	}
	return "site:" + domain
}

// Register adds backup actions.
func Register(r *actions.Registry, env *host.Env) {
	s := Service{Env: env}
	registerAccount(r, env, s)
	r.Register(actions.Action{Name: "backup.create", Mutating: true, Description: "Back up website files and databases (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in SiteRequest
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		cred, _, err := env.Credential(in.User)
		if err != nil {
			return nil, actions.InvalidParams("user: " + err.Error())
		}
		j := env.Jobs.Start("backup.create", jobTarget(in.Domain), func(ctx context.Context, log io.Writer) (any, error) {
			return s.CreateSite(ctx, in, cred, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "backup.server", Mutating: true, Description: "Archive the server configuration (nginx, PHP, MySQL, Redis, certificates, WhaCP) (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Encrypt   *bool   `json:"encrypt"`
			Label     string  `json:"label"`
			Remote    *Remote `json:"remote"`
			KeepLocal *bool   `json:"keep_local"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validLabel(in.Label); err != nil {
			return nil, host.Invalid(err)
		}
		// Server backups hold the agent token, the panel key and TLS private
		// keys: they are always encrypted, also when kept only on this server
		// (they can be downloaded and are copied by host backups).
		if in.Encrypt != nil && !*in.Encrypt {
			return nil, actions.InvalidParams("encrypt: server backups contain private keys and are always encrypted")
		}
		if in.Remote != nil {
			if err := in.Remote.Validate(); err != nil {
				return nil, actions.InvalidParams("remote: " + err.Error())
			}
		}
		j := env.Jobs.Start("backup.server", jobTarget(""), func(ctx context.Context, log io.Writer) (any, error) {
			return s.CreateServer(ctx, true, in.Label, in.Remote, keepLocal(in.KeepLocal, in.Remote), log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "backup.list", Description: "Backups stored on this server for a website (or kind=server).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in target
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.check(false); err != nil {
			return nil, err
		}
		return s.List(in.Domain)
	}})
	r.Register(actions.Action{Name: "backup.inspect", Description: "The manifest of one backup.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in target
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.check(true); err != nil {
			return nil, err
		}
		m, _, err := s.Load(in.Domain, in.ID)
		return m, err
	}})
	r.Register(actions.Action{Name: "backup.delete", Mutating: true, Description: "Delete a backup (and its remote copy when remote is given).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in target
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.check(true); err != nil {
			return nil, err
		}
		if err := s.Delete(ctx, in.Domain, in.ID, in.Remote); err != nil {
			if _, ok := err.(*actions.Error); ok {
				return nil, err
			}
			return nil, actions.Failed("The backup could not be deleted.", err.Error())
		}
		return map[string]any{"deleted": in.ID}, nil
	}})
	r.Register(actions.Action{Name: "backup.restore", Mutating: true, Description: "Restore website files and/or databases from a backup (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in RestoreRequest
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		sr := SiteRequest{Domain: in.Domain, User: in.User, Root: in.Root, Files: true}
		if err := sr.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		in.Root = sr.Root
		if !idRe.MatchString(in.ID) {
			return nil, actions.InvalidParams("id: invalid backup id")
		}
		if !in.Files && len(in.Databases) == 0 {
			return nil, actions.InvalidParams("choose files and/or databases to restore")
		}
		for src, dst := range in.Databases {
			if validate.DBIdentifier("databases", src) != nil || validate.DBIdentifier("databases", dst) != nil {
				return nil, actions.InvalidParams("databases: invalid database name")
			}
		}
		if in.Remote != nil {
			if err := in.Remote.Validate(); err != nil {
				return nil, actions.InvalidParams("remote: " + err.Error())
			}
		}
		if _, _, err := s.Load(in.Domain, in.ID); err != nil {
			return nil, err
		}
		cred, _, err := env.Credential(in.User)
		if err != nil {
			return nil, actions.InvalidParams("user: " + err.Error())
		}
		j := env.Jobs.Start("backup.restore", jobTarget(in.Domain), func(ctx context.Context, log io.Writer) (any, error) {
			return s.Restore(ctx, in, cred, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "backup.server_restore", Mutating: true, Description: "Restore components (nginx, php, mysql, redis, letsencrypt, fail2ban, cron) of a server backup after a safety backup, validating and rolling back on failure; or extract it for manual review (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in ServerRestoreRequest
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		j := env.Jobs.Start("backup.server_restore", jobTarget(""), func(ctx context.Context, log io.Writer) (any, error) {
			defer env.LockWebConfig()()
			return s.RestoreServer(ctx, in, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "backup.remote_import", Mutating: true, Timeout: 10 * time.Minute, Description: "Find the backups in a remote storage and make those encrypted with this server's backup key (with an authenticated manifest) restorable here; used to recover onto a new server.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Remote *Remote `json:"remote"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.Remote == nil {
			return nil, actions.InvalidParams("remote is required")
		}
		if err := in.Remote.Validate(); err != nil {
			return nil, actions.InvalidParams("remote: " + err.Error())
		}
		found, err := s.ImportRemote(ctx, in.Remote)
		if err != nil {
			return nil, actions.Failed("The storage could not be searched.", err.Error())
		}
		return map[string]any{"backups": found}, nil
	}})
	r.Register(actions.Action{Name: "backup.download", Description: "Read a chunk of a stored backup file (base64).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			target
			File   string `json:"file"`
			Offset int64  `json:"offset"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.check(true); err != nil {
			return nil, err
		}
		b, size, err := s.Chunk(in.Domain, in.ID, in.File, in.Offset)
		if err != nil {
			return nil, err
		}
		return map[string]any{"data": base64.StdEncoding.EncodeToString(b), "size": size, "eof": in.Offset+int64(len(b)) >= size}, nil
	}})
	r.Register(actions.Action{Name: "backup.key_info", Description: "Fingerprint of the backup encryption key (never the key).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		real, err := env.Root.Path(s.keyPath())
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(real)
		if err != nil || len(b) != 32 {
			return map[string]any{"exists": false, "path": s.keyPath()}, nil
		}
		return map[string]any{"exists": true, "key_id": KeyID(b), "path": s.keyPath()}, nil
	}})
	r.Register(actions.Action{Name: "backup.remote_test", Mutating: true, Description: "Check that remote storage settings work. Returns an SFTP server's host key (to pin), creates the Google Drive folder when none is set, and creates a missing local folder under /backup(s) or /srv/backup(s) (never under /mnt or /media).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Remote Remote `json:"remote"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.Remote.Validate(); err != nil {
			return nil, actions.InvalidParams("remote: " + err.Error())
		}
		out := map[string]any{"ok": true}
		fail := func(err error) (any, error) {
			return nil, actions.Failed("The storage could not be reached with these settings.", err.Error())
		}
		switch in.Remote.Type {
		case "sftp":
			if in.Remote.HostKey == "" {
				// Trust on first use: try each key the server offers and pin
				// the one it actually uses; the admin confirms the fingerprint.
				keys, err := ScanHostKeys(ctx, env, in.Remote)
				if err != nil {
					return fail(err)
				}
				var last error
				for _, k := range keys {
					in.Remote.HostKey = k
					if _, last = in.Remote.Open(env).List(ctx, in.Remote.Key("")); last == nil {
						break
					}
					in.Remote.HostKey = ""
				}
				if in.Remote.HostKey == "" {
					return fail(last)
				}
			}
			out["host_key"] = in.Remote.HostKey
			out["fingerprint"] = Fingerprint(in.Remote.HostKey)
		case "ftp":
			if in.Remote.TLS != "none" && in.Remote.TLSPin == "" {
				if _, err := in.Remote.Open(env).List(ctx, in.Remote.Key("")); err != nil && strings.Contains(err.Error(), "(60)") {
					// Not signed by a trusted CA: offer to pin its key.
					pin, subject, perr := ScanTLSPin(ctx, in.Remote)
					if perr != nil {
						return fail(err)
					}
					in.Remote.TLSPin = pin
					out["tls_subject"] = subject
				}
			}
			out["tls_pin"] = in.Remote.TLSPin
		case "gdrive":
			if in.Remote.FolderID == "" {
				id, err := NewDrive(in.Remote).CreateFolder(ctx, "WhaCP backups")
				if err != nil {
					return fail(err)
				}
				in.Remote.FolderID = id
			}
			out["folder_id"] = in.Remote.FolderID
		case "local":
			created, err := (&localDir{R: in.Remote, Env: env}).Ensure()
			if err != nil {
				return fail(err)
			}
			out["created"] = created
		}
		st := in.Remote.Open(env)
		keys, err := st.List(ctx, in.Remote.Key(""))
		if err != nil {
			return fail(err)
		}
		// Prove we can write and delete, not only read.
		probe := in.Remote.Key(".whacp-probe")
		tmp, err := os.CreateTemp("", "whacp-probe-")
		if err != nil {
			return nil, err
		}
		tmp.WriteString("WhaCP storage test\n")
		tmp.Close()
		defer os.Remove(tmp.Name())
		if err := st.Put(ctx, probe, tmp.Name()); err != nil {
			return fail(fmt.Errorf("listing works but writing failed: %v", err))
		}
		if err := st.Delete(ctx, probe); err != nil {
			return fail(fmt.Errorf("writing works but deleting failed: %v", err))
		}
		out["objects"] = len(keys)
		out["description"] = in.Remote.Describe()
		return out, nil
	}})
}
