package mailmgr

import (
	"strings"
	"testing"
	"time"
)

// Lines as Postfix 3.x and Dovecot 2.3 write them.
const sampleLog = `2026-09-27T10:00:01.000000+00:00 mx postfix/smtpd[100]: 1A2B3C4D5E: client=mail-ot1.example.net[198.51.100.7]
2026-09-27T10:00:01.100000+00:00 mx postfix/qmgr[90]: 1A2B3C4D5E: from=<news@example.net>, size=2048, nrcpt=1 (queue active)
2026-09-27T10:00:01.300000+00:00 mx postfix/lmtp[101]: 1A2B3C4D5E: to=<me@shop.test>, relay=mx[private/dovecot-lmtp], delay=0.2, delays=0.1/0/0/0.1, dsn=2.0.0, status=sent (250 2.0.0 <me@shop.test> Saved)
2026-09-27T10:05:00.000000+00:00 mx postfix/submission/smtpd[110]: 6F7A8B9C0D: client=unknown[203.0.113.9], sasl_method=PLAIN, sasl_username=me@shop.test
2026-09-27T10:05:00.100000+00:00 mx postfix/qmgr[90]: 6F7A8B9C0D: from=<me@shop.test>, size=900, nrcpt=2 (queue active)
2026-09-27T10:05:01.000000+00:00 mx postfix/smtp[111]: 6F7A8B9C0D: to=<friend@gmail.com>, relay=gmail-smtp-in.l.google.com[142.250.1.1]:25, delay=0.9, delays=0.1/0/0.3/0.5, dsn=2.0.0, status=sent (250 2.0.0 OK)
2026-09-27T10:05:02.000000+00:00 mx postfix/smtp[111]: 6F7A8B9C0D: to=<x@nowhere.invalid>, relay=none, delay=1.9, delays=0.1/0/1.8/0, dsn=4.4.3, status=deferred (Host or domain name not found. Name service error for name=nowhere.invalid type=MX: Host not found, try again)
2026-09-27T11:00:00.000000+00:00 mx postfix/pickup[120]: 1111AAAA22: uid=1001 from=<shop@mx>
2026-09-27T11:00:00.100000+00:00 mx postfix/qmgr[90]: 1111AAAA22: from=<shop@mx>, size=500, nrcpt=1 (queue active)
2026-09-27T11:00:01.000000+00:00 mx postfix/smtp[121]: 1111AAAA22: to=<gone@example.org>, relay=mx.example.org[192.0.2.5]:25, delay=1, delays=0/0/0.5/0.5, dsn=5.1.1, status=bounced (host mx.example.org[192.0.2.5] said: 550 5.1.1 User unknown (in reply to RCPT TO command))
2026-09-27T11:10:00.000000+00:00 mx postfix/smtpd[130]: NOQUEUE: reject: RCPT from unknown[192.0.2.66]: 554 5.7.1 <spam@shop.test>: Relay access denied; from=<a@b.c> to=<spam@other.test> proto=ESMTP helo=<x>
2026-09-27T11:11:00.000000+00:00 mx postfix/smtpd[130]: warning: unknown[192.0.2.66]: SASL LOGIN authentication failed: UGFzc3dvcmQ6
2026-09-27T11:12:00.000000+00:00 mx dovecot: imap-login: Login: user=<me@shop.test>, method=PLAIN, rip=203.0.113.9, lip=192.0.2.1, mpid=1, TLS
2026-09-27T11:13:00.000000+00:00 mx dovecot: imap-login: Disconnected (auth failed, 1 attempts in 2 secs): user=<me@shop.test>, method=PLAIN, rip=203.0.113.66
2026-09-26T09:00:00.000000+00:00 mx postfix/qmgr[90]: OLDOLD1234: from=<old@x.y>, size=1, nrcpt=1 (queue active)
`

func TestAnalyzeMailLog(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	lines := ParseSyslog([]byte(sampleLog), now)
	if len(lines) != 15 {
		t.Fatalf("parsed %d lines", len(lines))
	}
	rep := Analyze(lines, now.Add(-24*time.Hour), now, "", "", 0)
	want := map[string]int{"received": 3, "delivered": 1, "sent": 1, "deferred": 1, "bounced": 1, "rejected": 1, "auth_failed": 2, "auth_ok": 1}
	for k, v := range want {
		if rep.Totals[k] != v {
			t.Errorf("%s = %d, want %d (%v)", k, rep.Totals[k], v, rep.Totals)
		}
	}
	if len(rep.Series) != 2 {
		t.Errorf("hourly buckets: %d", len(rep.Series))
	}
	if rep.Deliveries[0].Status != "rejected" || rep.Deliveries[len(rep.Deliveries)-1].To != "me@shop.test" {
		t.Errorf("order: %+v", rep.Deliveries)
	}
	var bounced Delivery
	for _, d := range rep.Deliveries {
		if d.Status == "bounced" {
			bounced = d
		}
	}
	if bounced.From != "shop@mx" || bounced.DSN != "5.1.1" || !strings.Contains(bounced.Reason, "User unknown") {
		t.Errorf("bounce: %+v", bounced)
	}
	relayers := map[string]int{}
	for _, r := range rep.Relayers {
		relayers[r.Kind+":"+r.Name] = r.Messages
	}
	if relayers["sasl:me@shop.test"] != 1 || relayers["local:uid 1001 (shop@mx)"] != 1 || relayers["client:mail-ot1.example.net[198.51.100.7]"] != 1 {
		t.Errorf("relayers: %v", relayers)
	}
	if len(rep.Senders) != 1 || rep.Senders[0].Name != "shop.test" {
		t.Errorf("senders: %+v", rep.Senders)
	}
	// Filters.
	if r := Analyze(lines, now.Add(-24*time.Hour), now, "GMAIL.com", "", 0); len(r.Deliveries) != 1 || r.Deliveries[0].To != "friend@gmail.com" {
		t.Errorf("address filter: %+v", r.Deliveries)
	}
	if r := Analyze(lines, now.Add(-24*time.Hour), now, "", "deferred", 0); len(r.Deliveries) != 1 || !strings.Contains(r.Deliveries[0].Reason, "Host not found") {
		t.Errorf("status filter: %+v", r.Deliveries)
	}
	// A longer range groups by day and includes the older line.
	if r := Analyze(lines, now.Add(-72*time.Hour), now, "", "", 0); r.Totals["received"] != 4 || len(r.Series) != 2 {
		t.Errorf("daily: %v %d", r.Totals, len(r.Series))
	}
}

func TestParseTraditionalAndJournal(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	l := ParseSyslog([]byte("Dec 31 23:59:59 mx postfix/qmgr[1]: ABCDEF1234: from=<a@b.c>, size=1, nrcpt=1 (queue active)\n"), now)
	if len(l) != 1 || l[0].Time.Year() != 2025 || l[0].Ident != "postfix/qmgr" {
		t.Fatalf("traditional: %+v", l)
	}
	j := ParseJournal([]byte(`{"MESSAGE":"ABCDEF1234: from=<a@b.c>, size=1, nrcpt=1 (queue active)","SYSLOG_IDENTIFIER":"postfix/qmgr","__REALTIME_TIMESTAMP":"1790000000000000"}
{"MESSAGE":[1,2],"SYSLOG_IDENTIFIER":"x","__REALTIME_TIMESTAMP":"1"}
`))
	if len(j) != 1 || j[0].Time.Unix() != 1790000000 {
		t.Fatalf("journal: %+v", j)
	}
}
