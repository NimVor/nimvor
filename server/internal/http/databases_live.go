package http

import (
	"regexp"
	"strings"
)

// dbOverviewRow is one live database as rendered on the page (the Size
// column and the "other databases" section).
type dbOverviewRow struct {
	Name     string
	Tables   int
	SizeText string
}

var dbVersionNumRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,2}`)

// dbServerLabel shortens the server's version string:
// "10.11.6-MariaDB-0ubuntu…" → "MariaDB 10.11.6", otherwise "MySQL 8.0.36".
// Ported from the databases/_manage view.
func dbServerLabel(v string) string {
	vendor := "MySQL"
	if strings.Contains(strings.ToLower(v), "mariadb") {
		vendor = "MariaDB"
	}
	if m := dbVersionNumRe.FindString(v); m != "" {
		return vendor + " " + m
	}
	return vendor
}
