package dbmgr

import (
	"strconv"
)

// Profile is a starting point for the memory and I/O settings of the database
// server, scaled to the memory of this server. It is only offered: nothing is
// written until an administrator reviews the values and saves them.
type Profile struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Values      map[string]string `json:"values"`
	Recommended bool              `json:"recommended"`
	Warning     string            `json:"warning,omitempty"`
}

type profileDef struct {
	id, name, desc string
	// Share of the memory given to the InnoDB buffer pool; the server also runs
	// web sites, so even the largest profile leaves half of the memory free.
	poolShare             float64
	redo                  int64
	maxConn, threadCache  int
	tableCache            int
	tmp                   int64
	ioCap, ioCapMax       int
	readThreads, writeThr int
	// Memory (bytes) below which this profile is a poor fit.
	minRAM int64
}

var profileDefs = []profileDef{
	{"small", "Small", "For servers with about 2–4 GB of memory, shared with the web sites and mail.", 0.25, 256 << 20, 100, 16, 1000, 32 << 20, 400, 800, 4, 4, 1 * gib},
	{"standard", "Standard", "For servers with about 8 GB of memory.", 0.40, 512 << 20, 200, 32, 2000, 64 << 20, 1000, 2000, 8, 8, 6 * gib},
	{"high", "High performance", "For servers with 16 GB of memory or more and fast (SSD/NVMe) disks.", 0.50, 1 * gib, 400, 64, 4000, 128 << 20, 2000, 4000, 16, 16, 12 * gib},
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

// Profiles computes the three profiles for a server with ram bytes of memory.
// It returns nil when the memory is unknown.
func Profiles(ram int64) []Profile {
	if ram <= 0 {
		return nil
	}
	rec := RecommendedProfile(ram)
	out := make([]Profile, 0, len(profileDefs))
	for _, d := range profileDefs {
		pool := int64(float64(ram)*d.poolShare) / (128 << 20) * (128 << 20)
		if pool < 128<<20 {
			pool = 128 << 20
		}
		redo := d.redo
		if redo > pool/2 {
			redo = pool / 2
		}
		v := map[string]string{
			"innodb_buffer_pool_size":  human(pool),
			"innodb_log_file_size":     human(redo),
			"max_connections":          strconv.Itoa(d.maxConn),
			"thread_cache_size":        strconv.Itoa(d.threadCache),
			"table_open_cache":         strconv.Itoa(d.tableCache),
			"tmp_table_size":           human(d.tmp),
			"max_heap_table_size":      human(d.tmp),
			"innodb_io_capacity":       strconv.Itoa(d.ioCap),
			"innodb_io_capacity_max":   strconv.Itoa(d.ioCapMax),
			"innodb_read_io_threads":   strconv.Itoa(d.readThreads),
			"innodb_write_io_threads":  strconv.Itoa(d.writeThr),
			"innodb_stats_on_metadata": "OFF",
		}
		p := Profile{ID: d.id, Name: d.name, Description: d.desc, Values: v, Recommended: d.id == rec}
		if ram < d.minRAM {
			p.Warning = "This server has less memory than this profile is meant for; the memory values are scaled down, but check them before saving."
		}
		out = append(out, p)
	}
	return out
}
