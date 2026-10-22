// Package stats ports AWStats' plain-text data file to the website's
// statistics report (StatsService). The panel draws the report itself;
// AWStats' own HTML is never shown.
package stats

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DayRow is one day of the month (the DAY section).
type DayRow struct {
	Day    string // YYYYMMDD
	Visits int
	Pages  int
	Hits   int
	Bytes  int
}

// HourRow is one hour of the day (the TIME section).
type HourRow struct {
	Pages int
	Hits  int
	Bytes int
}

// TopRow is one row of a top list (label + value).
type TopRow struct {
	Label string
	Value int
}

// Report is the parsed month (the view's $report).
type Report struct {
	Unique    int
	Visits    int
	Pages     int
	Hits      int
	Bytes     int
	Updated   string // ISO time, "" when the file gave none
	Days      []DayRow
	Hours     [24]HourRow
	PagesTop  []TopRow
	Referrers []TopRow
	Search    []TopRow
	Browsers  []TopRow
	Systems   []TopRow
	Countries []TopRow
	Errors    []TopRow
}

var beginRe = regexp.MustCompile(`^BEGIN_([A-Z_]+)\b`)
var dayKeyRe = regexp.MustCompile(`^[0-9]{8}$`)
var stampRe = regexp.MustCompile(`^([0-9]{4})([0-9]{2})([0-9]{2})([0-9]{2})([0-9]{2})([0-9]{2})`)

// MonthRe validates a month (YYYYMM, the agent's check).
var MonthRe = regexp.MustCompile(`^[0-9]{4}(0[1-9]|1[0-2])$`)

// Parse converts one month's AWStats data file to the report. Sections are
// "BEGIN_X n" … "END_X" with space-separated rows; unknown sections and
// malformed rows are skipped.
func Parse(data string) Report {
	sections := map[string][][]string{}
	var cur string
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if m := beginRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
			sections[cur] = nil
			continue
		}
		if strings.HasPrefix(line, "END_") {
			cur = ""
			continue
		}
		if cur == "" || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sections[cur] = append(sections[cur], strings.Split(line, " "))
	}
	// (int) $row[$i] ?? 0: PHP's numeric cast of the field.
	num := func(r []string, i int) int {
		if i >= len(r) {
			return 0
		}
		n, err := strconv.Atoi(strings.TrimSpace(r[i]))
		if err != nil {
			return 0
		}
		return n
	}
	general := map[string]string{}
	for _, r := range sections["GENERAL"] {
		if len(r) == 0 {
			continue
		}
		general[r[0]] = ""
		if len(r) > 1 {
			general[r[0]] = r[1]
		}
	}
	// The DAY rows keyed by day, ksorted (newest months are read back in
	// file order; the view shows them in key order).
	days := map[string]DayRow{}
	for _, r := range sections["DAY"] {
		if len(r) > 0 && dayKeyRe.MatchString(r[0]) {
			days[r[0]] = DayRow{Day: r[0], Visits: num(r, 4), Pages: num(r, 1), Hits: num(r, 2), Bytes: num(r, 3)}
		}
	}
	keys := make([]string, 0, len(days))
	for d := range days {
		keys = append(keys, d)
	}
	sort.Strings(keys)
	daysList := make([]DayRow, 0, len(days))
	for _, d := range keys {
		daysList = append(daysList, days[d])
	}
	var hours [24]HourRow
	for _, r := range sections["TIME"] {
		h := 0
		if len(r) > 0 {
			if n, err := strconv.Atoi(strings.TrimSpace(r[0])); err == nil {
				h = n
			}
		}
		if h >= 0 && h < 24 {
			hours[h] = HourRow{Pages: num(r, 1), Hits: num(r, 2), Bytes: num(r, 3)}
		}
	}
	// top: every row becomes a labeled row, sorted by value (stable), cut.
	top := func(rows [][]string, valueIdx, limit int, label func(string) string) []TopRow {
		out := make([]TopRow, 0, len(rows))
		for _, r := range rows {
			l := ""
			if len(r) > 0 {
				l = label(r[0])
			}
			out = append(out, TopRow{Label: l, Value: num(r, valueIdx)})
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Value > out[j].Value })
		if len(out) > limit {
			out = out[:limit]
		}
		return out
	}
	plain := func(s string) string { return s }
	totals := struct{ visits, pages, hits, bytes int }{}
	for _, d := range daysList {
		totals.visits += d.Visits
		totals.pages += d.Pages
		totals.hits += d.Hits
		totals.bytes += d.Bytes
	}
	visits := totals.visits
	if tv, err := strconv.Atoi(general["TotalVisits"]); err == nil && tv > visits {
		visits = tv
	}
	unique, _ := strconv.Atoi(general["TotalUnique"])
	searchLabel := func(e string) string {
		return strings.TrimPrefix(strings.ReplaceAll(e, "_", "."), "www.")
	}
	countryLabel := func(c string) string {
		if c == "ip" || c == "unknown" {
			return "Unknown"
		}
		return strings.ToUpper(c)
	}
	return Report{
		Unique:    unique,
		Visits:    visits,
		Pages:     totals.pages,
		Hits:      totals.hits,
		Bytes:     totals.bytes,
		Updated:   Stamp(general["LastUpdate"]),
		Days:      daysList,
		Hours:     hours,
		PagesTop:  top(sections["SIDER"], 1, 25, plain),
		Referrers: top(sections["PAGEREFS"], 1, 15, plain),
		Search:    top(sections["SEREFERRALS"], 1, 10, searchLabel),
		Browsers:  Merge(top(sections["BROWSER"], 1, 200, Browser), 10),
		Systems:   Merge(top(sections["OS"], 1, 200, System), 10),
		Countries: top(sections["DOMAIN"], 2, 15, countryLabel),
		Errors:    top(sections["ERRORS"], 1, 10, plain),
	}
}

// Stamp is "20260927153000" → ISO time, or "".
func Stamp(s string) string {
	m := stampRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1] + "-" + m[2] + "-" + m[3] + "T" + m[4] + ":" + m[5] + ":" + m[6] + "Z"
}

// Merge sums the rows that got the same friendly label (e.g. all Chrome
// versions), keeps the first-appearance order for equal totals and cuts.
func Merge(rows []TopRow, limit int) []TopRow {
	sum := map[string]int{}
	order := []string{}
	for _, r := range rows {
		if _, ok := sum[r.Label]; !ok {
			order = append(order, r.Label)
		}
		sum[r.Label] += r.Value
	}
	out := make([]TopRow, 0, len(order))
	for _, label := range order {
		out = append(out, TopRow{Label: label, Value: sum[label]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Value > out[j].Value })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Browser is the friendly browser name (the id's prefix).
func Browser(id string) string {
	switch {
	case strings.HasPrefix(id, "msedge"), strings.HasPrefix(id, "edge"):
		return "Edge"
	case strings.HasPrefix(id, "chrome"):
		return "Chrome"
	case strings.HasPrefix(id, "firefox"):
		return "Firefox"
	case strings.HasPrefix(id, "safari"):
		return "Safari"
	case strings.HasPrefix(id, "opera"):
		return "Opera"
	case strings.HasPrefix(id, "msie"):
		return "Internet Explorer"
	case strings.HasPrefix(id, "samsung"):
		return "Samsung Internet"
	case id == "Unknown" || id == "unknown":
		return "Unknown"
	}
	return id
}

// System is the friendly operating system name (the id's prefix).
func System(id string) string {
	switch {
	case strings.HasPrefix(id, "win"):
		return "Windows"
	case strings.HasPrefix(id, "macos"):
		return "macOS"
	case strings.HasPrefix(id, "ios"):
		return "iOS"
	case strings.HasPrefix(id, "android"):
		return "Android"
	case strings.HasPrefix(id, "linux"):
		return "Linux"
	case strings.HasPrefix(id, "cros"):
		return "ChromeOS"
	case strings.HasPrefix(id, "bsd"):
		return "BSD"
	case id == "Unknown" || id == "unknown":
		return "Unknown"
	}
	return id
}

// MonthName is the month's label ("September 2026"); the month is YYYYMM.
func MonthName(m string) string {
	if len(m) != 6 {
		return m
	}
	y, _ := strconv.Atoi(m[0:4])
	mon, _ := strconv.Atoi(m[4:6])
	if y < 1970 || mon < 1 || mon > 12 {
		return m
	}
	return time.Date(y, time.Month(mon), 1, 0, 0, 0, 0, time.UTC).Format("January 2006")
}

// DayLabel is the day's table label ("Tue 1"); the day is YYYYMMDD.
func DayLabel(day string) string {
	if len(day) != 8 {
		return day
	}
	y, _ := strconv.Atoi(day[0:4])
	mon, _ := strconv.Atoi(day[4:6])
	d, _ := strconv.Atoi(day[6:8])
	if y < 1970 || mon < 1 || mon > 12 || d < 1 || d > 31 {
		return day
	}
	return time.Date(y, time.Month(mon), d, 0, 0, 0, 0, time.UTC).Format("Mon 2")
}

// Bar is the PHP view's bar width: round up to the next 5 percent (0 when
// the max is 0) — ceil(v*100/max/5)*5.
func Bar(v, max int) int {
	if max <= 0 {
		return 0
	}
	return int(math.Ceil(float64(v)*100/float64(max)/5)) * 5
}
