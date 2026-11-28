package dbmgr

import "testing"

func pool(t *testing.T, p Profile) int64 {
	t.Helper()
	n, ok := ParseSize(p.Values["innodb_buffer_pool_size"])
	if !ok {
		t.Fatalf("%s: pool %q", p.ID, p.Values["innodb_buffer_pool_size"])
	}
	return n
}

func TestRecommendedProfile(t *testing.T) {
	if Profiles(0, 0) != nil {
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
}

func TestProfilePoolsFollowTheirRanges(t *testing.T) {
	for _, tc := range []struct {
		ram          int64
		profile      string
		minPool, max int64
	}{
		{2 * gib, "small", 512 * mib, 512 * mib}, // 2 GB: the ceiling (RAM minus 1.5 GB) wins over the 512 MB floor's neighbours
		{4 * gib, "small", 512 * mib, 1 * gib},
		{8 * gib, "small", 512 * mib, 1 * gib},
		{8 * gib, "standard", 2 * gib, 4 * gib},
		{7884 << 20, "standard", 2 * gib, 4 * gib},
		{16 * gib, "high", 6389 << 20, 6717 << 20},
		{32 * gib, "high", 14500 << 20, 14900 << 20},
		{64 * gib, "high", 35389 << 20, 36110 << 20},
		{256 * gib, "high", 141557 << 20, 144441 << 20},
	} {
		for _, p := range Profiles(tc.ram, 0) {
			if p.ID != tc.profile {
				continue
			}
			if n := pool(t, p); n < tc.minPool || n > tc.max {
				t.Errorf("%d GiB %s: pool %d outside %d–%d", tc.ram/gib, p.ID, n, tc.minPool, tc.max)
			}
		}
	}
}

func TestNoProfileStarvesTheServer(t *testing.T) {
	for _, ram := range []int64{512 * mib, 1 * gib, 2 * gib, 3 * gib, 4 * gib, 6 * gib, 8 * gib, 12 * gib, 16 * gib, 32 * gib, 128 * gib} {
		for _, p := range Profiles(ram, 0) {
			n := pool(t, p)
			if n < 128*mib {
				t.Errorf("%d MiB %s: pool %d", ram/mib, p.ID, n)
			}
			// Never more than 55 % of the memory, and the services keep their reserve (except where even the 128 MB minimum does not fit).
			if ram >= 1*gib && float64(n) > 0.55*float64(ram) {
				t.Errorf("%d MiB %s: pool %d is more than 55%% of the memory", ram/mib, p.ID, n)
			}
			if ram >= 2*gib && ram-n < serviceReserve {
				t.Errorf("%d MiB %s: only %d bytes left for other services", ram/mib, p.ID, ram-n)
			}
			// The values must be ones the settings accept, and consistent with each other.
			for k, v := range p.Values {
				s, ok := settingByKey(k)
				if !ok {
					t.Fatalf("%s is not a managed setting", k)
				}
				if _, _, err := s.normalize(v); err != nil {
					t.Errorf("%d MiB %s %s=%q: %v", ram/mib, p.ID, k, v, err)
				}
			}
			if err := checkConsistency(p.Values, Limits{RAM: ram}); err != nil {
				t.Errorf("%d MiB %s: %v", ram/mib, p.ID, err)
			}
			redo, _ := ParseSize(p.Values["innodb_log_file_size"])
			if redo > n/2 {
				t.Errorf("%d MiB %s: redo log %d more than half of the pool %d", ram/mib, p.ID, redo, n)
			}
			if atoiOr(p.Values["thread_cache_size"]) > atoiOr(p.Values["max_connections"]) {
				t.Errorf("%d MiB %s: thread cache above connections", ram/mib, p.ID)
			}
		}
	}
}

func TestProfileConnectionsAndTablesFollowTheServer(t *testing.T) {
	small := Profiles(1*gib, 0)[0]
	if atoiOr(small.Values["max_connections"]) > 64 || small.Values["tmp_table_size"] != "16M" {
		t.Errorf("1 GB server: connections %s, tmp %s", small.Values["max_connections"], small.Values["tmp_table_size"])
	}
	big := Profiles(64*gib, 0)[2]
	if big.Values["max_connections"] != "400" || big.Values["tmp_table_size"] != "128M" || big.Values["innodb_log_file_size"] != "2G" {
		t.Errorf("64 GB high: %v", big.Values)
	}
	// An open files limit of 1024 cannot carry 2000 cached tables.
	std := Profiles(8*gib, 1024)[1]
	if n := atoiOr(std.Values["table_open_cache"]); n >= 2000 || n < 64 || len(std.Notes) == 0 {
		t.Errorf("table cache %d with notes %v", n, std.Notes)
	}
	if err := checkConsistency(std.Values, Limits{RAM: 8 * gib, OpenFiles: 1024}); err != nil {
		t.Errorf("a profile must pass the open files check: %v", err)
	}
}

func TestStandardIOValues(t *testing.T) {
	std := Profiles(8*gib, 0)[1]
	if std.Values["innodb_io_capacity"] != "1000" || std.Values["innodb_io_capacity_max"] != "2000" || std.Values["innodb_read_io_threads"] != "8" || std.Values["innodb_write_io_threads"] != "8" || std.Values["innodb_stats_on_metadata"] != "OFF" {
		t.Errorf("I/O values %v", std.Values)
	}
	ps := Profiles(8*gib, 0)
	if !ps[1].Recommended || ps[0].Recommended || ps[2].Recommended {
		t.Error("only Standard is recommended on 8 GB")
	}
	if ps[2].Warning == "" || ps[1].Warning != "" {
		t.Errorf("warnings: standard %q, high %q", ps[1].Warning, ps[2].Warning)
	}
}

func TestCheckConsistency(t *testing.T) {
	lim := Limits{RAM: 8 * gib, OpenFiles: 5000, MaxConn: 200}
	for name, tc := range map[string]struct {
		next map[string]string
		bad  bool
	}{
		"io max below capacity":        {map[string]string{"innodb_io_capacity": "2000", "innodb_io_capacity_max": "1000"}, true},
		"io max equal":                 {map[string]string{"innodb_io_capacity": "1000", "innodb_io_capacity_max": "1000"}, false},
		"only one of the pair":         {map[string]string{"innodb_io_capacity": "2000"}, false},
		"pool beyond the memory":       {map[string]string{"innodb_buffer_pool_size": "9G"}, true},
		"pool above 85% of the memory": {map[string]string{"innodb_buffer_pool_size": "7G"}, true},
		"pool of half the memory":      {map[string]string{"innodb_buffer_pool_size": "4G"}, false},
		"table cache beyond files":     {map[string]string{"table_open_cache": "4000"}, true},
		"table cache within files":     {map[string]string{"table_open_cache": "2000"}, false},
		"table cache with conns":       {map[string]string{"table_open_cache": "2300", "max_connections": "200"}, false},
		"table cache with many conns":  {map[string]string{"table_open_cache": "2400", "max_connections": "400"}, true},
	} {
		if err := checkConsistency(tc.next, lim); (err != nil) != tc.bad {
			t.Errorf("%s: error %v, want error %v", name, err, tc.bad)
		}
	}
	// Unknown limits check nothing they cannot know.
	if err := checkConsistency(map[string]string{"innodb_buffer_pool_size": "64G", "table_open_cache": "999999"}, Limits{}); err != nil {
		t.Errorf("unknown limits: %v", err)
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
