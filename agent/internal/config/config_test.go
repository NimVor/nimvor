package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SocketPath != "/run/whacp/agent.sock" || cfg.AllowWriteActions {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadOverridesAndRejectsUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.json")
	os.WriteFile(p, []byte(`{"socket_path":"/tmp/x.sock","metrics_interval":"10s"}`), 0o600)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SocketPath != "/tmp/x.sock" || cfg.MetricsInterval.Duration != 10*time.Second {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
	os.WriteFile(p, []byte(`{"sokcet_path":"/tmp/x.sock"}`), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("expected unknown key error")
	}
}

func TestValidateRejectsWorldAccessibleSocket(t *testing.T) {
	cfg := Default()
	cfg.SocketMode = "0666"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for world-accessible socket")
	}
}

func TestReadToken(t *testing.T) {
	dir := t.TempDir()
	cfg := Default()
	cfg.TokenFile = filepath.Join(dir, "token")

	os.WriteFile(cfg.TokenFile, []byte("short\n"), 0o600)
	if _, err := cfg.ReadToken(); err == nil {
		t.Fatal("expected short token error")
	}
	good := "0123456789abcdef0123456789abcdef0123"
	os.WriteFile(cfg.TokenFile, []byte(good+"\n"), 0o644)
	os.Chmod(cfg.TokenFile, 0o644)
	if _, err := cfg.ReadToken(); err == nil {
		t.Fatal("expected world-readable token error")
	}
	os.Chmod(cfg.TokenFile, 0o640)
	tok, err := cfg.ReadToken()
	if err != nil || tok != good {
		t.Fatalf("ReadToken = %q, %v", tok, err)
	}
}
