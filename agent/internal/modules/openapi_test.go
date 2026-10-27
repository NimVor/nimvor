package modules

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

const specPath = "../../api/openapi.yaml"

// The API reference must list exactly the actions the agent registers.
// Regenerate the listed blocks with: WHACP_UPDATE_OPENAPI=1 go test ./internal/modules -run OpenAPI
func TestOpenAPIListsEveryAction(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env, "test", true, All()...)
	actions.RegisterBuiltin(r, actions.Deps{Version: "test"})
	list := r.List()

	var enum, catalog strings.Builder
	for _, a := range list {
		fmt.Fprintf(&enum, "              - %s\n", a.Name)
		fmt.Fprintf(&catalog, "  %s: { mutating: %t, description: %q }\n", a.Name, a.Mutating, a.Description)
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	want := replaceBlock(t, spec, "enum", enum.String())
	want = replaceBlock(t, want, "catalog", catalog.String())
	if want == spec {
		return
	}
	if os.Getenv("WHACP_UPDATE_OPENAPI") == "1" {
		if err := os.WriteFile(specPath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("api/openapi.yaml does not match the registered actions (%d); run WHACP_UPDATE_OPENAPI=1 go test ./internal/modules -run OpenAPI", len(list))
}

func replaceBlock(t *testing.T, spec, name, body string) string {
	t.Helper()
	re := regexp.MustCompile(`(?s)(# BEGIN generated ` + name + `[^\n]*\n).*?([ \t]*# END generated ` + name + `)`)
	if !re.MatchString(spec) {
		t.Fatalf("api/openapi.yaml has no %q generated block", name)
	}
	return re.ReplaceAllStringFunc(spec, func(m string) string {
		sub := re.FindStringSubmatch(m)
		return sub[1] + body + sub[2]
	})
}
