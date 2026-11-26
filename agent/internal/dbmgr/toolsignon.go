package dbmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// phpMyAdmin and Adminer sign in with a database account of their own per database (nimvor_t_<hash>@localhost),
// with privileges on that database only. It is locked except between a sign-in and its expiry: each sign-in gives
// it a fresh random password and unlocks it, and LockToolAccount locks it again with another one.

// ToolPrefix marks the tools' sign-in accounts; users cannot create such names.
const ToolPrefix = "nimvor_t_"

var toolAccountRe = regexp.MustCompile(`^` + ToolPrefix + `[0-9a-f]{16}$`)

// ToolAccountName returns the tools' sign-in account name for a database.
func ToolAccountName(db string) string {
	sum := sha256.Sum256([]byte("tool:" + db))
	return ToolPrefix + hex.EncodeToString(sum[:8])
}

// ToolSignon prepares the account for db and returns its name and a new password.
func (c Client) ToolSignon(ctx context.Context, db string) (user, password string, err error) {
	if err := validate.DBIdentifier("database", db); err != nil {
		return "", "", host.Invalid(err)
	}
	ok, err := c.databaseExists(ctx, db)
	if err != nil {
		return "", "", actions.Failed("The database server is not reachable.", err.Error())
	}
	if !ok {
		return "", "", actions.NotFound("database " + db + " does not exist")
	}
	user, password = ToolAccountName(db), randomPassword()
	acct := account(user, "localhost")
	setup := fmt.Sprintf("CREATE USER IF NOT EXISTS %s IDENTIFIED BY %s WITH MAX_USER_CONNECTIONS 6;\nALTER USER %s IDENTIFIED BY %s ACCOUNT UNLOCK;\nGRANT ALL PRIVILEGES ON %s.* TO %s;\n",
		acct, Quote(password), acct, Quote(password), Ident(db), acct)
	if err := c.Exec(ctx, "Preparing the sign-in account", setup); err != nil {
		return "", "", err
	}
	return user, password, nil
}

// LockToolAccount locks a tools' sign-in account with a password nobody knows.
func (c Client) LockToolAccount(ctx context.Context, user string) error {
	if !toolAccountRe.MatchString(user) {
		return host.Invalid(fmt.Errorf("%q is not a database tools account", user))
	}
	return c.Exec(ctx, "Locking the sign-in account", fmt.Sprintf("ALTER USER %s IDENTIFIED BY %s ACCOUNT LOCK;", account(user, "localhost"), Quote(randomPassword())))
}
