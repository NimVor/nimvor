// Package firewall reports firewall state (read-only).
//
// Firewall changes are intentionally not implemented yet; see
// docs/security for the planned backup/validate/apply/verify/rollback flow.
package firewall

import (
	"bufio"
	"bytes"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// Status summarises the detected firewall.
type Status struct {
	Backend string `json:"backend"`
	Enabled bool   `json:"enabled"`
	Detail  string `json:"detail,omitempty"`
}

// Detect inspects UFW or firewalld configuration.
func Detect(root filesystem.Root, paths platform.Paths) Status {
	if paths.UFWConfig != "" {
		if data, err := root.ReadFile(paths.UFWConfig); err == nil {
			return Status{Backend: "ufw", Enabled: ufwEnabled(data)}
		}
	}
	if root.Exists("/etc/firewalld/firewalld.conf") {
		return Status{Backend: "firewalld", Detail: "state is reported by the firewalld service"}
	}
	if root.Exists("/etc/nftables.conf") {
		return Status{Backend: "nftables", Detail: "state is reported by the nftables service"}
	}
	return Status{Backend: "none"}
}

func ufwEnabled(data []byte) bool {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if ok && strings.TrimSpace(k) == "ENABLED" {
			return strings.EqualFold(strings.Trim(strings.TrimSpace(v), `"'`), "yes")
		}
	}
	return false
}
