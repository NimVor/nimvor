package backup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// maxUploadChunk bounds one decoded upload chunk.
const maxUploadChunk = 8 << 20

// registerAccount adds the account backup and backup key actions.
func registerAccount(r *actions.Registry, env *host.Env, s Service) {
	r.Register(actions.Action{Name: "backup.account", Mutating: true, Description: "Back up every website (files, databases) of a hosting account and write one account backup file with the panel's description of the account (job). Always encrypted.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in AccountRequest
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.validate(s); err != nil {
			return nil, host.Invalid(err)
		}
		creds := map[string]*execx.Credential{}
		for _, site := range in.Sites {
			if _, ok := creds[site.User]; ok {
				continue
			}
			cred, _, err := env.Credential(site.User)
			if err != nil {
				return nil, actions.InvalidParams("user: " + err.Error())
			}
			creds[site.User] = cred
		}
		j := env.Jobs.Start("backup.account", "account:"+in.Username, func(ctx context.Context, log io.Writer) (any, error) {
			m, file, err := s.CreateAccount(ctx, in, creds, log)
			if err != nil {
				return nil, err
			}
			return map[string]any{"manifest": m, "path": file}, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "backup.account_files", Description: "Account backup files on this server (made here, uploaded, or in folders for mounted disks) with their descriptions.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		return map[string]any{"files": s.AccountFiles()}, nil
	}})
	r.Register(actions.Action{Name: "backup.account_inspect", Description: "The description of an account backup file, and whether it can be restored on this server.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Path string `json:"path"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		m, err := s.InspectAccount(in.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"manifest": m}, nil
	}})
	r.Register(actions.Action{Name: "backup.account_import", Mutating: true, Description: "Check an account backup file (key, authenticated descriptions, checksums) and put its website backups in the local backup folders so they can be restored (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Path string `json:"path"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if _, err := s.InspectAccount(in.Path); err != nil {
			return nil, err
		}
		j := env.Jobs.Start("backup.account_import", "account:import", func(ctx context.Context, log io.Writer) (any, error) {
			m, sites, err := s.ImportAccount(ctx, in.Path, log)
			if err != nil {
				return nil, err
			}
			return map[string]any{"manifest": m, "sites": sites}, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "backup.account_download", Description: "Read a chunk of an account backup file (base64).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Path   string `json:"path"`
			Offset int64  `json:"offset"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		b, size, err := s.AccountChunk(in.Path, in.Offset)
		if err != nil {
			return nil, err
		}
		return map[string]any{"data": base64.StdEncoding.EncodeToString(b), "size": size, "eof": in.Offset+int64(len(b)) >= size}, nil
	}})
	r.Register(actions.Action{Name: "backup.account_upload", Mutating: true, Timeout: 2 * time.Minute, Description: "Upload a chunk of an account backup file (base64) to the incoming folder.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Name   string `json:"name"`
			Offset int64  `json:"offset"`
			Data   string `json:"data"`
			Final  bool   `json:"final"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if base64.StdEncoding.DecodedLen(len(in.Data)) > maxUploadChunk {
			return nil, actions.InvalidParams("data: chunks of at most 8 MiB")
		}
		data, err := base64.StdEncoding.DecodeString(in.Data)
		if err != nil {
			return nil, actions.InvalidParams("data: not base64")
		}
		size, path, err := s.UploadAccountChunk(in.Name, in.Offset, data, in.Final)
		if err != nil {
			if _, ok := err.(*actions.Error); ok {
				return nil, err
			}
			return nil, actions.Failed("The upload could not be stored.", err.Error())
		}
		return map[string]any{"size": size, "path": path}, nil
	}})
	r.Register(actions.Action{Name: "backup.account_delete", Mutating: true, Description: "Delete an account backup file made or uploaded on this server.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Path string `json:"path"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := s.RemoveAccountFile(in.Path); err != nil {
			if _, ok := err.(*actions.Error); ok {
				return nil, err
			}
			return nil, actions.Failed("The file could not be deleted.", err.Error())
		}
		return map[string]any{"deleted": in.Path}, nil
	}})
	r.Register(actions.Action{Name: "backup.key_export", Mutating: true, Timeout: time.Minute, Description: "The backup key encrypted with a passphrase (a key file to keep safe; the key itself never leaves the agent unencrypted).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Passphrase string `json:"passphrase"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := checkPassphrase(in.Passphrase); err != nil {
			return nil, actions.InvalidParams("passphrase: " + err.Error())
		}
		key, err := existingKey(s.keyReal())
		if err != nil {
			return nil, actions.Failed("The backup key could not be read.", err.Error())
		}
		if key == nil {
			return nil, actions.NotFound("There is no backup key yet: it is created with the first encrypted backup.")
		}
		kf, err := ExportKey(key, in.Passphrase)
		if err != nil {
			return nil, actions.Failed("The key file could not be made.", err.Error())
		}
		b, _ := json.MarshalIndent(kf, "", "  ")
		return map[string]any{"key_id": kf.KeyID, "file": string(b) + "\n"}, nil
	}})
	r.Register(actions.Action{Name: "backup.key_import", Mutating: true, Timeout: time.Minute, Description: "Install a backup key from a key file and its passphrase, when this server has no backup key yet (to restore backups made on another server).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			File       string `json:"file"`
			Passphrase string `json:"passphrase"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		key, err := UnwrapKey([]byte(in.File), in.Passphrase)
		if errors.Is(err, ErrKeyPassphrase) {
			return nil, actions.InvalidParams("passphrase: " + err.Error())
		}
		if err != nil {
			return nil, actions.InvalidParams("file: " + err.Error())
		}
		already, err := InstallKey(s.keyReal(), key)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return nil, actions.Failed("The key could not be installed.", err.Error())
			}
			return nil, actions.Failed("The key was not installed.", err.Error())
		}
		return map[string]any{"key_id": KeyID(key), "already": already}, nil
	}})
}
