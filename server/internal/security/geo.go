package security

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"time"
)

// GeoMaxBytes and geoDecodedMaxBytes mirror GeoIpService::MAX_BYTES and the
// gzdecode limit: the database file is at most 64 MiB, and 256 MiB once
// decompressed.
const (
	GeoMaxBytes        = 64 * 1024 * 1024
	geoDecodedMaxBytes = 256 * 1024 * 1024
)

var geoCountryCode = regexp.MustCompile(`^[A-Z]{2}$`)

// Geo is the DB-IP "IP to Country Lite" store (the port of
// app/Services/GeoIpService.php over the geo_ranges table).
type Geo struct {
	db *sql.DB
}

// NewGeo returns the country database store.
func NewGeo(db *sql.DB) *Geo { return &Geo{db: db} }

// Bin is GeoIpService::bin — the IP as its 16-byte binary form (IPv4 mapped
// into IPv6, matching inet_pton + the geo_ranges columns).
func Bin(ip string) []byte {
	p := net.ParseIP(strings.TrimSpace(ip))
	if p == nil {
		return nil
	}
	return p.To16()
}

// ipFromBin is GeoIpService::ip — the printable address of a 16-byte form
// (IPv4 for the mapped addresses).
func ipFromBin(b []byte) string {
	if len(b) == 16 && bytes.Equal(b[:10], make([]byte, 10)) && b[10] == 0xff && b[11] == 0xff {
		return net.IP(b[12:]).String()
	}
	return net.IP(b).String()
}

// Loaded is GeoIpService::loaded.
func (g *Geo) Loaded(ctx context.Context) (bool, error) {
	var one int
	if err := g.db.QueryRowContext(ctx, "SELECT 1 FROM geo_ranges LIMIT 1").Scan(&one); err != nil {
		return false, nil
	}
	return true, nil
}

// Country is GeoIpService::country.
func (g *Geo) Country(ctx context.Context, ip string) (string, error) {
	b := Bin(ip)
	if b == nil {
		return "", nil
	}
	var end []byte
	var country string
	err := g.db.QueryRowContext(ctx, "SELECT `end`, country FROM geo_ranges WHERE start <= ? ORDER BY start DESC LIMIT 1", b).Scan(&end, &country)
	if err != nil {
		return "", nil
	}
	if bytes.Compare(end, b) < 0 {
		return "", nil
	}
	return country, nil
}

// CountryRange is one address range of a country (for the agent sync).
type CountryRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Ranges is GeoIpService::ranges.
func (g *Geo) Ranges(ctx context.Context, codes []string) ([]CountryRange, error) {
	ranges := []CountryRange{}
	if len(codes) == 0 {
		return ranges, nil
	}
	args := make([]any, len(codes))
	for i, c := range codes {
		args[i] = c
	}
	query := fmt.Sprintf("SELECT start, `end` FROM geo_ranges WHERE country IN (%s) ORDER BY start",
		strings.TrimSuffix(strings.Repeat("?,", len(codes)), ","))
	rows, err := g.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var from, to []byte
		if err := rows.Scan(&from, &to); err != nil {
			return nil, err
		}
		ranges = append(ranges, CountryRange{From: ipFromBin(from), To: ipFromBin(to)})
	}
	return ranges, rows.Err()
}

// Import is GeoIpService::import — it replaces the database with the ranges
// of the DB-IP "IP to Country Lite" CSV (or gzipped CSV) and returns the
// number of ranges stored. Malformed lines are skipped, like the PHP. raw is
// the file content, read by the caller with the same MAX_BYTES+1 cap as the
// PHP file_get_contents.
func (g *Geo) Import(ctx context.Context, raw []byte) (int, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("The file could not be read.")
	}
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return 0, fmt.Errorf("The file is not a valid gzip file.")
		}
		decoded, err := io.ReadAll(io.LimitReader(zr, geoDecodedMaxBytes+1))
		if err != nil || int64(len(decoded)) > geoDecodedMaxBytes {
			return 0, fmt.Errorf("The file is not a valid gzip file.")
		}
		raw = decoded
	}
	rows := [][]any{}
	for _, line := range strings.Split(string(raw), "\n") {
		cols, err := readGeoLine(strings.TrimSpace(line))
		if err != nil {
			continue
		}
		a, b := Bin(strings.TrimSpace(cols[0])), Bin(strings.TrimSpace(cols[1]))
		cc := strings.ToUpper(strings.TrimSpace(cols[2]))
		if a == nil || b == nil || bytes.Compare(a, b) > 0 || !geoCountryCode.MatchString(cc) {
			continue
		}
		rows = append(rows, []any{a, b, cc})
	}
	if len(rows) == 0 {
		return 0, fmt.Errorf(`The file has no address ranges (expected DB-IP "IP to Country Lite" CSV: first,last,country).`)
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM geo_ranges"); err != nil {
		return 0, err
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO geo_ranges (start, `end`, country) VALUES (?, ?, ?)")
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, r...); err != nil {
			stmt.Close()
			return 0, err
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// readGeoLine is str_getcsv($line, ',', '"', ”) — a two- or three-column CSV
// row with double quotes and no escape character.
func readGeoLine(line string) ([]string, error) {
	c := csv.NewReader(strings.NewReader(line))
	c.FieldsPerRecord = -1
	c.LazyQuotes = false
	c.TrailingComma = true
	cols, err := c.Read()
	if err != nil || len(cols) < 3 {
		return nil, fmt.Errorf("bad csv")
	}
	for i := range cols {
		cols[i] = strings.TrimSpace(cols[i])
	}
	return cols[:3], nil
}

// ImportStamp is the hulk_geo_updated value after a successful import (today,
// like GeoIpService::import).
func ImportStamp() string { return time.Now().Format("2006-01-02") }
