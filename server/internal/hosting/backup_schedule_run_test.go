package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBackupScheduleSlots(t *testing.T) {
	loc := time.UTC
	daily := BackupSchedule{Frequency: "daily", Hour: 3, Enabled: true, CreatedAt: "2026-01-01T00:00:00Z"}
	now := time.Date(2026, 3, 10, 14, 0, 0, 0, loc)
	if got := LastSlot(daily, now); !got.Equal(time.Date(2026, 3, 10, 3, 0, 0, 0, loc)) {
		t.Errorf("daily slot %v", got)
	}
	early := time.Date(2026, 3, 10, 2, 0, 0, 0, loc)
	if got := LastSlot(daily, early); !got.Equal(time.Date(2026, 3, 9, 3, 0, 0, 0, loc)) {
		t.Errorf("before the hour the slot is yesterday's: %v", got)
	}
	weekly := BackupSchedule{Frequency: "weekly", Weekday: 1, Hour: 4, Enabled: true, CreatedAt: "2026-01-01T00:00:00Z"} // Monday
	wed := time.Date(2026, 3, 11, 12, 0, 0, 0, loc)                                                                      // Wednesday
	if got := LastSlot(weekly, wed); !got.Equal(time.Date(2026, 3, 9, 4, 0, 0, 0, loc)) {
		t.Errorf("weekly slot %v", got)
	}
	// Due until it has run in the slot; a new schedule waits for its first slot.
	if !IsDue(daily, now) {
		t.Error("never run, created before the slot: due")
	}
	daily.LastRunAt = sql.NullString{String: "2026-03-10T03:05:00Z", Valid: true}
	if IsDue(daily, now) {
		t.Error("already ran in this slot")
	}
	fresh := BackupSchedule{Frequency: "daily", Hour: 3, Enabled: true, CreatedAt: "2026-03-10T10:00:00Z"}
	if IsDue(fresh, now) {
		t.Error("a schedule made after today's slot waits for tomorrow's")
	}
	daily.Enabled = false
	daily.LastRunAt = sql.NullString{}
	if IsDue(daily, now) {
		t.Error("a disabled schedule is never due")
	}
}

func TestBackupSchedulerStartsDueBackupsAndPrunes(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	h.agent.canned = map[string]json.RawMessage{
		"backup.create": json.RawMessage(`{"job_id":"job-1"}`),
		"backup.delete": json.RawMessage(`{}`),
	}
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO backup_schedules (id,server_id,website_id,kind,frequency,hour,weekday,include_files,include_databases,encrypt,keep_local,retention,enabled,created_at)
		VALUES (1,1,10,'site','daily',3,0,1,0,0,1,1,1,'2020-01-01T00:00:00Z')`)
	exec(`INSERT INTO backups (id,server_id,website_id,schedule_id,kind,domain,backup_id,label,status,created_at) VALUES
		(1,1,10,1,'site','cust.example','old1','x','completed','2026-01-01T00:00:00Z'),
		(2,1,10,1,'site','cust.example','new1','x','completed','2026-01-02T00:00:00Z')`)
	bs := NewBackupService(db, h.agent, nil, 1, func(context.Context, string, string, string, string, map[string]any) {})
	sched := NewBackupScheduler(db, 1, nil)
	var notified []string
	run := ScheduleRunner{
		Backups: bs, Sites: NewWebsiteRepository(db), Jobs: NewJobTracker(db),
		Notify: func(_ context.Context, _ sql.NullInt64, _, title, _, _, _ string) { notified = append(notified, title) },
		Now:    func() time.Time { return time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC) },
	}
	log := strings.Join(sched.Run(context.Background(), run), "\n")
	if !strings.Contains(log, "Started backup of cust.example (job job-1)") || len(notified) != 0 {
		t.Fatalf("expected a started backup, got %q (notified %v)", log, notified)
	}
	var status, last string
	_ = db.QueryRow(`SELECT last_status, COALESCE(last_run_at,'') FROM backup_schedules WHERE id = 1`).Scan(&status, &last)
	if last == "" {
		t.Fatalf("the slot was not recorded (status %q, log %q)", status, log)
	}
	// Retention 1: the older completed backup is deleted.
	if !strings.Contains(log, "Retention: deleted backup old1") {
		t.Errorf("retention did not prune: %q", log)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM backups WHERE id = 1`).Scan(&n)
	if n != 0 {
		t.Error("the pruned backup is still listed")
	}
	// A second pass in the same slot starts nothing.
	if again := sched.Run(context.Background(), run); strings.Contains(strings.Join(again, "\n"), "Started backup") {
		t.Errorf("ran twice in one slot: %v", again)
	}
}
