package mailmgr

import (
	"bufio"
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Mail statistics and delivery reports from Postfix's and Dovecot's log
// lines (the journal, or /var/log/mail.log / maillog). Only what the log
// says: counts, delivery records, who sent mail, login results.

// LogLine is one syslog message.
type LogLine struct {
	Time  time.Time
	Ident string // e.g. postfix/smtp, postfix/submission/smtpd, dovecot
	Msg   string
}

// Delivery is one recipient's delivery attempt.
type Delivery struct {
	Time   time.Time `json:"time"`
	ID     string    `json:"id"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	Relay  string    `json:"relay"`
	Status string    `json:"status"` // sent, delivered (locally), deferred, bounced, rejected
	DSN    string    `json:"dsn"`
	Delay  string    `json:"delay"`
	Reason string    `json:"reason"`
}

// Relayer is who submitted mail: an authenticated account, a local system
// account (e.g. a website's PHP) or a client address.
type Relayer struct {
	Kind     string `json:"kind"` // sasl, local, client
	Name     string `json:"name"`
	Messages int    `json:"messages"`
}

// Report is the result of reading a time range.
type Report struct {
	Totals     map[string]int   `json:"totals"` // received, sent, delivered, deferred, bounced, rejected, auth_ok, auth_failed
	Series     []map[string]any `json:"series"` // per bucket: start + counts
	Deliveries []Delivery       `json:"deliveries"`
	Relayers   []Relayer        `json:"relayers"`
	Senders    []Relayer        `json:"senders"` // sent mail per sender domain
	Truncated  bool             `json:"truncated"`
}

var (
	qidRe      = `([0-9A-Za-z]{6,20})`
	qmgrFromRe = regexp.MustCompile(`^` + qidRe + `: from=<([^>]*)>, size=\d+, nrcpt=(\d+)`)
	deliveryRe = regexp.MustCompile(`^` + qidRe + `: to=<([^>]*)>,(?: orig_to=<[^>]*>,)? relay=([^,]*), .*?delay=([0-9.]+), .*?dsn=([0-9.]+), status=(sent|deferred|bounced|expired) ?\(?(.*?)\)?$`)
	rejectRe   = regexp.MustCompile(`^NOQUEUE: reject: \S+ from ([^:]*): (\d{3} [0-9.]+ .*?); from=<([^>]*)> to=<([^>]*)>`)
	clientRe   = regexp.MustCompile(`^` + qidRe + `: client=([^,\s]+)(?:, sasl_method=\S+, sasl_username=(\S+))?`)
	pickupRe   = regexp.MustCompile(`^` + qidRe + `: uid=(\d+) from=<([^>]*)>`)
	saslFailRe = regexp.MustCompile(`SASL \S+ authentication failed`)
	doveOkRe   = regexp.MustCompile(`^(imap|pop3|submission)-login: Login: user=<`)
	doveFailRe = regexp.MustCompile(`^(imap|pop3|submission)-login: .*\(auth failed`)
)

// ParseJournal reads journalctl -o json lines.
func ParseJournal(out []byte) []LogLine {
	var lines []LogLine
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var j struct {
			Msg   any    `json:"MESSAGE"`
			Ident string `json:"SYSLOG_IDENTIFIER"`
			TS    string `json:"__REALTIME_TIMESTAMP"`
		}
		if json.Unmarshal(sc.Bytes(), &j) != nil {
			continue
		}
		msg, ok := j.Msg.(string)
		if !ok {
			continue
		}
		us, _ := strconv.ParseInt(j.TS, 10, 64)
		lines = append(lines, LogLine{Time: time.UnixMicro(us).UTC(), Ident: j.Ident, Msg: msg})
	}
	return lines
}

var (
	isoLine  = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})) \S+ ([^\[:]+)(?:\[\d+\])?: (.*)$`)
	bsdLine  = regexp.MustCompile(`^([A-Z][a-z]{2} [ 0-9]\d \d{2}:\d{2}:\d{2}) \S+ ([^\[:]+)(?:\[\d+\])?: (.*)$`)
	isoForms = []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999-0700", "2006-01-02T15:04:05-0700"}
)

// ParseSyslog reads plain log lines (RFC 3339 or traditional timestamps;
// traditional ones get the year of now, or the previous one if that
// would be in the future).
func ParseSyslog(out []byte, now time.Time) []LogLine {
	var lines []LogLine
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		s := sc.Text()
		if m := isoLine.FindStringSubmatch(s); m != nil {
			for _, f := range isoForms {
				if t, err := time.Parse(f, m[1]); err == nil {
					lines = append(lines, LogLine{Time: t.UTC(), Ident: m[2], Msg: m[3]})
					break
				}
			}
		} else if m := bsdLine.FindStringSubmatch(s); m != nil {
			t, err := time.ParseInLocation("2006 Jan _2 15:04:05", strconv.Itoa(now.Year())+" "+m[1], time.Local)
			if err != nil {
				continue
			}
			if t.After(now.Add(24 * time.Hour)) {
				t = t.AddDate(-1, 0, 0)
			}
			lines = append(lines, LogLine{Time: t.UTC(), Ident: m[2], Msg: m[3]})
		}
	}
	return lines
}

// Analyze builds the report of [since, until). Deliveries are filtered by
// address (sender or recipient contains it) and status, newest first.
func Analyze(lines []LogLine, since, until time.Time, address, status string, limit int) Report {
	rep := Report{Totals: map[string]int{}}
	bucket := time.Hour
	if until.Sub(since) > 48*time.Hour {
		bucket = 24 * time.Hour
	}
	series := map[int64]map[string]int{}
	add := func(t time.Time, key string) {
		rep.Totals[key]++
		b := t.Truncate(bucket).Unix()
		if series[b] == nil {
			series[b] = map[string]int{}
		}
		series[b][key]++
	}
	from := map[string]string{}
	relay := map[string]int{}
	senders := map[string]int{}
	seenReceived := map[string]bool{}
	address = strings.ToLower(strings.TrimSpace(address))
	for _, l := range lines {
		if l.Time.Before(since) || !l.Time.Before(until) {
			continue
		}
		switch {
		case strings.HasPrefix(l.Ident, "postfix"):
			if m := qmgrFromRe.FindStringSubmatch(l.Msg); m != nil {
				from[m[1]] = m[2]
				if !seenReceived[m[1]] {
					seenReceived[m[1]] = true
					add(l.Time, "received")
				}
				continue
			}
			if m := clientRe.FindStringSubmatch(l.Msg); m != nil {
				if m[3] != "" {
					relay["sasl\x00"+m[3]]++
				} else if !strings.HasPrefix(m[2], "localhost[") && !strings.Contains(m[2], "[127.0.0.1]") && !strings.Contains(m[2], "[::1]") {
					relay["client\x00"+m[2]]++
				}
				continue
			}
			if m := pickupRe.FindStringSubmatch(l.Msg); m != nil {
				relay["local\x00uid "+m[2]+" ("+m[3]+")"]++
				continue
			}
			if m := deliveryRe.FindStringSubmatch(l.Msg); m != nil {
				d := Delivery{Time: l.Time, ID: m[1], From: from[m[1]], To: m[2], Relay: m[3], Delay: m[4], DSN: m[5], Status: m[6], Reason: m[7]}
				if d.Status == "expired" {
					d.Status = "bounced"
				}
				if d.Status == "sent" {
					if strings.Contains(d.Relay, "lmtp") || strings.HasPrefix(l.Ident, "postfix/lmtp") || strings.HasPrefix(l.Ident, "postfix/local") || strings.HasPrefix(l.Ident, "postfix/virtual") {
						d.Status = "delivered"
					} else if _, dom, ok := strings.Cut(d.From, "@"); ok {
						senders[strings.ToLower(dom)]++
					}
				}
				add(l.Time, d.Status)
				rep.Deliveries = appendDelivery(rep.Deliveries, d, address, status)
				continue
			}
			if m := rejectRe.FindStringSubmatch(l.Msg); m != nil {
				add(l.Time, "rejected")
				rep.Deliveries = appendDelivery(rep.Deliveries, Delivery{Time: l.Time, From: m[3], To: m[4], Relay: m[1], Status: "rejected", DSN: strings.Fields(m[2] + " x")[1], Reason: m[2]}, address, status)
				continue
			}
			if saslFailRe.MatchString(l.Msg) {
				add(l.Time, "auth_failed")
			}
		case l.Ident == "dovecot":
			if doveOkRe.MatchString(l.Msg) {
				add(l.Time, "auth_ok")
			} else if doveFailRe.MatchString(l.Msg) {
				add(l.Time, "auth_failed")
			}
		}
	}
	keys := make([]int64, 0, len(series))
	for k := range series {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		row := map[string]any{"start": time.Unix(k, 0).UTC()}
		for n, c := range series[k] {
			row[n] = c
		}
		rep.Series = append(rep.Series, row)
	}
	sort.SliceStable(rep.Deliveries, func(i, j int) bool { return rep.Deliveries[i].Time.After(rep.Deliveries[j].Time) })
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if len(rep.Deliveries) > limit {
		rep.Deliveries = rep.Deliveries[:limit]
		rep.Truncated = true
	}
	rep.Relayers = topCounts(relay, 50)
	sd := map[string]int{}
	for k, v := range senders {
		sd["domain\x00"+k] = v
	}
	rep.Senders = topCounts(sd, 50)
	if rep.Deliveries == nil {
		rep.Deliveries = []Delivery{}
	}
	return rep
}

func appendDelivery(list []Delivery, d Delivery, address, status string) []Delivery {
	if status != "" && d.Status != status {
		return list
	}
	if address != "" && !strings.Contains(strings.ToLower(d.From), address) && !strings.Contains(strings.ToLower(d.To), address) {
		return list
	}
	// Keep memory bounded on large logs (newest win when sorted later).
	if len(list) >= 20000 {
		list = list[1:]
	}
	return append(list, d)
}

func topCounts(m map[string]int, n int) []Relayer {
	out := []Relayer{}
	for k, v := range m {
		kind, name, _ := strings.Cut(k, "\x00")
		out = append(out, Relayer{Kind: kind, Name: name, Messages: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Messages != out[j].Messages {
			return out[i].Messages > out[j].Messages
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
