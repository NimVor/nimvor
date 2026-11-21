package phpmgr

import (
	"testing"
	"time"
)

func TestScanFPMLogCountsOnlyTheLastDay(t *testing.T) {
	now := time.Date(2026, 11, 21, 21, 0, 0, 0, time.UTC)
	log := `[19-Nov-2026 10:00:00] WARNING: [pool old] server reached pm.max_children setting (5), consider raising it
[21-Nov-2026 08:00:00] WARNING: [pool shop] server reached pm.max_children setting (5), consider raising it
[21-Nov-2026 08:00:05.123456] WARNING: [pool blog] server reached pm.max_children setting (5), consider raising it
[21-Nov-2026 09:00:00] WARNING: [pool shop] seems busy (you may need to increase pm.start_servers, or pm.min/max_spare_servers), spawning 8 children, there are 0 idle, and 11 total children
[21-Nov-2026 10:00:00] WARNING: [pool shop] child 1234 exited on signal 11 (SIGSEGV - core dumped) after 100.2 seconds from start
[21-Nov-2026 11:00:00] NOTICE: fpm is running, pid 1
not a log line
`
	mc, busy, crashes, pool := ScanFPMLog(log, now)
	if mc != 2 || busy != 1 || crashes != 1 || pool != "blog" {
		t.Fatalf("got max=%d busy=%d crashes=%d pool=%q", mc, busy, crashes, pool)
	}
}

func TestScanFPMLogOfNothingIsZeroEvents(t *testing.T) {
	if mc, b, c, p := ScanFPMLog("", time.Now()); mc+b+c != 0 || p != "" {
		t.Fatal("empty log has no events")
	}
}
