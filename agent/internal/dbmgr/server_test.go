package dbmgr

import (
	"reflect"
	"strings"
	"testing"
)

func TestServerSettingNormalize(t *testing.T) {
	get := func(k string) ServerSetting {
		s, ok := settingByKey(k)
		if !ok {
			t.Fatalf("no %s", k)
		}
		return s
	}
	for _, tc := range []struct{ key, in, file, sql string }{
		{"max_connections", " 300 ", "300", "300"},
		{"innodb_buffer_pool_size", "1g", "1G", "1073741824"},
		{"max_allowed_packet", "64M", "64M", "67108864"},
		{"slow_query_log", "on", "ON", "ON"},
		{"long_query_time", "0.5", "0.5", "0.5"},
		{"innodb_flush_log_at_trx_commit", "2", "2", "2"},
		{"character_set_server", "utf8mb4", "utf8mb4", "'utf8mb4'"},
		{"collation_server", "utf8mb4_unicode_ci", "utf8mb4_unicode_ci", "'utf8mb4_unicode_ci'"},
	} {
		f, q, err := get(tc.key).normalize(tc.in)
		if err != nil || f != tc.file || q != tc.sql {
			t.Errorf("%s(%q) = %q %q %v", tc.key, tc.in, f, q, err)
		}
	}
	for _, tc := range [][2]string{
		{"max_connections", "5"}, {"max_connections", "1e3"}, {"max_connections", "100; SET GLOBAL x=1"},
		{"innodb_buffer_pool_size", "1M"}, {"innodb_buffer_pool_size", "1X"}, {"max_allowed_packet", "2G"},
		{"slow_query_log", "maybe"}, {"long_query_time", "NaN"}, {"long_query_time", "-1"},
		{"innodb_flush_log_at_trx_commit", "3"}, {"character_set_server", "utf16"},
		{"collation_server", "x' OR '1"}, {"collation_server", "utf8mb4"},
		{"innodb_log_file_size", "99999999999999T"},
	} {
		if f, _, err := get(tc[0]).normalize(tc[1]); err == nil {
			t.Errorf("%s(%q) accepted as %q", tc[0], tc[1], f)
		}
	}
}

func TestDropInRoundTrip(t *testing.T) {
	v := map[string]string{"slow_query_log": "ON", "max_connections": "300", "innodb_buffer_pool_size": "1G"}
	text := RenderDropIn(v)
	if !strings.Contains(text, "[mysqld]\nmax_connections = 300\n") || strings.Index(text, "max_connections") > strings.Index(text, "innodb_buffer_pool_size") {
		t.Fatalf("render (fixed order):\n%s", text)
	}
	if got := ParseDropIn(text + "unknown_option = 1\nmax-allowed-packet = 16M\n"); !reflect.DeepEqual(got, map[string]string{"slow_query_log": "ON", "max_connections": "300", "innodb_buffer_pool_size": "1G", "max_allowed_packet": "16M"}) {
		t.Errorf("parse = %v", got)
	}
}
