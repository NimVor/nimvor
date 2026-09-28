package mailmgr

import "testing"

func TestParseQueue(t *testing.T) {
	out := []byte(`postqueue: warning: Mail system is down -- accessing queue directly
{"queue_name": "deferred", "queue_id": "7506CE0062", "arrival_time": 100, "message_size": 310, "forced_expire": false, "sender": "a@example.com", "recipients": [{"address": "x@y.invalid", "delay_reason": "Host not found"}]}
{"queue_name": "hold", "queue_id": "ABCDEF1234", "arrival_time": 200, "message_size": 10, "sender": "", "recipients": [{"address": "z@y.test"}, {"address": "w@y.test"}]}
{"queue_name": "active", "queue_id": "../../etc", "arrival_time": 300, "sender": "x"}
not json
`)
	list := ParseQueue(out)
	if len(list) != 2 || list[0].ID != "ABCDEF1234" || list[1].Reason != "Host not found" || len(list[0].Recipients) != 2 {
		t.Fatalf("parsed: %+v", list)
	}
}

func TestHeaderFields(t *testing.T) {
	h := headerFields([]byte("*** ENVELOPE RECORDS ***\nReceived: by vm\n\tid 1\nFrom: Tester <t@example.com>\nSubject: =?UTF-8?B?UXVldWUgdGVzdCDinJM=?=\n  continued\nX-Secret: nope\nTo: a@b.c\n"))
	if h["subject"] != "Queue test ✓ continued" || h["from"] != "Tester <t@example.com>" || h["to"] != "a@b.c" || h["x-secret"] != "" || h["received"] != "" {
		t.Fatalf("headers: %v", h)
	}
}
