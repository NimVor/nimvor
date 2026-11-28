package dbmgr

import (
	"strconv"
)

// Profile is a starting point for the memory and I/O settings of the database
// server, calculated from the memory of this server. It is only offered:
// nothing is written until an administrator reviews the values and saves them.
type Profile struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Values      map[string]string `json:"values"`
	Recommended bool              `json:"recommended"`
	Warning     string            `json:"warning,omitempty"`
	Notes       []string          `json:"notes,omitempty"`
}

const (
	mib = 1 << 20
	// Memory kept for the operating system, PHP-FPM, Nginx, Redis and mail on
	// top of whatever share the buffer pool leaves.
	serviceReserve = 3 * gib / 2
	// The most of the memory a profile gives the buffer pool.
	maxProfilePoolShare = 0.55
)

type profileDef struct {
	id, name, desc        string
	poolShare             func(ram int64) float64
	poolMin, poolMax      int64 // the range the buffer pool is kept in (0: none)
	redo                  func(ram int64) int64
	maxConn, threadCache  int
	tableCache            int
	tmp                   int64
	ioCap, ioCapMax       int
	readThreads, writeThr int
	// Memory (bytes) below which this profile is a poor fit.
	minRAM int64
}

func fixed(v float64) func(int64) float64  { return func(int64) float64 { return v } }
func fixedBytes(v int64) func(int64) int64 { return func(int64) int64 { return v } }

var profileDefs = []profileDef{
	{id: "small", name: "Small", desc: "For servers with about 2–4 GB of memory, shared with the web sites and mail. Buffer pool 512 MB–1 GB.",
		poolShare: fixed(0.25), poolMin: 512 * mib, poolMax: 1 * gib, redo: fixedBytes(256 * mib),
		maxConn: 100, threadCache: 16, tableCache: 1000, tmp: 32 * mib, ioCap: 400, ioCapMax: 800, readThreads: 4, writeThr: 4, minRAM: 1 * gib},
	{id: "standard", name: "Standard", desc: "For servers with about 8 GB of memory. Buffer pool 2–4 GB.",
		poolShare: fixed(0.35), poolMin: 2 * gib, poolMax: 4 * gib, redo: fixedBytes(512 * mib),
		maxConn: 200, threadCache: 32, tableCache: 2000, tmp: 64 * mib, ioCap: 1000, ioCapMax: 2000, readThreads: 8, writeThr: 8, minRAM: 6 * gib},
	// From 40 % of the memory at 16 GB up to 55 % at 64 GB and more.
	{id: "high", name: "High performance", desc: "For servers with 16 GB of memory or more and fast (SSD/NVMe) disks. Buffer pool 40–55% of the memory, growing with it.",
		poolShare: func(ram int64) float64 {
			f := float64(ram-16*gib) / float64(48*gib)
			if f < 0 {
				f = 0
			} else if f > 1 {
				f = 1
			}
			return 0.40 + 0.15*f
		},
		redo: func(ram int64) int64 {
			if ram >= 32*gib {
				return 2 * gib
			}
			return 1 * gib
		},
		maxConn: 400, threadCache: 64, tableCache: 4000, tmp: 128 * mib, ioCap: 2000, ioCapMax: 4000, readThreads: 16, writeThr: 16, minRAM: 12 * gib},
}

// RecommendedProfile picks the profile for a server's memory. MemTotal is a
// little below the nominal size (8 GB reads as about 7.7 GiB), hence the slack.
func RecommendedProfile(ram int64) string {
	switch {
	case ram >= 12*gib:
		return "high"
	case ram >= 6*gib:
		return "standard"
	}
	return "small"
}

// PoolCeiling is the most of ram a buffer pool may take in a profile: never
// more than 55 %, and always leaving room for the other services.
func PoolCeiling(ram int64) int64 {
	c := int64(float64(ram) * maxProfilePoolShare)
	if r := ram - serviceReserve; r < c {
		c = r
	}
	if c < 128*mib {
		c = 128 * mib
	}
	return c
}

func clampBytes(v, lo, hi int64) int64 {
	if lo > 0 && v < lo {
		v = lo
	}
	if hi > 0 && v > hi {
		v = hi
	}
	return v
}

// Profiles calculates the three profiles for a server with ram bytes of memory
// and the given open_files_limit (0: unknown). It returns nil when the memory
// is unknown.
func Profiles(ram int64, openFiles int) []Profile {
	if ram <= 0 {
		return nil
	}
	rec := RecommendedProfile(ram)
	out := make([]Profile, 0, len(profileDefs))
	for _, d := range profileDefs {
		var notes []string
		pool := clampBytes(int64(float64(ram)*d.poolShare(ram)), d.poolMin, d.poolMax)
		if ceil := PoolCeiling(ram); pool > ceil {
			pool = ceil
			notes = append(notes, "The buffer pool is held back to leave memory for PHP-FPM, Nginx, Redis, mail and the system.")
		}
		pool = pool / (128 * mib) * (128 * mib)
		if pool < 128*mib {
			pool = 128 * mib
		}
		redo := d.redo(ram)
		if redo > pool/2 {
			redo = pool / 2
		}
		// Memory a connection or a temporary table can add up to scales with the memory.
		conn := d.maxConn
		if byRAM := int(ram / (16 * mib)); conn > byRAM {
			conn = max(byRAM, 20)
		}
		tmp := d.tmp
		if byRAM := ram / 64 / mib * mib; tmp > byRAM {
			tmp = max(byRAM, 16*mib)
		}
		threads := min(d.threadCache, conn)
		cache := d.tableCache
		if openFiles > 0 { // what MariaDB itself would allow with this open files limit
			if allowed := (openFiles - 10 - conn) / 2; allowed > 0 && cache > allowed {
				cache = allowed
				notes = append(notes, "The open tables cache is limited by this server's open files limit ("+strconv.Itoa(openFiles)+"); raise LimitNOFILE of the database service for a larger cache.")
			}
		}
		v := map[string]string{
			"innodb_buffer_pool_size":  human(pool),
			"innodb_log_file_size":     human(redo),
			"max_connections":          strconv.Itoa(conn),
			"thread_cache_size":        strconv.Itoa(threads),
			"table_open_cache":         strconv.Itoa(cache),
			"tmp_table_size":           human(tmp),
			"max_heap_table_size":      human(tmp),
			"innodb_io_capacity":       strconv.Itoa(d.ioCap),
			"innodb_io_capacity_max":   strconv.Itoa(d.ioCapMax),
			"innodb_read_io_threads":   strconv.Itoa(d.readThreads),
			"innodb_write_io_threads":  strconv.Itoa(d.writeThr),
			"innodb_stats_on_metadata": "OFF",
		}
		p := Profile{ID: d.id, Name: d.name, Description: d.desc, Values: v, Recommended: d.id == rec, Notes: notes}
		if ram < d.minRAM {
			p.Warning = "This server has less memory than this profile is meant for; the memory values are scaled down, but check them before saving."
		}
		out = append(out, p)
	}
	return out
}
