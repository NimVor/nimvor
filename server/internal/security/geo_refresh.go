package security

// The monthly download of the country database (GeoIpService::refresh).

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GeoSource is the DB-IP "IP to Country Lite" download for a month ("2006-01").
const GeoSource = "https://download.db-ip.com/free/dbip-country-lite-%s.csv.gz"

// GeoFetch returns the body of the https url, or nil when it cannot be
// downloaded (not 200, too large, unreachable). Overridable in tests.
var GeoFetch = func(ctx context.Context, url string) []byte {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "WhaCP brute force protection")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, GeoMaxBytes+1))
	if err != nil || len(body) > GeoMaxBytes {
		return nil
	}
	return body
}

// GeoSettings is the part of the settings store Refresh uses.
type GeoSettings interface {
	String(ctx context.Context, key, def string) (string, error)
	Set(ctx context.Context, key string, value any, userID int64) error
}

// Refresh downloads this month's database unless one from this month is
// loaded or a download was already tried today. It returns the number of
// ranges stored, or 0 when nothing was updated.
func (g *Geo) Refresh(ctx context.Context, st GeoSettings, now time.Time) (int, error) {
	now = now.UTC()
	month, day := now.Format("2006-01"), now.Format("2006-01-02")
	if updated, _ := st.String(ctx, "hulk_geo_updated", ""); len(updated) >= len(month) && updated[:len(month)] == month {
		return 0, nil
	}
	if tried, _ := st.String(ctx, "hulk_geo_tried", ""); tried == day {
		return 0, nil
	}
	if err := st.Set(ctx, "hulk_geo_tried", day, 0); err != nil {
		return 0, err
	}
	body := GeoFetch(ctx, fmt.Sprintf(GeoSource, month))
	if body == nil {
		return 0, nil
	}
	n, err := g.Import(ctx, body)
	if err != nil {
		return 0, err
	}
	if err := st.Set(ctx, "hulk_geo_updated", day, 0); err != nil {
		return n, err
	}
	return n, nil
}
