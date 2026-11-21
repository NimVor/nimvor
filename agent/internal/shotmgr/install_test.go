package shotmgr

import (
	"testing"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// The files the install writes must be on the agent's write allowlist
// (internal/host/env.go); without them it fails with "path is not managed by
// NimVor" before it installs anything.
func TestInstallWritesOnlyAllowedFiles(t *testing.T) {
	h := testhost.New(t)
	for _, p := range []string{"/etc/apt/keyrings", aptKeyring, aptSources, dnfRepoFile} {
		if !h.Env.Writer.Policy.Allows(p) {
			t.Errorf("the agent may not write %s", p)
		}
	}
	if h.Env.Writer.Policy.Allows("/etc/apt/sources.list.d/other.sources") {
		t.Error("the allowlist is wider than NimVor's own file")
	}
}
