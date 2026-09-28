package mailmgr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Mail queue management through Postfix's own tools: postqueue -j lists,
// postcat shows headers of one message, postsuper deletes, requeues,
// holds and releases (queue IDs on stdin), postqueue -f/-i retries.

// QueuedMessage is one message in Postfix's queue.
type QueuedMessage struct {
	Queue      string   `json:"queue"` // active, deferred, hold, incoming, maildrop
	ID         string   `json:"id"`
	Arrival    int64    `json:"arrival"`
	Size       int64    `json:"size"`
	Sender     string   `json:"sender"`
	Recipients []string `json:"recipients"`
	Reason     string   `json:"reason,omitempty"` // why delivery was deferred
}

var queueIDRe = regexp.MustCompile(`^[0-9A-Za-z]{6,20}$`)

// ParseQueue reads postqueue -j output (one JSON object per line).
func ParseQueue(out []byte) []QueuedMessage {
	var list []QueuedMessage
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var m struct {
			QueueName   string `json:"queue_name"`
			QueueID     string `json:"queue_id"`
			ArrivalTime int64  `json:"arrival_time"`
			Size        int64  `json:"message_size"`
			Sender      string `json:"sender"`
			Recipients  []struct {
				Address     string `json:"address"`
				DelayReason string `json:"delay_reason"`
			} `json:"recipients"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil || !queueIDRe.MatchString(m.QueueID) {
			continue
		}
		q := QueuedMessage{Queue: m.QueueName, ID: m.QueueID, Arrival: m.ArrivalTime, Size: m.Size, Sender: m.Sender, Recipients: []string{}}
		for _, r := range m.Recipients {
			q.Recipients = append(q.Recipients, r.Address)
			if q.Reason == "" && r.DelayReason != "" {
				q.Reason = r.DelayReason
			}
		}
		list = append(list, q)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Arrival > list[j].Arrival })
	return list
}

// headerFields picks the envelope-independent headers shown for a message.
func headerFields(out []byte) map[string]string {
	want := map[string]bool{"from": true, "to": true, "subject": true, "date": true, "message-id": true, "reply-to": true}
	res := map[string]string{}
	dec := new(mime.WordDecoder)
	var key string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "*** ") {
			continue // postcat section markers
		}
		if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && key != "" {
			if len(res[key]) < 1000 {
				res[key] += " " + strings.TrimSpace(line)
			}
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		key = ""
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		if want[k] {
			if _, seen := res[k]; !seen {
				key = k
				res[k] = strings.TrimSpace(v)
			}
		}
	}
	for k, v := range res {
		if d, err := dec.DecodeHeader(v); err == nil {
			v = d
		}
		if len(v) > 500 {
			v = v[:500]
		}
		res[k] = v
	}
	return res
}

func registerQueue(r *actions.Registry, e env) {
	r.Register(actions.Action{Name: "mail.queue", Description: "Postfix's mail queue: messages with queue, sender, recipients, size, age and the reason delivery was deferred (postqueue -j).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		out, err := e.Runner.Run(ctx, "postqueue", "-j")
		if err != nil {
			return nil, actions.Failed("The mail queue could not be read.", host.Output(out))
		}
		list := ParseQueue(out)
		counts := map[string]int{}
		for _, m := range list {
			counts[m.Queue]++
		}
		total := len(list)
		if len(list) > 2000 {
			list = list[:2000]
		}
		if list == nil {
			list = []QueuedMessage{}
		}
		return map[string]any{"messages": list, "total": total, "counts": counts}, nil
	}})

	r.Register(actions.Action{Name: "mail.queue_message", Description: "Headers of one queued message (From, To, Subject, Date, Message-ID; not the body).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if !queueIDRe.MatchString(in.ID) {
			return nil, actions.InvalidParams("id is not a queue ID")
		}
		out, err := e.Runner.Run(ctx, "postcat", "-h", "-q", in.ID)
		if err != nil {
			// The message may be moving between queues (e.g. to deferred).
			time.Sleep(300 * time.Millisecond)
			out, err = e.Runner.Run(ctx, "postcat", "-h", "-q", in.ID)
		}
		if err != nil {
			return nil, actions.NotFound("The message is no longer in the queue: " + host.Output(out))
		}
		return map[string]any{"id": in.ID, "headers": headerFields(out)}, nil
	}})

	r.Register(actions.Action{Name: "mail.queue_action", Mutating: true, Description: "Act on queued messages: delete, retry (deliver now), hold, release; or flush (retry every deferred message). IDs go to postsuper on stdin.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Action string   `json:"action"`
			IDs    []string `json:"ids"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if in.Action == "flush" {
			if out, err := e.Runner.Run(ctx, "postqueue", "-f"); err != nil {
				return nil, actions.Failed("The queue could not be flushed.", host.Output(out))
			}
			return map[string]any{"action": "flush"}, nil
		}
		flag := map[string]string{"delete": "-d", "requeue": "-r", "hold": "-h", "release": "-H"}[in.Action]
		if flag == "" && in.Action != "retry" {
			return nil, actions.InvalidParams("action must be delete, retry, requeue, hold, release or flush")
		}
		if len(in.IDs) == 0 || len(in.IDs) > 5000 {
			return nil, actions.InvalidParams("ids: 1 to 5000 queue IDs")
		}
		for _, id := range in.IDs {
			if !queueIDRe.MatchString(id) {
				return nil, actions.InvalidParams("not a queue ID: " + id)
			}
		}
		if in.Action == "retry" {
			// Deliver these messages now (Postfix 3.5+: postqueue -i).
			done := 0
			for _, id := range in.IDs {
				if _, err := e.Runner.Run(ctx, "postqueue", "-i", id); err == nil {
					done++
				}
			}
			return map[string]any{"action": "retry", "count": done}, nil
		}
		out, err := e.Runner.RunWith(ctx, "postsuper", execx.Opts{Stdin: strings.NewReader(strings.Join(in.IDs, "\n") + "\n")}, flag, "-")
		if err != nil {
			return nil, actions.Failed("postsuper failed.", host.Output(out))
		}
		return map[string]any{"action": in.Action, "count": len(in.IDs), "output": host.Output(out)}, nil
	}})
}
