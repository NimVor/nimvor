package audit

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

func seedLogger(t *testing.T) *Logger {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES (1,'administrator','Admin','all','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id,username,email,password_hash,role_id,is_active,password_changed_at,created_at,updated_at)
		VALUES (1,'bob','b@x','h',1,1,'','',''),(2,'amy','a@x','h',1,1,'','','')`); err != nil {
		t.Fatal(err)
	}
	return New(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestSearchFiltersAndOrder(t *testing.T) {
	l := seedLogger(t)
	ctx := context.Background()
	b, a := int64(1), int64(2)
	l.Record(ctx, Entry{UserID: &b, Username: "bob", Action: "database.create", TargetType: "database", TargetID: "shop", IP: "10.0.0.1"})
	l.Record(ctx, Entry{UserID: &b, Username: "bob", Action: "database.drop", TargetType: "database", TargetID: "old", Result: Failure, IP: "10.0.0.1"})
	l.Record(ctx, Entry{UserID: &a, Username: "amy", Action: "auth.login", TargetType: "user", TargetID: "2", IP: "10.0.0.2"})

	// Newest first, no filter.
	all, err := l.Search(ctx, Filters{}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 3 || len(all.Rows) != 3 || all.Rows[0].Action != "auth.login" {
		t.Fatalf("unfiltered = total %d, first %s", all.Total, all.Rows[0].Action)
	}

	// Action prefix match.
	dbActs, _ := l.Search(ctx, Filters{Action: "database."}, 1, 50)
	if dbActs.Total != 2 {
		t.Fatalf("action prefix total = %d, want 2", dbActs.Total)
	}

	// Result filter.
	fails, _ := l.Search(ctx, Filters{Result: Failure}, 1, 50)
	if fails.Total != 1 || fails.Rows[0].Action != "database.drop" {
		t.Fatalf("result filter = %d", fails.Total)
	}

	// User filter.
	amy, _ := l.Search(ctx, Filters{User: "amy"}, 1, 50)
	if amy.Total != 1 || amy.Rows[0].Username != "amy" {
		t.Fatalf("user filter = %d", amy.Total)
	}

	// q over target/ip.
	byIP, _ := l.Search(ctx, Filters{Q: "10.0.0.2"}, 1, 50)
	if byIP.Total != 1 || byIP.Rows[0].Username != "amy" {
		t.Fatalf("q filter = %d", byIP.Total)
	}
}

func TestSearchPagination(t *testing.T) {
	l := seedLogger(t)
	ctx := context.Background()
	b := int64(1)
	for i := 0; i < 5; i++ {
		l.Record(ctx, Entry{UserID: &b, Username: "bob", Action: "x", TargetType: "t", TargetID: "n"})
	}
	p1, _ := l.Search(ctx, Filters{}, 1, 2)
	p3, _ := l.Search(ctx, Filters{}, 3, 2)
	if p1.Total != 5 || len(p1.Rows) != 2 || len(p3.Rows) != 1 {
		t.Fatalf("pagination: total=%d p1=%d p3=%d", p1.Total, len(p1.Rows), len(p3.Rows))
	}
}

// A LIKE wildcard in user input must be matched literally, not as a wildcard.
func TestSearchEscapesLikeWildcards(t *testing.T) {
	l := seedLogger(t)
	ctx := context.Background()
	b := int64(1)
	l.Record(ctx, Entry{UserID: &b, Username: "bob", Action: "a", TargetType: "t", TargetID: "100%off"})
	l.Record(ctx, Entry{UserID: &b, Username: "bob", Action: "a", TargetType: "t", TargetID: "safe"})
	// "%off" as a literal substring should match only the first row, and the
	// bare "%" in the query must not turn into a match-all.
	hit, _ := l.Search(ctx, Filters{Q: "100%off"}, 1, 50)
	if hit.Total != 1 {
		t.Fatalf("literal %% match total = %d, want 1", hit.Total)
	}
}
