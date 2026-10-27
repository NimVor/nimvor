package security

import (
	"context"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

func TestGeoRefreshMonthlyOncePerDay(t *testing.T) {
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	st := settings.NewRepository(db)
	g := NewGeo(db)
	ctx := context.Background()
	var urls []string
	old := GeoFetch
	GeoFetch = func(_ context.Context, u string) []byte {
		urls = append(urls, u)
		return []byte("198.51.100.0,198.51.100.255,ZZ\n")
	}
	defer func() { GeoFetch = old }()

	now := time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC)
	n, err := g.Refresh(ctx, st, now)
	if err != nil || n != 1 {
		t.Fatalf("Refresh = %d, %v", n, err)
	}
	if len(urls) != 1 || urls[0] != "https://download.db-ip.com/free/dbip-country-lite-2026-10.csv.gz" {
		t.Errorf("urls = %v", urls)
	}
	// This month's database is loaded: no new download.
	if n, _ := g.Refresh(ctx, st, now.Add(time.Hour)); n != 0 || len(urls) != 1 {
		t.Errorf("same month: n=%d urls=%v", n, urls)
	}
	// Next month: one try a day, even when the download fails.
	GeoFetch = func(_ context.Context, u string) []byte { urls = append(urls, u); return nil }
	next := time.Date(2026, 11, 2, 3, 0, 0, 0, time.UTC)
	g.Refresh(ctx, st, next)
	g.Refresh(ctx, st, next.Add(time.Hour))
	if len(urls) != 2 {
		t.Errorf("tries on one day = %d, want 1 more", len(urls)-1)
	}
	g.Refresh(ctx, st, next.Add(24*time.Hour))
	if len(urls) != 3 {
		t.Errorf("next day should try again: %v", urls)
	}
}
