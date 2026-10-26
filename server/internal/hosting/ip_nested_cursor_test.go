package hosting

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// SQLite is opened with a single pooled connection, so a query issued while a
// rows cursor is still open waits for ever. Pool, PoolFull and Ranges used to do
// exactly that (one lookup per row) and hung the IP pages; they must finish.
func TestIPPoolReadsDoNotDeadlockOnTheSingleConnection(t *testing.T) {
	db := writeDB(t)
	for _, q := range []string{
		`INSERT INTO ip_addresses (server_id,address,family,kind,reserved,reason,created_at) VALUES
			(1,'10.10.0.1',4,'shared',0,'',''),(1,'10.10.0.2',4,'dedicated',0,'',''),(1,'2001:db8::5',6,'shared',0,'','')`,
		`INSERT INTO ipv6_ranges (server_id,prefix,note,created_at) VALUES (1,'2001:db8::/64','','')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	ag := &fakeAgent{canned: map[string]json.RawMessage{"net.addresses": json.RawMessage(`{"addresses":[],"gateways":[]}`)}}
	svc := NewIpService(db, nil, NewWebsiteRepository(db), nil, nil, ag, nil, 1)

	done := make(chan error, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _, err := svc.Pool(ctx); done <- err }()
	go func() { _, err := svc.PoolFull(ctx); done <- err }()
	go func() { _, err := svc.Ranges(ctx); done <- err }()
	for i := 0; i < 3; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("pool read failed (a deadlock surfaces as the context deadline): %v", err)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("IP pool read did not finish: nested query on the single connection")
		}
	}
}
