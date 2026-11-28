package dbmgr

import (
	"context"
	"github.com/NimVor/nimvor/agent/internal/actions"
	"os"
	"path/filepath"
	"strconv"
)

// Limits are what this server can carry, used to refuse settings that would
// not work or would starve the rest of the server.
type Limits struct {
	RAM       int64 // bytes of memory; 0 when unknown
	OpenFiles int   // open_files_limit in force; 0 when unknown
	MaxConn   int   // max_connections in force; 0 when unknown
}

// A buffer pool above this share of the memory leaves too little for the
// operating system, PHP-FPM, Nginx, Redis and mail: refused outright.
const maxPoolShare = 0.85

func (c Client) limits(ctx context.Context) Limits {
	var l Limits
	if b, err := os.ReadFile(filepath.Join(c.Env.Cfg.ProcDir, "meminfo")); err == nil {
		l.RAM = memTotal(b)
	}
	if v, err := c.variables(ctx, []string{"open_files_limit", "max_connections"}); err == nil {
		l.OpenFiles, l.MaxConn = atoiOr(v["open_files_limit"]), atoiOr(v["max_connections"])
	}
	return l
}

// checkConsistency checks the settings NimVor would write (next) against each
// other and against the server's limits.
func checkConsistency(next map[string]string, l Limits) error {
	if lo, hi := atoiOr(next["innodb_io_capacity"]), atoiOr(next["innodb_io_capacity_max"]); lo > 0 && hi > 0 && hi < lo {
		return actions.InvalidParams("innodb_io_capacity_max: not below innodb_io_capacity (" + strconv.Itoa(lo) + ")")
	}
	if v, ok := next["innodb_buffer_pool_size"]; ok && l.RAM > 0 {
		if pool, ok := ParseSize(v); ok && float64(pool) > maxPoolShare*float64(l.RAM) {
			return actions.InvalidParams("innodb_buffer_pool_size: " + human(pool) + " is more than " + strconv.Itoa(int(maxPoolShare*100)) + "% of this server's memory (" + human(l.RAM>>20<<20) + "); the operating system, PHP-FPM, Nginx and mail would be starved. Use a smaller value.")
		}
	}
	// MariaDB lowers table_open_cache on its own when open_files_limit is too
	// small: (limit - 10 - max_connections) / 2 tables fit.
	if v, ok := next["table_open_cache"]; ok && l.OpenFiles > 0 {
		conn := atoiOr(next["max_connections"])
		if conn == 0 {
			conn = l.MaxConn
		}
		if allowed := (l.OpenFiles - 10 - conn) / 2; allowed > 0 && atoiOr(v) > allowed {
			return actions.InvalidParams("table_open_cache: " + v + " is more than this server's open files limit (" + strconv.Itoa(l.OpenFiles) + ") allows with " + strconv.Itoa(conn) + " connections; at most " + strconv.Itoa(allowed) + ". Raise the limit (LimitNOFILE of the database service) or use a smaller value.")
		}
	}
	return nil
}
