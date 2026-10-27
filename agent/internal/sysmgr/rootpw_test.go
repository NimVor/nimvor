package sysmgr

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestRootPassword(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("server.root_password")
	call := func(pw string) error {
		b, _ := json.Marshal(map[string]string{"password": pw})
		_, err := a.Handler(context.Background(), b)
		return err
	}
	const pw = "Correct-Horse-Battery-9"
	if err := call(pw); err != nil {
		t.Fatal(err)
	}
	fake := h.Runner
	in := fake.Stdins[len(fake.Stdins)-1]
	if !strings.HasPrefix(in, "root:$6$") || strings.Contains(in, pw) || strings.Count(in, "\n") != 1 {
		t.Fatalf("chpasswd got %q", in)
	}
	for _, bad := range []string{"short", "Correct-Horse\nbob:x", "Correct:Horse-Battery", strings.Repeat("a", 600)} {
		if err := call(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
