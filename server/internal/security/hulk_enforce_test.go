package security

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

func hulkEnv(t *testing.T) (*Hulk, *settings.Repository, *[]string, HulkHooks) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	st := settings.NewRepository(db)
	ctx := context.Background()
	for k, v := range map[string]any{"hulk_enabled": true, "hulk_ip_enabled": true, "hulk_ip_max": 3, "hulk_ip_period": 5, "hulk_oneday_max": 10,
		"hulk_user_enabled": true, "hulk_user_max": 4, "hulk_user_period": 5, "hulk_notify_brute": true} {
		if err := st.Set(ctx, k, v, 0); err != nil {
			t.Fatal(err)
		}
	}
	var log []string
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	hk := HulkHooks{
		Now:    func() time.Time { return now },
		Notify: func(_ context.Context, _, title, _, _, _ string) { log = append(log, "notify:"+title) },
		Audit:  func(_ context.Context, a, r string, _ map[string]any) { log = append(log, a+":"+r) },
	}
	return NewHulk(db, st), st, &log, hk
}

func fail(h *Hulk, hk HulkHooks, id, user, ip string) []HulkEvent {
	return h.Store(context.Background(), 1, []HulkEvent{{ID: id, Time: hk.Now().Unix(), Service: "sshd", AuthService: "sshd", User: user, IP: ip}})
}

func TestHulkBlocksAnAddressAfterTooManyFailures(t *testing.T) {
	h, _, log, hk := hulkEnv(t)
	ctx := context.Background()
	made := 0
	for i := 0; i < 3; i++ {
		made += h.Evaluate(ctx, fail(h, hk, "e"+string(rune('a'+i)), "user"+string(rune('a'+i)), "203.0.113.9"), hk)
	}
	if made != 1 {
		t.Fatalf("blocks %d, want 1 (at the third failure)", made)
	}
	// A fourth failure does not block twice.
	if n := h.Evaluate(ctx, fail(h, hk, "ex", "userx", "203.0.113.9"), hk); n != 0 {
		t.Errorf("blocked again: %d", n)
	}
	if got := h.LoginRefusal(ctx, "bob", "203.0.113.9", hk); got == "" {
		t.Error("the blocked address may still sign in")
	}
	if got := h.LoginRefusal(ctx, "bob", "203.0.113.77", hk); got != "" {
		t.Errorf("another address refused: %s", got)
	}
	found := false
	for _, l := range *log {
		found = found || l == "notify:Brute force attack detected: 203.0.113.9"
	}
	if !found {
		t.Errorf("no notification: %v", *log)
	}
}

func TestHulkNeverBlocksWhitelistedOrLocalAddresses(t *testing.T) {
	h, st, _, hk := hulkEnv(t)
	ctx := context.Background()
	_ = st.Set(ctx, "hulk_whitelist", "198.51.100.0/24", 0)
	for i := 0; i < 6; i++ {
		id := string(rune('a' + i))
		if n := h.Evaluate(ctx, fail(h, hk, "w"+id, "w"+id, "198.51.100.5"), hk); n != 0 {
			t.Fatalf("a whitelisted address was blocked")
		}
		if n := h.Evaluate(ctx, fail(h, hk, "l"+id, "l"+id, "127.0.0.1"), hk); n != 0 {
			t.Fatalf("loopback was blocked")
		}
	}
}

func TestHulkLocksAnAccountAndTheRootOption(t *testing.T) {
	h, st, _, hk := hulkEnv(t)
	ctx := context.Background()
	made := 0
	for i := 0; i < 4; i++ {
		// A different address each time: only the account rule can fire.
		made += h.Evaluate(ctx, fail(h, hk, "u"+string(rune('a'+i)), "alice", "192.0.2."+string(rune('1'+i))), hk)
	}
	if made != 1 {
		t.Fatalf("account blocks %d", made)
	}
	if h.LoginRefusal(ctx, "alice", "192.0.2.50", hk) == "" {
		t.Error("the locked account may sign in")
	}
	// root is never locked unless the option is on.
	for i := 0; i < 5; i++ {
		if n := h.Evaluate(ctx, fail(h, hk, "r"+string(rune('a'+i)), "root", "192.0.2."+string(rune('1'+i))), hk); n != 0 {
			t.Fatalf("root was locked without the option")
		}
	}
	_ = st.Set(ctx, "hulk_user_lock_root", true, 0)
	n := 0
	for i := 0; i < 2; i++ {
		n += h.Evaluate(ctx, fail(h, hk, "r2"+string(rune('a'+i)), "root", "192.0.2.9"), hk)
	}
	if n != 1 {
		t.Errorf("root with the option on: %d blocks", n)
	}
}

func TestHulkPanelFailuresAndDisabled(t *testing.T) {
	h, st, _, hk := hulkEnv(t)
	ctx := context.Background()
	total := 0
	for i := 0; i < 3; i++ {
		total += h.RecordPanelFailure(ctx, "carol", "203.0.113.20", hk)
	}
	if total != 1 || h.LoginRefusal(ctx, "carol", "203.0.113.20", hk) == "" {
		t.Fatalf("panel failures: blocks %d", total)
	}
	_ = st.Set(ctx, "hulk_enabled", false, 0)
	if h.RecordPanelFailure(ctx, "dave", "203.0.113.21", hk) != 0 || h.LoginRefusal(ctx, "carol", "203.0.113.20", hk) != "" {
		t.Error("a disabled protection acts")
	}
}

// The panel never runs a command when an address is blocked: there is no
// hook for it.
func TestBlockingRunsNoCommand(t *testing.T) {
	if _, ok := reflect.TypeOf(HulkHooks{}).FieldByName("RunCommand"); ok {
		t.Error("HulkHooks gained a way to run commands")
	}
}
