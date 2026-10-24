package hosting

// The known vulnerabilities of an installation's WordPress version, plugins
// and themes, from the free WPVulnerability.net database
// (WordPressVulnerabilities.php): public data, no account, the panel sends
// only names and version numbers. Answers are cached for twelve hours per
// item. When the database cannot be reached the check says so instead of
// reporting "no vulnerabilities".

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

const (
	wpVulnAPI = "https://www.wpvulnerability.net/"
	wpVulnTTL = 43200 * time.Second
)

var (
	wpVulnSeverity    = map[string]string{"c": "critical", "h": "high", "m": "medium", "l": "low", "n": "none"}
	wpVulnSlugRe      = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	wpVulnVersionRe   = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,40}$`)
	wpVulnCveRe       = regexp.MustCompile(`^CVE-\d{4}-\d{3,}$`)
	wpVulnLinkRe      = regexp.MustCompile(`^https://[^\s"<>]{1,300}$`)
	wpVulnScoreRe     = regexp.MustCompile(`^\d{1,2}(\.\d)?$`)
	wpVulnTagRe       = regexp.MustCompile(`<[^>]*>`)
)

// WpVulnFetch returns the body of the url, or "" when it cannot be reached
// (https only, 10 s, at most 4 MB). Overridable in tests.
var WpVulnFetch = defaultWpVulnFetch

func defaultWpVulnFetch(rawurl string) (string, bool) {
	req, err := http.NewRequest(http.MethodGet, rawurl, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "WhaCP WordPress toolkit")
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil || len(body) >= 4<<20 {
		return "", false
	}
	return string(body), true
}

type wpVulnOperator struct {
	MinOperator string `json:"min_operator"`
	MinVersion  string `json:"min_version"`
	MaxOperator string `json:"max_operator"`
	MaxVersion  string `json:"max_version"`
	Unfixed     string `json:"unfixed"`
}

type wpVulnSource struct {
	ID   string `json:"id"`
	Link string `json:"link"`
}

type wpVulnEntry struct {
	Name     string         `json:"name"`
	Operator wpVulnOperator `json:"operator"`
	Impact   struct {
		CVSS struct {
			Severity string `json:"severity"`
			Score    string `json:"score"`
		} `json:"cvss"`
	} `json:"impact"`
	Source   []wpVulnSource `json:"source"`
}

type wpVulnTarget struct{ kind, slug, version, title string }

// WpVulnerabilities checks an installation against the vulnerability
// database.
type WpVulnerabilities struct {
	cacheDir string // '' = no cache (the answers are always fetched)
}

// NewWpVulnerabilities builds the checker; cacheDir holds the per-item
// answer caches ('' disables the cache).
func NewWpVulnerabilities(cacheDir string) *WpVulnerabilities {
	return &WpVulnerabilities{cacheDir: cacheDir}
}

// Check is the vulnerabilities of the installation's core, its non-must-use
// plugins and its themes, with whether the database was reachable
// (WordPressVulnerabilities::check).
func (s *WpVulnerabilities) Check(info *WordPressInfo) ([]WpVulnItem, bool) {
	items := []WpVulnItem{}
	reachable := true
	targets := []wpVulnTarget{{kind: "core", slug: "wordpress", version: info.Version, title: "WordPress"}}
	for _, p := range info.Plugins {
		if !p.MustUse {
			title := p.Title
			if title == "" {
				title = p.Name
			}
			targets = append(targets, wpVulnTarget{kind: "plugin", slug: p.Name, version: p.Version, title: title})
		}
	}
	for _, th := range info.Themes {
		title := th.Title
		if title == "" {
			title = th.Name
		}
		targets = append(targets, wpVulnTarget{kind: "theme", slug: th.Name, version: th.Version, title: title})
	}
	if len(targets) > 300 {
		targets = targets[:300]
	}
	for _, t := range targets {
		if t.version == "" || !wpVulnSlugRe.MatchString(t.slug) || !wpVulnVersionRe.MatchString(t.version) {
			continue
		}
		path := t.kind + "/" + url.PathEscape(t.slug) + "/"
		if t.kind == "core" {
			path = "core/" + url.PathEscape(t.version) + "/"
		}
		data, ok := s.get(path)
		if !ok {
			reachable = false
			continue
		}
		for _, v := range WpVulnAffecting(data, t.version) {
			v.Kind = t.kind
			v.Slug = t.slug
			v.Title = t.title
			v.Version = t.version
			items = append(items, v)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		ri, rj := wpVulnRank(items[i].Severity), wpVulnRank(items[j].Severity)
		if ri != rj {
			return ri < rj
		}
		return items[i].Title < items[j].Title
	})
	return items, reachable
}

// get is the answer's data (an empty list when the item is unknown) and
// whether the database was reached (WordPressVulnerabilities::get).
func (s *WpVulnerabilities) get(path string) ([]wpVulnEntry, bool) {
	var file string
	if s.cacheDir != "" {
		sum := sha1.Sum([]byte(path))
		file = filepath.Join(s.cacheDir, hex.EncodeToString(sum[:])+".json")
		if st, err := os.Stat(file); err == nil && time.Since(st.ModTime()) < wpVulnTTL {
			if b, err := os.ReadFile(file); err == nil {
				var entries []wpVulnEntry
				if err := json.Unmarshal(b, &entries); err == nil {
					return entries, true
				}
			}
		}
	}
	body, ok := WpVulnFetch(wpVulnAPI + path)
	if !ok {
		return nil, false
	}
	var o struct {
		Data struct {
			Vulnerability []wpVulnEntry `json:"vulnerability"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &o); err != nil {
		return nil, false
	}
	entries := o.Data.Vulnerability
	if entries == nil {
		entries = []wpVulnEntry{}
	}
	if s.cacheDir != "" {
		if b, err := json.Marshal(entries); err == nil {
			_ = os.MkdirAll(s.cacheDir, 0o700)
			_ = os.WriteFile(file, b, 0o600)
		}
	}
	return entries, true
}

// WpVulnAffecting are the vulnerabilities in data that affect version (the
// core answers are per version already: version '' = all)
// (WordPressVulnerabilities::affecting). The caller adds the target's
// kind / slug / title / version.
func WpVulnAffecting(data []wpVulnEntry, version string) []WpVulnItem {
	out := []WpVulnItem{}
	if len(data) > 500 {
		data = data[:500]
	}
	for _, v := range data {
		if version != "" && !wpVulnMatches(version, v.Operator) {
			continue
		}
		cve, link := "", ""
		for _, src := range v.Source {
			if cve == "" && wpVulnCveRe.MatchString(src.ID) {
				cve = src.ID
			}
			if link == "" && wpVulnLinkRe.MatchString(src.Link) {
				link = src.Link
			}
		}
		name := v.Name
		if name == "" {
			name = "Vulnerability"
		}
		name = wpVulnClampRune(wpVulnTagRe.ReplaceAllString(name, ""), 300)
		out = append(out, WpVulnItem{
			Severity: wpVulnSeverity[v.Impact.CVSS.Severity],
			Score:    wpVulnScore(v.Impact.CVSS.Score),
			Name:     name,
			FixedIn:  wpVulnFixedIn(v.Operator),
			Unfixed:  v.Operator.Unfixed == "1",
			Cve:      cve,
			Link:     link,
		})
	}
	return out
}

// wpVulnMatches is the min / max operator gate
// (WordPressVulnerabilities::matches).
func wpVulnMatches(version string, op wpVulnOperator) bool {
	sides := [][2]string{{op.MinOperator, op.MinVersion}, {op.MaxOperator, op.MaxVersion}}
	for _, side := range sides {
		o, v := side[0], side[1]
		if o == "" || v == "" {
			continue
		}
		cmp := wpVersionCompare(version, v)
		switch o {
		case "lt":
			if cmp >= 0 {
				return false
			}
		case "le":
			if cmp > 0 {
				return false
			}
		case "gt":
			if cmp <= 0 {
				return false
			}
		case "ge":
			if cmp < 0 {
				return false
			}
		case "eq":
			if cmp != 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func wpVulnRank(severity string) int {
	switch severity {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	case "none":
		return 4
	default:
		return 5
	}
}

func wpVulnScore(score string) string {
	if wpVulnScoreRe.MatchString(score) {
		return score
	}
	return ""
}

func wpVulnFixedIn(op wpVulnOperator) string {
	if op.MaxOperator == "lt" && wpVulnVersionRe.MatchString(op.MaxVersion) {
		return op.MaxVersion
	}
	return ""
}

func wpVulnClampRune(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
