package dnsmgr

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// Importing a zone file (BIND format, as exported by most DNS hosts): the
// records NimVor supports come back as panel records; SOA and anything
// unsupported is reported as skipped. $ORIGIN and $TTL are honoured;
// $INCLUDE and $GENERATE are not.

// Skipped is a line that was not imported.
type Skipped struct {
	Line   int    `json:"line"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

// ParseResult is what a zone file yields.
type ParseResult struct {
	Records []Record  `json:"records"`
	Skipped []Skipped `json:"skipped"`
}

// tokenize splits a line into fields, keeping quoted strings (with their
// quotes) together and dropping comments.
func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	inQ, esc, has := false, false, false
	flush := func() {
		if has {
			out = append(out, cur.String())
			cur.Reset()
			has = false
		}
	}
	for _, c := range s {
		switch {
		case esc:
			cur.WriteRune(c)
			esc = false
		case c == '\\':
			cur.WriteRune(c)
			esc, has = true, true
		case c == '"':
			cur.WriteRune(c)
			inQ, has = !inQ, true
		case inQ:
			cur.WriteRune(c)
		case c == ';':
			flush()
			return out
		case c == ' ' || c == '\t':
			flush()
		default:
			cur.WriteRune(c)
			has = true
		}
	}
	flush()
	return out
}

// unquote joins TXT character strings.
func unquote(fields []string) string {
	var b strings.Builder
	for _, f := range fields {
		if strings.HasPrefix(f, `"`) && strings.HasSuffix(f, `"`) && len(f) >= 2 {
			f = f[1 : len(f)-1]
		}
		f = strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(f)
		b.WriteString(f)
	}
	return b.String()
}

var classes = map[string]bool{"IN": true, "CH": true, "HS": true}

// ParseZoneFile reads zone text for zone (e.g. "example.com").
func ParseZoneFile(text, zone string) ParseResult {
	zone = strings.ToLower(strings.TrimSuffix(zone, "."))
	origin := zone
	res := ParseResult{Records: []Record{}, Skipped: []Skipped{}}
	defaultTTL := 0
	last := "@"
	relName := func(n string) (string, bool) {
		n = strings.ToLower(n)
		switch {
		case n == "@":
			n = origin + "."
		case !strings.HasSuffix(n, "."):
			n = n + "." + origin + "."
		}
		n = strings.TrimSuffix(n, ".")
		if n == zone {
			return "@", true
		}
		if strings.HasSuffix(n, "."+zone) {
			return strings.TrimSuffix(n, "."+zone), true
		}
		return "", false
	}
	relTarget := func(t string) string {
		t = strings.ToLower(t)
		if t == "@" {
			return origin + "."
		}
		if strings.HasSuffix(t, ".") {
			return t
		}
		return t + "." + origin + "."
	}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	lineNo := 0
	var pending []string
	pendingLine, depth := 0, 0
	skip := func(n int, fields []string, why string) {
		if len(res.Skipped) < 500 {
			res.Skipped = append(res.Skipped, Skipped{Line: n, Text: strings.Join(fields, " "), Reason: why})
		}
	}
	for sc.Scan() {
		lineNo++
		raw := sc.Text()
		startsBlank := raw != "" && (raw[0] == ' ' || raw[0] == '\t')
		fields := tokenize(raw)
		// Parentheses join lines (SOA and long records).
		for _, f := range fields {
			if f == "(" || strings.HasPrefix(f, "(") {
				depth++
			}
			if f == ")" || strings.HasSuffix(f, ")") {
				depth--
			}
		}
		if depth > 0 || pending != nil {
			if pending == nil {
				pendingLine = lineNo
				if startsBlank {
					pending = []string{""}
				}
			}
			pending = append(pending, fields...)
			if depth > 0 {
				continue
			}
			fields, startsBlank = pending, pending[0] == ""
			if startsBlank {
				fields = fields[1:]
			}
			pending = nil
		} else {
			pendingLine = lineNo
		}
		var clean []string
		for _, f := range fields {
			f = strings.Trim(f, "()")
			if f != "" {
				clean = append(clean, f)
			}
		}
		fields = clean
		if len(fields) == 0 {
			continue
		}
		n := pendingLine
		switch strings.ToUpper(fields[0]) {
		case "$ORIGIN":
			if len(fields) > 1 {
				origin = strings.ToLower(strings.TrimSuffix(fields[1], "."))
			}
			continue
		case "$TTL":
			if len(fields) > 1 {
				defaultTTL, _ = strconv.Atoi(fields[1])
			}
			continue
		case "$INCLUDE", "$GENERATE":
			skip(n, fields, fields[0]+" is not supported")
			continue
		}
		name := last
		if !startsBlank {
			name, fields = fields[0], fields[1:]
			last = name
		}
		ttl := 0
		for len(fields) > 0 {
			if v, err := strconv.Atoi(fields[0]); err == nil {
				ttl, fields = v, fields[1:]
			} else if classes[strings.ToUpper(fields[0])] {
				fields = fields[1:]
			} else {
				break
			}
		}
		if len(fields) < 2 {
			skip(n, fields, "incomplete record")
			continue
		}
		typ, data := strings.ToUpper(fields[0]), fields[1:]
		rel, ok := relName(name)
		if !ok {
			skip(n, append([]string{name}, fields...), "outside the zone "+zone)
			continue
		}
		if ttl == defaultTTL {
			ttl = 0 // the zone's default
		}
		r := Record{Name: rel, Type: typ, TTL: ttl}
		atoi := func(s string) int { v, _ := strconv.Atoi(s); return v }
		switch typ {
		case "SOA":
			continue // NimVor writes its own
		case "A", "AAAA":
			r.Value = data[0]
		case "CNAME", "NS", "PTR":
			r.Value = relTarget(data[0])
		case "MX":
			if len(data) < 2 {
				skip(n, fields, "MX needs a priority and a host")
				continue
			}
			r.Priority, r.Value = atoi(data[0]), relTarget(data[1])
		case "SRV":
			if len(data) < 4 {
				skip(n, fields, "SRV needs priority, weight, port and target")
				continue
			}
			r.Priority, r.Weight, r.Port = atoi(data[0]), atoi(data[1]), atoi(data[2])
			if data[3] == "." {
				r.Value = "."
			} else {
				r.Value = relTarget(data[3])
			}
		case "TXT", "SPF":
			r.Type, r.Value = "TXT", unquote(data)
		case "CAA":
			if len(data) < 3 {
				skip(n, fields, "CAA needs flags, tag and value")
				continue
			}
			r.Priority, r.Value = atoi(data[0]), strings.ToLower(data[1])+" "+unquote(data[2:])
		default:
			skip(n, fields, fmt.Sprintf("%s records are not supported", typ))
			continue
		}
		if err := r.validate(); err != nil {
			skip(n, append([]string{name}, fields...), err.Error())
			continue
		}
		if len(res.Records) >= maxRecords {
			skip(n, fields, "too many records")
			continue
		}
		res.Records = append(res.Records, r)
	}
	return res
}
