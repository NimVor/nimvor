package dbmgr

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
)

// More structure changes for the MySQL / MariaDB browser: table options (comment, engine, character set, row format,
// AUTO_INCREMENT), duplicating a table, renaming an index and CHECK constraints. Like the rest of alterSQL the input is
// data: names are validated and quoted, options come from fixed lists or strict patterns, and a CHECK condition is
// built from a column, an operator and literal values, never accepted as text.

var (
	charsetRe  = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)
	numberRe   = regexp.MustCompile(`^-?[0-9]{1,18}(\.[0-9]{1,10})?$`)
	rowFormats = map[string]bool{"DEFAULT": true, "DYNAMIC": true, "COMPACT": true, "REDUNDANT": true, "COMPRESSED": true, "FIXED": true}
	checkOps   = map[string]string{"eq": "=", "ne": "<>", "lt": "<", "le": "<=", "gt": ">", "ge": ">=", "like": "LIKE", "not_like": "NOT LIKE"}
)

type checkSpec struct {
	Column string `json:"column"`
	Op     string `json:"op"` // eq ne lt le gt ge like not_like between in not_in
	Value  string `json:"value"`
	Value2 string `json:"value2"` // between: the upper bound
}

// sqlLit writes v as a number when it is one, otherwise as a quoted string.
func sqlLit(v string) string {
	if numberRe.MatchString(v) {
		return v
	}
	return Quote(v)
}

func (c checkSpec) sql() (string, error) {
	if err := checkIdent("check column", c.Column); err != nil {
		return "", err
	}
	col := Ident(c.Column)
	if op, ok := checkOps[c.Op]; ok {
		return col + " " + op + " " + sqlLit(c.Value), nil
	}
	switch c.Op {
	case "between":
		return col + " BETWEEN " + sqlLit(c.Value) + " AND " + sqlLit(c.Value2), nil
	case "in", "not_in":
		items := strings.Split(c.Value, ",")
		if len(items) > 50 {
			return "", actions.InvalidParams("check: at most 50 values")
		}
		out := make([]string, 0, len(items))
		for _, it := range items {
			out = append(out, sqlLit(strings.TrimSpace(it)))
		}
		neg := ""
		if c.Op == "not_in" {
			neg = "NOT "
		}
		return col + " " + neg + "IN (" + strings.Join(out, ", ") + ")", nil
	}
	return "", actions.InvalidParams("check operator: eq, ne, lt, le, gt, ge, between, in, not_in, like or not_like")
}

func moreAlterSQL(table string, in alterReq) (string, error) {
	alter := "ALTER TABLE " + Ident(table) + " "
	switch in.Op {
	case "comment_table":
		if len(in.Comment) > 2048 {
			return "", actions.InvalidParams("comment: at most 2048 characters")
		}
		return alter + "COMMENT = " + Quote(in.Comment), nil
	case "set_engine":
		if in.Engine == "" || !tableEngines[in.Engine] {
			return "", actions.InvalidParams("engine: InnoDB, Aria, MyISAM or MEMORY")
		}
		return alter + "ENGINE = " + in.Engine, nil
	case "set_charset":
		cs, co := strings.ToLower(in.Charset), strings.ToLower(in.Collation)
		if !charsetRe.MatchString(cs) || (co != "" && !charsetRe.MatchString(co)) {
			return "", actions.InvalidParams("character set / collation: a name such as utf8mb4 and utf8mb4_unicode_ci")
		}
		coll := ""
		if co != "" {
			coll = " COLLATE " + co
		}
		if in.Convert {
			return alter + "CONVERT TO CHARACTER SET " + cs + coll, nil
		}
		return alter + "DEFAULT CHARACTER SET " + cs + coll, nil
	case "set_row_format":
		rf := strings.ToUpper(in.RowFormat)
		if !rowFormats[rf] {
			return "", actions.InvalidParams("row format: default, dynamic, compact, redundant, compressed or fixed")
		}
		return alter + "ROW_FORMAT = " + rf, nil
	case "set_auto_increment":
		if in.AutoInc < 1 {
			return "", actions.InvalidParams("auto_increment: a number from 1")
		}
		return alter + "AUTO_INCREMENT = " + strconv.FormatInt(in.AutoInc, 10), nil
	case "duplicate_table":
		if err := checkIdent("new name", in.NewName); err != nil {
			return "", err
		}
		sql := "CREATE TABLE " + Ident(in.NewName) + " LIKE " + Ident(table)
		if in.WithData {
			sql += "; INSERT INTO " + Ident(in.NewName) + " SELECT * FROM " + Ident(table)
		}
		return sql, nil
	case "rename_index":
		if err := checkIdent("index", in.Name); err != nil {
			return "", err
		}
		if err := checkIdent("new name", in.NewName); err != nil {
			return "", err
		}
		if strings.EqualFold(in.Name, "PRIMARY") {
			return "", actions.InvalidParams("the primary key cannot be renamed")
		}
		return alter + "RENAME INDEX " + Ident(in.Name) + " TO " + Ident(in.NewName), nil
	case "add_partition", "drop_partition", "truncate_partition", "rebuild_partition", "optimize_partition", "analyze_partition", "check_partition", "repair_partition":
		if in.Op == "add_partition" && (in.PartKind == "hash" || in.PartKind == "key") {
			if in.PartCount < 1 || in.PartCount > 128 {
				return "", actions.InvalidParams("part_count: 1 to 128")
			}
			return alter + "ADD PARTITION PARTITIONS " + strconv.Itoa(in.PartCount), nil
		}
		names := strings.Split(in.Name, ",")
		if len(names) == 0 || len(names) > 32 {
			return "", actions.InvalidParams("partition: 1 to 32 names")
		}
		quoted := make([]string, len(names))
		for i, n := range names {
			n = strings.TrimSpace(n)
			if err := checkIdent("partition", n); err != nil {
				return "", err
			}
			quoted[i] = Ident(n)
		}
		list := strings.Join(quoted, ", ")
		switch in.Op {
		case "drop_partition":
			return alter + "DROP PARTITION " + list, nil
		case "truncate_partition":
			return alter + "TRUNCATE PARTITION " + list, nil
		case "rebuild_partition":
			return alter + "REBUILD PARTITION " + list, nil
		case "optimize_partition":
			return alter + "OPTIMIZE PARTITION " + list, nil
		case "analyze_partition":
			return alter + "ANALYZE PARTITION " + list, nil
		case "check_partition":
			return alter + "CHECK PARTITION " + list, nil
		case "repair_partition":
			return alter + "REPAIR PARTITION " + list, nil
		}
		// add_partition for RANGE / LIST
		if len(names) != 1 {
			return "", actions.InvalidParams("partition: one name when adding")
		}
		switch in.PartKind {
		case "range":
			v := strings.TrimSpace(in.PartValue)
			if strings.EqualFold(v, "MAXVALUE") {
				return alter + "ADD PARTITION (PARTITION " + quoted[0] + " VALUES LESS THAN MAXVALUE)", nil
			}
			return alter + "ADD PARTITION (PARTITION " + quoted[0] + " VALUES LESS THAN (" + sqlLit(v) + "))", nil
		case "list":
			items := strings.Split(in.PartValue, ",")
			if len(items) > 100 {
				return "", actions.InvalidParams("part_value: at most 100 values")
			}
			out := make([]string, 0, len(items))
			for _, it := range items {
				out = append(out, sqlLit(strings.TrimSpace(it)))
			}
			return alter + "ADD PARTITION (PARTITION " + quoted[0] + " VALUES IN (" + strings.Join(out, ", ") + "))", nil
		}
		return "", actions.InvalidParams("part_kind: range, list, hash or key")
	case "remove_partitioning":
		return alter + "REMOVE PARTITIONING", nil
	case "add_check":
		if in.Check == nil {
			return "", actions.InvalidParams("check: describe the condition")
		}
		cond, err := in.Check.sql()
		if err != nil {
			return "", err
		}
		name := ""
		if in.Name != "" {
			if err := checkIdent("constraint name", in.Name); err != nil {
				return "", err
			}
			name = "CONSTRAINT " + Ident(in.Name) + " "
		}
		return alter + "ADD " + name + "CHECK (" + cond + ")", nil
	case "drop_check":
		if err := checkIdent("constraint", in.Name); err != nil {
			return "", err
		}
		// MariaDB: DROP CONSTRAINT; MySQL 8.0.19+ accepts it too (DROP CHECK is the older MySQL spelling).
		return alter + "DROP CONSTRAINT " + Ident(in.Name), nil
	}
	return "", actions.InvalidParams("unknown operation " + in.Op)
}
