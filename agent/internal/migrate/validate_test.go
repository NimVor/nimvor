package migrate

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func answerHTTP(r *rig, code string, err error) {
	r.h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "curl" {
			return code, err, true
		}
		return "", nil, false
	}
}

func byName(rep *ValidationReport) map[string]Check {
	m := map[string]Check{}
	for _, c := range rep.Checks {
		m[c.Item+"/"+c.Name] = c
	}
	return m
}

func TestValidationOfAHealthyMigrationPasses(t *testing.T) {
	r := newRig(t)
	answerHTTP(r, "200", nil)
	m := runDone(t, r)
	rep, err := r.e.Validate(context.Background(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed != 0 || rep.Warned != 0 || rep.Passed < 6 || len(rep.Limits) == 0 {
		t.Fatalf("report = %+v", rep)
	}
	c := byName(rep)
	for _, k := range []string{"example.com/Document root", "example.com/Files match the source", "example.com/Created files present", "example.com/HTTP answer", "example_wp/Database reachable", "example_wp/Table count"} {
		if c[k].Status != Pass {
			t.Errorf("%s = %+v", k, c[k])
		}
	}
	if os.Geteuid() == 0 && c["example.com/File ownership"].Status != Pass {
		t.Errorf("ownership = %+v", c["example.com/File ownership"])
	}
	// The result is kept with the migration and nothing was sent to the source.
	if s := r.state(m.ID); s.Validation == nil || s.Validation.Passed != rep.Passed {
		t.Errorf("state = %+v", s.Validation)
	}
	before := len(r.src.execs)
	r.e.Validate(context.Background(), m.ID)
	if len(r.src.execs) != before || r.src.rsyncCount() != 2 {
		t.Error("validating contacted the source")
	}
}

func TestValidationReportsWhatIsWrong(t *testing.T) {
	r := newRig(t)
	answerHTTP(r, "500", nil)
	m := runDone(t, r)
	os.Remove(filepath.Join(r.real, "index.php")) // a created file went missing
	r.db.tables = 7                               // the database changed
	rep, err := r.e.Validate(context.Background(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := byName(rep)
	if c["example.com/Created files present"].Status != Fail || !strings.Contains(c["example.com/Created files present"].Detail, "1 of 2") {
		t.Errorf("missing file = %+v", c["example.com/Created files present"])
	}
	if c["example_wp/Table count"].Status != Fail || !strings.Contains(c["example_wp/Table count"].Detail, "7 table(s) now, 1 were imported") {
		t.Errorf("tables = %+v", c["example_wp/Table count"])
	}
	if c["example.com/HTTP answer"].Status != Warn || !strings.Contains(c["example.com/HTTP answer"].Detail, "HTTP 500") {
		t.Errorf("http = %+v", c["example.com/HTTP answer"])
	}
	if rep.Failed != 2 || rep.Warned != 1 {
		t.Errorf("counts = %d failed, %d warned", rep.Failed, rep.Warned)
	}
}

func TestValidationFlagsAFileOwnedBySomeoneElseAndDifferencesFromTheSource(t *testing.T) {
	r := newRig(t)
	answerHTTP(r, "301", nil)
	m := runDone(t, r)
	if c := byName(mustValidate(t, r, m.ID))["example.com/HTTP answer"]; c.Status != Pass || !strings.Contains(c.Detail, "301") {
		t.Errorf("a redirect should count as an answer: %+v", c)
	}
	if os.Geteuid() == 0 {
		os.Chown(filepath.Join(r.real, "index.php"), 0, 0)
		if c := byName(mustValidate(t, r, m.ID))["example.com/File ownership"]; c.Status != Fail || !strings.Contains(c.Detail, "not owned by example") {
			t.Errorf("ownership = %+v", c)
		}
	}
	s := r.state(m.ID)
	s.Tasks[0].Differences = 3
	r.e.Store.Save(s)
	if c := byName(mustValidate(t, r, m.ID))["example.com/Files match the source"]; c.Status != Warn || !strings.Contains(c.Detail, "3 file(s) differed") {
		t.Errorf("differences = %+v", c)
	}
	// A file edited since the copy is only a note: sites are used.
	os.WriteFile(filepath.Join(r.real, "sub dir", "a b.txt"), []byte("edited since"), 0o644)
	if c := byName(mustValidate(t, r, m.ID))["example.com/Created files unchanged"]; c.Status != Warn {
		t.Errorf("changed = %+v", c)
	}
}

func TestAnUnreachableSiteIsAWarningNotAFailure(t *testing.T) {
	r := newRig(t)
	r.plan.Databases = nil
	answerHTTP(r, "", errors.New("curl: (7) Failed to connect"))
	m := runDone(t, r)
	c := byName(mustValidate(t, r, m.ID))["example.com/HTTP answer"]
	if c.Status != Warn || !strings.Contains(c.Detail, "is the website enabled") {
		t.Errorf("http = %+v", c)
	}
}

func TestValidationNeedsAMigrationThatRanAndNotOneThatWasRolledBack(t *testing.T) {
	r := newRig(t)
	m := r.create()
	if _, err := r.e.Validate(context.Background(), m.ID); err == nil {
		t.Error("a migration that never ran was validated")
	}
	if _, err := r.e.Validate(context.Background(), "0123456789abcdef"); err == nil {
		t.Error("an unknown migration was validated")
	}
	// A failed copy shows as a failed check, not a crash.
	r.src.rsyncFn = func(int, []string, string, io.Writer) error { return errors.New("connection refused") }
	r.run(m.ID)
	rep := mustValidate(t, r, m.ID)
	if c := byName(rep)["example.com/Files copied"]; c.Status != Fail {
		t.Errorf("unfinished copy = %+v", c)
	}
	r2 := newRig(t)
	r2.plan.Databases = nil
	m2 := runDone(t, r2)
	r2.e.Rollback(context.Background(), m2.ID, false, io.Discard)
	if _, err := r2.e.Validate(context.Background(), m2.ID); err == nil {
		t.Error("a rolled back migration was validated")
	}
}

func TestTheValidateActionReadsOnly(t *testing.T) {
	r := newRig(t)
	rg := newRegistry(r.h)
	a, err := rg.Lookup("migrate.validate")
	if err != nil || a.Mutating {
		t.Fatalf("action = %+v, %v", a, err)
	}
}

func mustValidate(t *testing.T, r *rig, id string) *ValidationReport {
	t.Helper()
	rep, err := r.e.Validate(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}
