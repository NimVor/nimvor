package deploy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func TestDeployRunsGitAsUserWithSafeConfig(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("deploy.run")
	res, err := a.Handler(context.Background(), json.RawMessage(`{"domain":"a.test","user":"example","repo":"git@github.com:acme/app.git","branch":"main","path":"app","hooks":["composer install --no-dev"]}`))
	if err != nil {
		t.Fatal(err)
	}
	j, _ := h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string))
	if j.Status != "succeeded" {
		l, _, _ := h.Env.Jobs.Log(j.ID, 0)
		t.Fatalf("%+v\n%s", j, l)
	}
	var clone string
	for i, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "git ") && strings.Contains(c, " clone ") {
			clone = c
			o := h.Runner.Opts[i]
			if o.As == nil || o.As.UID < 1000 {
				t.Error("git must run as the site user")
			}
			env := strings.Join(o.Env, " ")
			if !strings.Contains(env, "GIT_ALLOW_PROTOCOL=https:ssh") || !strings.Contains(env, "whacp_deploy_ed25519") {
				t.Errorf("git env = %s", env)
			}
		}
	}
	if !strings.Contains(clone, "protocol.ext.allow=never") || !strings.Contains(clone, "-- git@github.com:acme/app.git app") {
		t.Errorf("clone = %q", clone)
	}
	if !h.Runner.Called("sh -c composer install --no-dev") {
		t.Errorf("hook not run: %v", h.Runner.Calls)
	}
}

func TestDeployValidation(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("deploy.run")
	for _, bad := range []string{
		`{"domain":"a.test","user":"example","repo":"file:///etc","branch":"main","path":"app"}`,
		`{"domain":"a.test","user":"example","repo":"ext::sh -c id","branch":"main","path":"app"}`,
		`{"domain":"a.test","user":"example","repo":"https://x.test/a.git","branch":"--upload-pack=id","path":"app"}`,
		`{"domain":"a.test","user":"example","repo":"https://x.test/a.git","branch":"main","path":"../other"}`,
		`{"domain":"a.test","user":"example","repo":"https://x.test/a.git","branch":"main","path":"logs"}`,
		`{"domain":"a.test","user":"root","repo":"https://x.test/a.git","branch":"main","path":"app"}`,
	} {
		if _, err := a.Handler(context.Background(), json.RawMessage(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
