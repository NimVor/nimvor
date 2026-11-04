package dbmgr

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/mysqlwire"
)

// registerOverview adds the database overview of the MySQL / MariaDB browser: size, object counts, server version and
// the engine family (MariaDB-only features are offered only when this is MariaDB). Read-only, catalog queries only.
func registerOverview(r *actions.Registry, c Client) {
	r.Register(actions.Action{Name: "db.browse_overview", Mutating: true, Description: "Database browser: overview of one database (size, object counts, server version and engine family, character set).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in browseReq
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		var out map[string]any
		err := c.withBrowser(ctx, in.Database, func(conn *mysqlwire.Conn) error {
			count := func(sql string) int64 {
				res, err := one(conn, sql)
				if err != nil || len(res.Rows) == 0 {
					return 0
				}
				n, _ := strconv.ParseInt(str(res.Rows[0][0]), 10, 64)
				return n
			}
			res, err := one(conn, "SELECT COUNT(CASE WHEN TABLE_TYPE <> 'VIEW' THEN 1 END), COUNT(CASE WHEN TABLE_TYPE = 'VIEW' THEN 1 END), COALESCE(SUM(TABLE_ROWS),0), COALESCE(SUM(DATA_LENGTH),0), COALESCE(SUM(INDEX_LENGTH),0), COALESCE(SUM(DATA_FREE),0) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()")
			if err != nil {
				return sqlFailed(err)
			}
			n := func(i int) int64 { v, _ := strconv.ParseInt(str(res.Rows[0][i]), 10, 64); return v }
			v, err := one(conn, "SELECT VERSION(), @@character_set_database, @@collation_database, @@version_comment")
			if err != nil {
				return sqlFailed(err)
			}
			version, comment := str(v.Rows[0][0]), str(v.Rows[0][3])
			family := "mysql"
			if containsFold(version, "mariadb") || containsFold(comment, "mariadb") {
				family = "mariadb"
			}
			engines := map[string]int64{}
			if er, err := one(conn, "SELECT COALESCE(ENGINE,''), COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE <> 'VIEW' GROUP BY ENGINE"); err == nil {
				for _, row := range er.Rows {
					k, _ := strconv.ParseInt(str(row[1]), 10, 64)
					engines[str(row[0])] = k
				}
			}
			out = map[string]any{
				"database": in.Database, "server": version, "family": family, "charset": str(v.Rows[0][1]), "collation": str(v.Rows[0][2]),
				"tables": n(0), "views": n(1), "rows": n(2), "data_bytes": n(3), "index_bytes": n(4), "free_bytes": n(5), "engines": engines,
				"routines":  count("SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE()"),
				"triggers":  count("SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE()"),
				"events":    count("SELECT COUNT(*) FROM information_schema.EVENTS WHERE EVENT_SCHEMA = DATABASE()"),
				"sequences": count("SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'SEQUENCE'"),
			}
			return nil
		})
		return out, err
	}})
}

func containsFold(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
