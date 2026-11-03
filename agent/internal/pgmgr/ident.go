package pgmgr

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/NimVor/nimvor/agent/internal/actions"
)

// Names of databases and roles NimVor creates are a strict subset of what PostgreSQL allows: lower-case
// letters, digits and underscores, starting with a letter or underscore, at most 63 bytes. Everything that
// reaches SQL as an identifier is validated here AND passed through the server's own format('%I') quoting.
var nameRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// AdminRole is the role NimVor provisions with; customers never get it and it is never listed as manageable.
const AdminRole = "nimvor_admin"

var reservedNames = map[string]bool{
	"postgres": true, "template0": true, "template1": true, "public": true, "none": true,
	AdminRole: true, "replication": true, "rdsadmin": true,
}

// ValidName checks a database or role name a customer or administrator may create.
func ValidName(field, s string) error {
	if !nameRe.MatchString(s) {
		return actions.InvalidParams(field + ": use lower-case letters, digits and underscores, starting with a letter or underscore, at most 63 characters.")
	}
	if reservedNames[s] || strings.HasPrefix(s, "pg_") || strings.HasPrefix(s, "nimvor_") {
		return actions.InvalidParams(field + ": " + s + " is reserved.")
	}
	return nil
}

// ValidPassword checks a database password: long enough, no control characters (they cannot be sent safely).
func ValidPassword(field, p string) error {
	if len(p) < 12 || len(p) > 128 {
		return actions.InvalidParams(field + ": use 12 to 128 characters.")
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return actions.InvalidParams(field + ": control characters are not allowed.")
		}
	}
	return nil
}

// Quote returns name as a quoted SQL identifier. The names are validated first; this is the second line of defence.
func Quote(name string) string { return pgx.Identifier{name}.Sanitize() }

// Level is how much a role may do in one database.
type Level string

// The levels a role can hold on a database. "none" removes every privilege NimVor grants.
const (
	LevelNone      Level = "none"
	LevelReadOnly  Level = "readonly"
	LevelReadWrite Level = "readwrite"
	LevelAll       Level = "all"
)

// ValidLevel checks a privilege level.
func ValidLevel(field string, l Level) error {
	switch l {
	case LevelNone, LevelReadOnly, LevelReadWrite, LevelAll:
		return nil
	}
	return actions.InvalidParams(fmt.Sprintf("%s: choose none, readonly, readwrite or all.", field))
}
