package sitemetrics

import (
	"strconv"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/host"
)

// uidToName maps a user id to its name through the host's /etc/passwd.
func uidToName(e *host.Env, uid uint32) string {
	b, err := e.Root.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	want := strconv.FormatUint(uint64(uid), 10)
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) > 2 && f[2] == want {
			return f[0]
		}
	}
	return ""
}
