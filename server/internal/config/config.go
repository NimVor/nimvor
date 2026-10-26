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
	LogFile        string // the panel's log file (the "panel" log source)
	Production     bool   // true behind TLS: adds HSTS, Secure cookies
	TrustedProxies string // WHACP_TRUSTED_PROXIES: reverse proxies whose X-Forwarded-For is trusted
}

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	return Config{
		Listen:         env("WHACP_LISTEN", "127.0.0.1:8090"),
		DBPath:         env("WHACP_DB_PATH", "/var/lib/whacp/whacp.sqlite"),
		StaticDir:      os.Getenv("WHACP_STATIC"), // empty → use embedded assets
		AgentSocket:    env("WHACP_AGENT_SOCKET", "/run/whacp/agent.sock"),
		AgentTokenFile: env("WHACP_AGENT_TOKEN_FILE", "/etc/whacp/agent.token"),
		KeyFile:        env("WHACP_KEY_FILE", "/etc/whacp/panel.key"),
		LogFile:        env("WHACP_LOG_FILE", "/var/log/whacp/panel.log"),
		Production:     os.Getenv("WHACP_PRODUCTION") == "1",
		TrustedProxies: env("WHACP_TRUSTED_PROXIES", "127.0.0.1,::1"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
