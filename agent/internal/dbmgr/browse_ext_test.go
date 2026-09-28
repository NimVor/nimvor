package dbmgr

import (
	"reflect"
	"strings"
	"testing"
)

func TestColumnSpecSQL(t *testing.T) {
	for _, tc := range []struct {
		c    ColumnSpec
		want string
	}{
		{ColumnSpec{Name: "id", Type: "int", Unsigned: true, AutoIncrement: true}, "`id` INT UNSIGNED NOT NULL AUTO_INCREMENT"},
		{ColumnSpec{Name: "title", Type: "VARCHAR", Length: "190", Nullable: true, Default: "null", Comment: "it's"}, "`title` VARCHAR(190) NULL DEFAULT NULL COMMENT 'it\\'s'"},
		{ColumnSpec{Name: "price", Type: "decimal", Length: "10, 2", Default: "value", DefaultValue: "0.00"}, "`price` DECIMAL(10,2) NOT NULL DEFAULT '0.00'"},
		{ColumnSpec{Name: "st", Type: "enum", Values: []string{"a", "b'c"}, Default: "value", DefaultValue: "a"}, "`st` ENUM('a','b\\'c') NOT NULL DEFAULT 'a'"},
		{ColumnSpec{Name: "at", Type: "timestamp", Length: "3", Default: "current_timestamp", OnUpdateNow: true}, "`at` TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)"},
		{ColumnSpec{Name: "we`ird", Type: "TEXT", Nullable: true}, "`we``ird` TEXT NULL"},
	} {
		got, err := tc.c.sql()
		if err != nil || got != tc.want {
			t.Errorf("%+v:\n got %q, %v\nwant %q", tc.c, got, err, tc.want)
		}
	}
	for _, bad := range []ColumnSpec{
		{Name: "", Type: "INT"},
		{Name: "x", Type: "INT; DROP TABLE t"},
		{Name: "x", Type: "VARCHAR"},                               // needs a length
		{Name: "x", Type: "VARCHAR", Length: "10) ; DROP TABLE t"}, // not a number
		{Name: "x", Type: "INT", Length: "11"},                     // no length for integers
		{Name: "x", Type: "DECIMAL", Length: "5,6"},                // scale above precision
		{Name: "x", Type: "TEXT", Default: "value", DefaultValue: "a"},
		{Name: "x", Type: "INT", Default: "null"},                          // NOT NULL defaulting to NULL
		{Name: "x", Type: "VARCHAR", Length: "5", AutoIncrement: true},     // not an integer
		{Name: "x", Type: "VARCHAR", Length: "5", Unsigned: true},          // not a number
		{Name: "x", Type: "INT", Default: "current_timestamp"},             // not a time
		{Name: "x", Type: "ENUM"},                                          // no values
		{Name: "x", Type: "DATETIME", Length: "7"},                         // fsp 0–6
		{Name: "x", Type: "INT", Default: "expression", DefaultValue: "1"}, // unknown default kind
		{Name: "trailing ", Type: "INT"},
	} {
		if s, err := bad.sql(); err == nil {
			t.Errorf("%+v accepted: %s", bad, s)
		}
	}
}

func TestAlterSQL(t *testing.T) {
	col := &ColumnSpec{Name: "n", Type: "INT", Nullable: true}
	for _, tc := range []struct {
		in   alterReq
		want string
	}{
		{alterReq{Op: "add_column", Column: col, After: "id"}, "ALTER TABLE `t` ADD COLUMN `n` INT NULL AFTER `id`"},
		{alterReq{Op: "add_column", Column: col, After: "^"}, "ALTER TABLE `t` ADD COLUMN `n` INT NULL FIRST"},
		{alterReq{Op: "modify_column", Name: "old", Column: col}, "ALTER TABLE `t` CHANGE COLUMN `old` `n` INT NULL"},
		{alterReq{Op: "drop_column", Name: "n"}, "ALTER TABLE `t` DROP COLUMN `n`"},
		{alterReq{Op: "add_index", Kind: "unique", Name: "u_ab", IndexCols: []string{"a", "b"}}, "ALTER TABLE `t` ADD UNIQUE INDEX `u_ab` (`a`, `b`)"},
		{alterReq{Op: "add_index", Kind: "primary", IndexCols: []string{"id"}}, "ALTER TABLE `t` ADD PRIMARY KEY (`id`)"},
		{alterReq{Op: "drop_index", Name: "PRIMARY"}, "ALTER TABLE `t` DROP PRIMARY KEY"},
		{alterReq{Op: "drop_index", Name: "u_ab"}, "ALTER TABLE `t` DROP INDEX `u_ab`"},
		{alterReq{Op: "add_foreign_key", Name: "fk_u", IndexCols: []string{"user_id"}, RefTable: "users", RefCols: []string{"id"}, OnDelete: "cascade"}, "ALTER TABLE `t` ADD CONSTRAINT `fk_u` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE ON UPDATE RESTRICT"},
		{alterReq{Op: "drop_foreign_key", Name: "fk_u"}, "ALTER TABLE `t` DROP FOREIGN KEY `fk_u`"},
		{alterReq{Op: "rename_table", NewName: "t2"}, "RENAME TABLE `t` TO `t2`"},
		{alterReq{Op: "truncate_table"}, "TRUNCATE TABLE `t`"},
		{alterReq{Op: "create_table", Columns: []ColumnSpec{{Name: "id", Type: "BIGINT", Unsigned: true, AutoIncrement: true}}, Primary: []string{"id"}, Engine: "InnoDB"}, "CREATE TABLE `t` (\n  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,\n  PRIMARY KEY (`id`)\n) ENGINE=InnoDB"},
	} {
		got, err := alterSQL("t", tc.in)
		if err != nil || got != tc.want {
			t.Errorf("%s:\n got %q, %v\nwant %q", tc.in.Op, got, err, tc.want)
		}
	}
	for _, bad := range []alterReq{
		{Op: "drop_database"},
		{Op: "add_column"},
		{Op: "add_index", IndexCols: nil},
		{Op: "add_index", Kind: "spatial", IndexCols: []string{"a"}},
		{Op: "add_foreign_key", IndexCols: []string{"a", "b"}, RefTable: "u", RefCols: []string{"id"}},
		{Op: "add_foreign_key", IndexCols: []string{"a"}, RefTable: "u", RefCols: []string{"id"}, OnDelete: "set default; drop table u"},
		{Op: "create_table", Columns: []ColumnSpec{{Name: "a", Type: "INT"}}, Engine: "CSV"},
		{Op: "rename_table", NewName: ""},
	} {
		if s, err := alterSQL("t", bad); err == nil {
			t.Errorf("%+v accepted: %s", bad, s)
		}
	}
}

func TestParseColumnType(t *testing.T) {
	for in, want := range map[string][4]any{
		"int(10) unsigned":             {"INT", "", []string(nil), true},
		"varchar(255)":                 {"VARCHAR", "255", []string(nil), false},
		"decimal(10,2)":                {"DECIMAL", "10,2", []string(nil), false},
		"text":                         {"TEXT", "", []string(nil), false},
		"enum('a','b''c')":             {"ENUM", "", []string{"a", "b'c"}, false},
		"set('x,y','z')":               {"SET", "", []string{"x,y", "z"}, false},
		"datetime(3)":                  {"DATETIME", "3", []string(nil), false},
		"bigint(20) unsigned zerofill": {"BIGINT", "", []string(nil), true},
	} {
		typ, l, v, u := ParseColumnType(in)
		if typ != want[0] || l != want[1] || !reflect.DeepEqual(v, want[2]) || u != want[3] {
			t.Errorf("%s: %s %q %q %v", in, typ, l, v, u)
		}
	}
}

func TestRowsWhereSearchEscapesWildcards(t *testing.T) {
	ti := tableInfo{Columns: []string{"a", "b"}}
	w, err := rowsWhere(ti, &filterReq{Column: "a", Op: ">", Value: "5"}, "50%_x'")
	if err != nil {
		t.Fatal(err)
	}
	want := " WHERE `a` > '5' AND (CAST(`a` AS CHAR) LIKE '%50\\\\%\\\\_x\\'%' OR CAST(`b` AS CHAR) LIKE '%50\\\\%\\\\_x\\'%')"
	if w != want {
		t.Errorf("got  %s\nwant %s", w, want)
	}
	if _, err := rowsWhere(ti, &filterReq{Column: "c", Op: "="}, ""); err == nil {
		t.Error("unknown column accepted")
	}
	if w, _ := rowsWhere(ti, nil, "  "); w != "" {
		t.Errorf("blank search: %q", w)
	}
}

func TestParseCSV(t *testing.T) {
	recs, err := ParseCSV("\ufeffid;name\n1;\"a;b\"\n\n2;\"say \"\"hi\"\"\"\n", ';')
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"id", "name"}, {"1", "a;b"}, {"2", `say "hi"`}}
	if !reflect.DeepEqual(recs, want) {
		t.Errorf("got %q", recs)
	}
	if _, err := ParseCSV(strings.Repeat("1\n", csvImportRows+5), ','); err == nil {
		t.Error("too many rows accepted")
	}
}
