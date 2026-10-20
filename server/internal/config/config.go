// Package config holds the Go panel's runtime configuration, read from
// environment variables with production-sane defaults that match the WhaCP
// install layout.
package config

import "os"

// Config is the resolved runtime configuration.
type Config struct {
	Listen         string // host:port the HTTP server binds
	DBPath         string // SQLite database file
	StaticDir      string // directory of the reused frontend assets (public/assets)
	AgentSocket    string // the agent's Unix socket
	AgentTokenFile string // file holding the agent bearer token
	KeyFile        string // libsodium/nacl secret key (session + secrets)
	Production     bool   // true behind TLS: adds HSTS, Secure cookies
}

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	return Config{
		Listen:         env("WHACP_LISTEN", "127.0.0.1:8080"),
		DBPath:         env("WHACP_DB", "/var/lib/whacp/whacp.sqlite"),
		StaticDir:      env("WHACP_STATIC", "/usr/share/whacp/assets"),
		AgentSocket:    env("WHACP_AGENT_SOCKET", "/run/whacp/agent.sock"),
		AgentTokenFile: env("WHACP_AGENT_TOKEN", "/etc/whacp/agent.token"),
		KeyFile:        env("WHACP_KEY_FILE", "/etc/whacp/panel.key"),
		Production:     os.Getenv("WHACP_PRODUCTION") == "1",
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
