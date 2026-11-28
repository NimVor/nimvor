package dbmgr

import "testing"

func TestProfilesScaleWithMemory(t *testing.T) {
	if Profiles(0) != nil {
		t.Fatal("unknown memory must give no profiles")
	}
	for _, tc := range []struct {
		ram  int64
		want string
	}{{2 * gib, "small"}, {4 * gib, "small"}, {7884 << 20, "standard"}, {15974 << 20, "high"}, {64 * gib, "high"}} {
		if got := RecommendedProfile(tc.ram); got != tc.want {
			t.Errorf("%d bytes: %s, want %s", tc.ram, got, tc.want)
		}
	}
	ps := Profiles(8 * gib)
	if len(ps) != 3 {
		t.Fatalf("%d profiles", len(ps))
	}
	std := ps[1]
	if !std.Recommended || ps[0].Recommended || ps[2].Recommended {
		t.Errorf("recommended flags: %v %v %v", ps[0].Recommended, std.Recommended, ps[2].Recommended)
	}
	if std.Values["innodb_buffer_pool_size"] != "3276M" && std.Values["innodb_buffer_pool_size"] != "3200M" {
		t.Errorf("pool %q", std.Values["innodb_buffer_pool_size"])
	}
	if std.Values["innodb_io_capacity"] != "1000" || std.Values["innodb_io_capacity_max"] != "2000" || std.Values["innodb_read_io_threads"] != "8" || std.Values["innodb_stats_on_metadata"] != "OFF" {
		t.Errorf("I/O values %v", std.Values)
	}
	if ps[2].Warning == "" || std.Warning != "" {
		t.Errorf("warnings: standard %q, high %q", std.Warning, ps[2].Warning)
	}
	// Every value must be one the settings accept, and the pool never exceeds the share of memory.
	for _, p := range Profiles(3 * gib) {
		var pool int64
		for k, v := range p.Values {
			s, ok := settingByKey(k)
			if !ok {
				t.Fatalf("%s is not a managed setting", k)
			}
			if _, _, err := s.normalize(v); err != nil {
				t.Errorf("%s %s=%q: %v", p.ID, k, v, err)
			}
			if k == "innodb_buffer_pool_size" {
				pool, _ = ParseSize(v)
			}
		}
		if pool > 3*gib/2 {
			t.Errorf("%s: pool %d is more than half of the memory", p.ID, pool)
		}
		if lo, hi := atoiOr(p.Values["innodb_io_capacity"]), atoiOr(p.Values["innodb_io_capacity_max"]); hi < lo {
			t.Errorf("%s: io capacity max below capacity", p.ID)
		}
	}
}

func TestMemTotal(t *testing.T) {
	if n := memTotal([]byte("MemTotal:       8000000 kB\nMemFree: 1 kB\n")); n != 8000000*1024 {
		t.Errorf("memTotal = %d", n)
	}
	if memTotal([]byte("nothing")) != 0 {
		t.Error("memTotal of nothing")
	}
}
