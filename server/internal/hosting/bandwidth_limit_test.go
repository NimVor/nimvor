package hosting

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBandwidthLimitMarksBlocksAndUnblocksForTheMonth(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	now := time.Now().UTC()
	month := now.Format("2006-01")
	// solo (4, plan 12) may send 100 MB a month; solo.example (12) sent 150 MB.
	for _, q := range []string{
		`UPDATE plans SET bandwidth_mb = 100 WHERE id = 12`,
		`INSERT INTO website_usage (website_id, bytes, files, measured_at, bw_month, bw_bytes) VALUES (12, 0, 0, '', '` + month + `', ` + "157286400" + `)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var notified []string
	svc := NewBandwidthLimitService(db, 1, NewPlanLimits(db), NewWebsiteRepository(db), h.svc, nil)
	svc.Now = func() time.Time { return now }
	svc.Notify = func(_ context.Context, owner int64, title, _, key string) { notified = append(notified, key+" "+title) }
	ctx := context.Background()
	lines := strings.Join(svc.Reconcile(ctx), "\n")
	if !strings.Contains(lines, "solo: over its monthly bandwidth (150 of 100 MB); websites blocked") {
		t.Fatalf("not blocked:\n%s", lines)
	}
	var marked string
	_ = db.QueryRow(`SELECT bw_blocked_month FROM users WHERE id = 4`).Scan(&marked)
	if marked != month || len(notified) != 1 || !strings.HasPrefix(notified[0], "bandwidth-block:4:"+month) {
		t.Fatalf("marked %q notified %v", marked, notified)
	}
	// The website's state follows (re-applied with the 509 mark), once.
	var applied int
	_ = db.QueryRow(`SELECT bandwidth_applied FROM websites WHERE id = 12`).Scan(&applied)
	if applied != 1 {
		t.Errorf("website not re-applied as blocked (bandwidth_applied %d)", applied)
	}
	if again := svc.Reconcile(ctx); len(again) != 0 {
		t.Errorf("second pass: %v", again)
	}
	// Raising the limit lifts the block.
	_, _ = db.Exec(`UPDATE plans SET bandwidth_mb = 1000 WHERE id = 12`)
	lines = strings.Join(svc.Reconcile(ctx), "\n")
	if !strings.Contains(lines, "solo: within its monthly bandwidth again; websites unblocked") {
		t.Fatalf("not unblocked:\n%s", lines)
	}
	_ = db.QueryRow(`SELECT bandwidth_applied FROM websites WHERE id = 12`).Scan(&applied)
	if applied != 0 {
		t.Errorf("website still marked blocked: %d", applied)
	}
}
