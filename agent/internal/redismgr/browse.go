package redismgr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

const (
	maxValue    = 1 << 20 // bytes of a value shown or written
	maxElements = 500     // elements of a collection shown
	maxKeysPage = 200
)

// Bin is a key or value that may not be valid text. B64 is the canonical
// form (always set); Text is set when the bytes are valid UTF-8.
type Bin struct {
	B64    string `json:"b64"`
	Text   string `json:"text,omitempty"`
	Binary bool   `json:"binary,omitempty"`
}

func bin(b []byte) Bin {
	out := Bin{B64: base64.StdEncoding.EncodeToString(b)}
	if utf8.Valid(b) && bytes.IndexByte(b, 0) < 0 {
		out.Text = string(b)
	} else {
		out.Binary = true
	}
	return out
}

// KeyInfo is one key in a listing.
type KeyInfo struct {
	Key  Bin    `json:"key"`
	Type string `json:"type"`
	TTL  int64  `json:"ttl"` // seconds; -1 no expiry
}

// browseReq is common to browser actions: the database and, for
// per-website access, the key prefix every key must start with.
type browseReq struct {
	DB     int    `json:"db"`
	Prefix string `json:"prefix"`
}

func (r browseReq) check() error {
	if r.DB < 0 || r.DB > 255 {
		return actions.InvalidParams("db: 0–255")
	}
	if len(r.Prefix) > 200 || strings.ContainsAny(r.Prefix, "\r\n\x00") {
		return actions.InvalidParams("prefix: invalid")
	}
	return nil
}

// decodeKey turns a request key (base64) into bytes and checks the prefix.
func (r browseReq) decodeKey(b64 string) ([]byte, error) {
	k, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(k) == 0 || len(k) > 4096 {
		return nil, actions.InvalidParams("key: invalid")
	}
	if !bytes.HasPrefix(k, []byte(r.Prefix)) {
		return nil, actions.InvalidParams("key: outside the allowed prefix")
	}
	return k, nil
}

// escapeGlob escapes Redis glob metacharacters so a prefix matches literally.
func escapeGlob(s string) string {
	var b strings.Builder
	for _, c := range s {
		if strings.ContainsRune(`*?[]\`, c) {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func (c Client) open(ctx context.Context, db int) (*conn, error) {
	x, err := c.dial(ctx)
	if err != nil {
		return nil, actions.Failed("Redis is not reachable.", err.Error())
	}
	if _, err := x.do("SELECT", strconv.Itoa(db)); err != nil {
		x.Close()
		return nil, actions.InvalidParams("db: " + err.Error())
	}
	return x, nil
}

// Keys lists keys matching pattern (a Redis glob, applied after the
// prefix), resuming from cursor. It returns the next cursor ("0" at the end).
func (c Client) Keys(ctx context.Context, r browseReq, pattern, cursor string) ([]KeyInfo, string, int64, error) {
	if err := r.check(); err != nil {
		return nil, "", 0, err
	}
	if pattern == "" {
		pattern = "*"
	}
	if len(pattern) > 200 {
		return nil, "", 0, actions.InvalidParams("pattern: too long")
	}
	if cursor == "" {
		cursor = "0"
	}
	if _, err := strconv.ParseUint(cursor, 10, 64); err != nil {
		return nil, "", 0, actions.InvalidParams("cursor: invalid")
	}
	x, err := c.open(ctx, r.DB)
	if err != nil {
		return nil, "", 0, err
	}
	defer x.Close()
	match := escapeGlob(r.Prefix) + pattern
	var keys [][]byte
	for i := 0; i < 50 && len(keys) < maxKeysPage; i++ {
		v, err := x.do("SCAN", cursor, "MATCH", match, "COUNT", "500")
		if err != nil {
			return nil, "", 0, actions.Failed("SCAN failed.", err.Error())
		}
		a := asArray(v)
		if len(a) != 2 {
			return nil, "", 0, errors.New("unexpected SCAN reply")
		}
		cursor = string(asBytes(a[0]))
		for _, k := range asArray(a[1]) {
			keys = append(keys, asBytes(k))
		}
		if cursor == "0" {
			break
		}
	}
	out := make([]KeyInfo, 0, len(keys))
	for _, k := range keys {
		t, _ := x.do("TYPE", string(k))
		ttl, _ := x.do("TTL", string(k))
		out = append(out, KeyInfo{Key: bin(k), Type: string(asBytes(t)), TTL: asInt(ttl)})
	}
	size, _ := x.do("DBSIZE")
	return out, cursor, asInt(size), nil
}

// Element is one member of a collection.
type Element struct {
	Field *Bin    `json:"field,omitempty"` // hash field, stream entry id
	Value Bin     `json:"value"`
	Score *string `json:"score,omitempty"` // sorted sets
	Index *int    `json:"index,omitempty"` // lists
}

// KeyValue is the content of one key.
type KeyValue struct {
	Key       Bin       `json:"key"`
	Type      string    `json:"type"`
	TTL       int64     `json:"ttl"`
	Length    int64     `json:"length"`
	Value     *Bin      `json:"value,omitempty"` // strings
	Elements  []Element `json:"elements,omitempty"`
	Truncated bool      `json:"truncated"`
}

// Get reads a key (strings up to 1 MiB, collections up to 500 elements).
func (c Client) Get(ctx context.Context, r browseReq, keyB64 string) (*KeyValue, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	key, err := r.decodeKey(keyB64)
	if err != nil {
		return nil, err
	}
	x, err := c.open(ctx, r.DB)
	if err != nil {
		return nil, err
	}
	defer x.Close()
	k := string(key)
	t, err := x.do("TYPE", k)
	if err != nil {
		return nil, err
	}
	kv := &KeyValue{Key: bin(key), Type: string(asBytes(t))}
	if kv.Type == "none" {
		return nil, actions.NotFound("The key does not exist (it may have expired).")
	}
	ttl, _ := x.do("TTL", k)
	kv.TTL = asInt(ttl)
	scan := func(cmd string, pairs bool) error {
		cursor := "0"
		for {
			v, err := x.do(cmd, k, cursor, "COUNT", "200")
			if err != nil {
				return err
			}
			a := asArray(v)
			if len(a) != 2 {
				return errors.New("unexpected scan reply")
			}
			cursor = string(asBytes(a[0]))
			items := asArray(a[1])
			for i := 0; i < len(items); i++ {
				if len(kv.Elements) >= maxElements {
					kv.Truncated = true
					return nil
				}
				if pairs && i+1 < len(items) {
					f := bin(asBytes(items[i]))
					kv.Elements = append(kv.Elements, Element{Field: &f, Value: bin(asBytes(items[i+1]))})
					i++
				} else {
					kv.Elements = append(kv.Elements, Element{Value: bin(asBytes(items[i]))})
				}
			}
			if cursor == "0" {
				return nil
			}
		}
	}
	switch kv.Type {
	case "string":
		n, _ := x.do("STRLEN", k)
		kv.Length = asInt(n)
		var v any
		if kv.Length > maxValue {
			kv.Truncated = true
			v, err = x.do("GETRANGE", k, "0", strconv.Itoa(maxValue-1))
		} else {
			v, err = x.do("GET", k)
		}
		if err != nil {
			return nil, err
		}
		b := bin(asBytes(v))
		kv.Value = &b
	case "hash":
		n, _ := x.do("HLEN", k)
		kv.Length = asInt(n)
		err = scan("HSCAN", true)
	case "set":
		n, _ := x.do("SCARD", k)
		kv.Length = asInt(n)
		err = scan("SSCAN", false)
	case "list":
		n, _ := x.do("LLEN", k)
		kv.Length = asInt(n)
		v, e := x.do("LRANGE", k, "0", strconv.Itoa(maxElements-1))
		err = e
		for i, it := range asArray(v) {
			idx := i
			kv.Elements = append(kv.Elements, Element{Value: bin(asBytes(it)), Index: &idx})
		}
		kv.Truncated = kv.Length > maxElements
	case "zset":
		n, _ := x.do("ZCARD", k)
		kv.Length = asInt(n)
		v, e := x.do("ZRANGE", k, "0", strconv.Itoa(maxElements-1), "WITHSCORES")
		err = e
		a := asArray(v)
		for i := 0; i+1 < len(a); i += 2 {
			s := string(asBytes(a[i+1]))
			kv.Elements = append(kv.Elements, Element{Value: bin(asBytes(a[i])), Score: &s})
		}
		kv.Truncated = kv.Length > maxElements
	case "stream":
		n, _ := x.do("XLEN", k)
		kv.Length = asInt(n)
		v, e := x.do("XREVRANGE", k, "+", "-", "COUNT", "100")
		err = e
		for _, entry := range asArray(v) {
			ea := asArray(entry)
			if len(ea) != 2 {
				continue
			}
			id := bin(asBytes(ea[0]))
			m := map[string]string{}
			fv := asArray(ea[1])
			for i := 0; i+1 < len(fv); i += 2 {
				m[string(asBytes(fv[i]))] = string(asBytes(fv[i+1]))
			}
			js, _ := json.Marshal(m)
			kv.Elements = append(kv.Elements, Element{Field: &id, Value: bin(js)})
		}
		kv.Truncated = kv.Length > 100
	default:
		// Module types (JSON, time series, …) are listed but not shown.
	}
	if err != nil {
		return nil, actions.Failed("Reading the key failed.", err.Error())
	}
	return kv, nil
}

// Edit is one change to a key.
type Edit struct {
	browseReq
	Key    string `json:"key"`     // base64
	Op     string `json:"op"`      // set, hset, hdel, lpush, rpush, lset, lrem, sadd, srem, zadd, zrem, expire, rename, create
	Type   string `json:"type"`    // create: string, hash, list, set, zset
	Field  string `json:"field"`   // hash field (base64)
	Value  string `json:"value"`   // base64
	Score  string `json:"score"`   // zadd
	Index  int64  `json:"index"`   // lset
	TTL    int64  `json:"ttl"`     // expire (0 removes the expiry), set/create
	NewKey string `json:"new_key"` // rename (base64)
}

func b64(s, field string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", actions.InvalidParams(field + ": invalid base64")
	}
	if len(b) > maxValue {
		return "", actions.InvalidParams(field + ": larger than 1 MiB")
	}
	return string(b), nil
}

// Apply performs one edit.
func (c Client) Apply(ctx context.Context, e Edit) (any, error) {
	if err := e.check(); err != nil {
		return nil, err
	}
	key, err := e.decodeKey(e.Key)
	if err != nil {
		return nil, err
	}
	k := string(key)
	val, err := b64(e.Value, "value")
	if err != nil {
		return nil, err
	}
	field, err := b64(e.Field, "field")
	if err != nil {
		return nil, err
	}
	if e.TTL < 0 || e.TTL > 10*365*86400 {
		return nil, actions.InvalidParams("ttl: 0 (no expiry) up to 10 years")
	}
	x, err := c.open(ctx, e.DB)
	if err != nil {
		return nil, err
	}
	defer x.Close()
	exists := func() bool { n, _ := x.do("EXISTS", k); return asInt(n) == 1 }
	var args [][]string
	switch e.Op {
	case "create":
		if exists() {
			return nil, actions.Conflict("A key with this name already exists.")
		}
		switch e.Type {
		case "string":
			args = [][]string{{"SET", k, val}}
		case "hash":
			if field == "" {
				return nil, actions.InvalidParams("field: required")
			}
			args = [][]string{{"HSET", k, field, val}}
		case "list":
			args = [][]string{{"RPUSH", k, val}}
		case "set":
			args = [][]string{{"SADD", k, val}}
		case "zset":
			if _, err := strconv.ParseFloat(e.Score, 64); err != nil {
				return nil, actions.InvalidParams("score: a number")
			}
			args = [][]string{{"ZADD", k, e.Score, val}}
		default:
			return nil, actions.InvalidParams("type: string, hash, list, set or zset")
		}
		if e.TTL > 0 {
			args = append(args, []string{"EXPIRE", k, strconv.FormatInt(e.TTL, 10)})
		}
	case "set":
		if e.TTL > 0 {
			args = [][]string{{"SET", k, val, "EX", strconv.FormatInt(e.TTL, 10)}}
		} else {
			args = [][]string{{"SET", k, val, "KEEPTTL"}}
		}
	case "hset":
		args = [][]string{{"HSET", k, field, val}}
	case "hdel":
		args = [][]string{{"HDEL", k, field}}
	case "lpush", "rpush":
		args = [][]string{{strings.ToUpper(e.Op), k, val}}
	case "lset":
		args = [][]string{{"LSET", k, strconv.FormatInt(e.Index, 10), val}}
	case "lrem":
		args = [][]string{{"LREM", k, "1", val}}
	case "sadd", "srem":
		args = [][]string{{strings.ToUpper(e.Op), k, val}}
	case "zadd":
		if _, err := strconv.ParseFloat(e.Score, 64); err != nil {
			return nil, actions.InvalidParams("score: a number")
		}
		args = [][]string{{"ZADD", k, e.Score, val}}
	case "zrem":
		args = [][]string{{"ZREM", k, val}}
	case "expire":
		if e.TTL == 0 {
			args = [][]string{{"PERSIST", k}}
		} else {
			args = [][]string{{"EXPIRE", k, strconv.FormatInt(e.TTL, 10)}}
		}
	case "rename":
		nk, err := e.decodeKey(e.NewKey)
		if err != nil {
			return nil, err
		}
		args = [][]string{{"RENAMENX", k, string(nk)}}
	default:
		return nil, actions.InvalidParams("op: unknown")
	}
	if e.Op != "create" && !exists() {
		return nil, actions.NotFound("The key does not exist (it may have expired).")
	}
	var last any
	for _, a := range args {
		if last, err = x.do(a...); err != nil {
			return nil, actions.Failed("Redis refused the change.", err.Error())
		}
	}
	if e.Op == "rename" && asInt(last) == 0 {
		return nil, actions.Conflict("A key with the new name already exists.")
	}
	return map[string]any{"ok": true}, nil
}

// Delete removes keys (all must match the prefix).
func (c Client) Delete(ctx context.Context, r browseReq, keysB64 []string) (int64, error) {
	if err := r.check(); err != nil {
		return 0, err
	}
	if len(keysB64) == 0 || len(keysB64) > 500 {
		return 0, actions.InvalidParams("keys: 1–500")
	}
	args := []string{"UNLINK"}
	for _, s := range keysB64 {
		k, err := r.decodeKey(s)
		if err != nil {
			return 0, err
		}
		args = append(args, string(k))
	}
	x, err := c.open(ctx, r.DB)
	if err != nil {
		return 0, err
	}
	defer x.Close()
	n, err := x.do(args...)
	if err != nil {
		return 0, actions.Failed("Deleting failed.", err.Error())
	}
	return asInt(n), nil
}

// registerBrowser adds the key browser actions.
func registerBrowser(r *actions.Registry, env *host.Env) {
	c := Client{Env: env}
	r.Register(actions.Action{Name: "redis.keys", Description: "List keys of a Redis database (SCAN, optionally limited to a key prefix).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			browseReq
			Pattern string `json:"pattern"`
			Cursor  string `json:"cursor"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		keys, next, size, err := c.Keys(ctx, in.browseReq, in.Pattern, in.Cursor)
		if err != nil {
			return nil, err
		}
		return map[string]any{"keys": keys, "cursor": next, "db_size": size}, nil
	}})
	r.Register(actions.Action{Name: "redis.key_get", Description: "Read one Redis key (strings up to 1 MiB, collections up to 500 elements).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			browseReq
			Key string `json:"key"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return c.Get(ctx, in.browseReq, in.Key)
	}})
	r.Register(actions.Action{Name: "redis.key_edit", Mutating: true, Description: "Create or change a Redis key (set, hash/list/set/sorted-set elements, TTL, rename).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in Edit
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return c.Apply(ctx, in)
	}})
	r.Register(actions.Action{Name: "redis.key_delete", Mutating: true, Description: "Delete Redis keys.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			browseReq
			Keys []string `json:"keys"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		n, err := c.Delete(ctx, in.browseReq, in.Keys)
		return map[string]any{"deleted": n}, err
	}})
}
