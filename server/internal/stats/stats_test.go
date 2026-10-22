package stats

// Unit tests for the AWStats data-file parsing (StatsService::parse and
// friends): the sections, the totals, the top lists, the label mapping,
// the merge, the month/day labels and the bar widths.

import (
	"reflect"
	"testing"
)

const fixture = `# file awstats092026.bob.example.txt
BEGIN_GENERAL
LastUpdate 20260927153000
TotalUnique 42
TotalVisits 250
END_GENERAL
BEGIN_DAY
20260902 140 300 42000 11
not-a-day 9 9 9 9
20260901 150 320 45000 12
END_DAY
BEGIN_TIME
0 10 20 1000
1 12 25 1500
3 0 0 0
24 99 99 99
END_TIME
BEGIN_SIDER
/index.php 250
/admin/ 120
END_SIDER
BEGIN_PAGEREFS
https://example.com/ 80
https://partner.example 40
END_PAGEREFS
BEGIN_SEREFERRALS
www.google_com 60
bing_com 10
END_SEREFERRALS
BEGIN_BROWSER
chrome_119 50
chrome_120 100
firefox_121 30
END_BROWSER
BEGIN_OS
win_11 120
macos_15 40
END_OS
BEGIN_DOMAIN
us 100
de 50
ip 20
END_DOMAIN
BEGIN_ERRORS
404_not_found 5
END_ERRORS
BEGIN_UNKNOWN
1 2 3
END_UNKNOWN
`

func TestParseFixture(t *testing.T) {
	r := Parse(fixture)
	if r.Unique != 42 {
		t.Errorf("unique = %d, want 42", r.Unique)
	}
	// visits = max(sum(days)=25, TotalVisits=250)
	if r.Visits != 250 {
		t.Errorf("visits = %d, want 250", r.Visits)
	}
	if r.Pages != 290 || r.Hits != 620 || r.Bytes != 87000 {
		t.Errorf("pages/hits/bytes = %d/%d/%d, want 290/620/87000", r.Pages, r.Hits, r.Bytes)
	}
	if r.Updated != "2026-09-27T15:30:00Z" {
		t.Errorf("updated = %q", r.Updated)
	}
	// The days are ksorted; the malformed row is skipped.
	wantDays := []DayRow{
		{Day: "20260901", Visits: 12, Pages: 150, Hits: 320, Bytes: 45000},
		{Day: "20260902", Visits: 11, Pages: 140, Hits: 300, Bytes: 42000},
	}
	if !reflect.DeepEqual(r.Days, wantDays) {
		t.Errorf("days = %#v", r.Days)
	}
	if r.Hours[0] != (HourRow{Pages: 10, Hits: 20, Bytes: 1000}) || r.Hours[1] != (HourRow{Pages: 12, Hits: 25, Bytes: 1500}) {
		t.Errorf("hours[0..1] = %#v", r.Hours[0:2])
	}
	if r.Hours[2].Pages != 0 || r.Hours[3] != (HourRow{}) {
		t.Errorf("hours[2..3] = %#v (24 is out of range and stays zero)", r.Hours[2:4])
	}
	if want := []TopRow{{"/index.php", 250}, {"/admin/", 120}}; !reflect.DeepEqual(r.PagesTop, want) {
		t.Errorf("pages_top = %#v", r.PagesTop)
	}
	if want := []TopRow{{"https://example.com/", 80}, {"https://partner.example", 40}}; !reflect.DeepEqual(r.Referrers, want) {
		t.Errorf("referrers = %#v", r.Referrers)
	}
	if want := []TopRow{{"google.com", 60}, {"bing.com", 10}}; !reflect.DeepEqual(r.Search, want) {
		t.Errorf("search = %#v", r.Search)
	}
	if want := []TopRow{{"Chrome", 150}, {"Firefox", 30}}; !reflect.DeepEqual(r.Browsers, want) {
		t.Errorf("browsers = %#v", r.Browsers)
	}
	if want := []TopRow{{"Windows", 120}, {"macOS", 40}}; !reflect.DeepEqual(r.Systems, want) {
		t.Errorf("systems = %#v", r.Systems)
	}
	if want := []TopRow{{"US", 100}, {"DE", 50}, {"Unknown", 20}}; !reflect.DeepEqual(r.Countries, want) {
		t.Errorf("countries = %#v", r.Countries)
	}
	if want := []TopRow{{"404_not_found", 5}}; !reflect.DeepEqual(r.Errors, want) {
		t.Errorf("errors = %#v", r.Errors)
	}
}

func TestParseEmptyAndMalformed(t *testing.T) {
	r := Parse("")
	if len(r.Days) != 0 || r.Visits != 0 || r.PagesTop != nil {
		t.Errorf("empty input = %#v", r)
	}
	// A section without END_ swallows the rest (as in PHP); rows before
	// any section are skipped.
	r = Parse("1 2 3\nBEGIN_DAY\n20260901 5 6 7 8\nBEGIN_TIME\n1 2 3 4")
	if len(r.Days) != 1 || r.Days[0].Visits != 8 {
		t.Errorf("unterminated section = %#v", r.Days)
	}
}

func TestTopLimitAndStableOrder(t *testing.T) {
	data := "BEGIN_SIDER\na 1\nb 2\nc 2\nd 9\n"
	// The SIDER limit is 25, so all four rows stay; the ties (b, c) keep
	// their file order.
	r := Parse(data)
	want := []TopRow{{"d", 9}, {"b", 2}, {"c", 2}, {"a", 1}}
	if !reflect.DeepEqual(r.PagesTop, want) {
		t.Errorf("top = %#v", r.PagesTop)
	}
}

func TestMerge(t *testing.T) {
	rows := []TopRow{{"a", 1}, {"b", 2}, {"a", 3}, {"c", 2}, {"b", 0}}
	got := Merge(rows, 2)
	want := []TopRow{{"a", 4}, {"b", 2}} // b and c tie; first appearance wins
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merge = %#v", got)
	}
}

func TestBrowserSystem(t *testing.T) {
	for id, want := range map[string]string{
		"chrome_120": "Chrome", "msedge_1": "Edge", "edge_2": "Edge", "msie_9": "Internet Explorer",
		"samsung_1": "Samsung Internet", "safari_17": "Safari", "opera_12": "Opera", "Unknown": "Unknown",
		"unknown": "Unknown", "conkeror": "conkeror",
	} {
		if got := Browser(id); got != want {
			t.Errorf("Browser(%q) = %q, want %q", id, got, want)
		}
	}
	for id, want := range map[string]string{
		"win_11": "Windows", "macos_15": "macOS", "ios_18": "iOS", "android_15": "Android",
		"linux_debian": "Linux", "cros_120": "ChromeOS", "bsd_free": "BSD", "unknown": "Unknown", "sunos": "sunos",
	} {
		if got := System(id); got != want {
			t.Errorf("System(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestLabelsAndBar(t *testing.T) {
	if got := MonthName("202609"); got != "September 2026" {
		t.Errorf("MonthName = %q", got)
	}
	if got := MonthName("bad"); got != "bad" {
		t.Errorf("MonthName(bad) = %q", got)
	}
	if got := DayLabel("20260901"); got != "Tue 1" {
		t.Errorf("DayLabel = %q", got)
	}
	if got := Stamp("20260927153000"); got != "2026-09-27T15:30:00Z" {
		t.Errorf("Stamp = %q", got)
	}
	if got := Stamp("nope"); got != "" {
		t.Errorf("Stamp(nope) = %q", got)
	}
	if Bar(0, 0) != 0 || Bar(1, 100) != 5 || Bar(50, 100) != 50 || Bar(51, 100) != 55 || Bar(100, 100) != 100 {
		t.Errorf("bar: %d %d %d %d %d", Bar(0, 0), Bar(1, 100), Bar(50, 100), Bar(51, 100), Bar(100, 100))
	}
}
