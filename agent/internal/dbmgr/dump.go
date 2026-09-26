package dbmgr

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
)

// Dump writes a consistent, gzip-compressed dump of name to dest (a host
// path inside the backup directory) and returns its size.
func (c Client) Dump(ctx context.Context, name, dest string, log io.Writer) (int64, error) {
	e := c.Env
	if _, err := e.Writer.MkdirAll(filepath.Dir(dest), 0o700, agentfs.RootOwner); err != nil {
		return 0, err
	}
	real, err := e.Root.Path(dest)
	if err != nil {
		return 0, err
	}
	tmp := real + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp)
	if err := c.DumpTo(ctx, name, f, log); err != nil {
		f.Close()
		return 0, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return 0, err
	}
	f.Close()
	if err := os.Rename(tmp, real); err != nil {
		return 0, err
	}
	info, _ := os.Stat(real)
	fmt.Fprintf(log, "Wrote %s (%d bytes)\n", dest, info.Size())
	return info.Size(), nil
}

// DumpTo streams a gzip-compressed dump of name to w.
func (c Client) DumpTo(ctx context.Context, name string, w io.Writer, log io.Writer) error {
	gz := gzip.NewWriter(w)
	args := c.args("--single-transaction", "--quick", "--routines", "--triggers", "--events", "--hex-blob", "--no-tablespaces", "--", name)
	// mysqldump shares the client flags except --batch/--raw/--skip-column-names.
	args = filterArgs(args, "--batch", "--skip-column-names", "--raw")
	fmt.Fprintf(log, "Dumping database %s…\n", name)
	if errOut, err := c.Env.Runner.RunWith(ctx, "mysqldump", execx.Opts{Stdout: gz, Output: log, Timeout: -1}, args...); err != nil {
		return fmt.Errorf("mysqldump failed: %s", strings.TrimSpace(string(errOut)))
	}
	return gz.Close()
}

// ImportFrom loads SQL read from r (plain text) into name. The client's own
// commands are turned off (--sandbox on MariaDB, --commands=OFF on MySQL):
// the SQL comes from backups and files a customer can influence, and the
// client runs as root, where "\! command" or "system command" in the input
// would run a shell. A client that cannot turn them off is refused.
func (c Client) ImportFrom(ctx context.Context, name string, r io.Reader, log io.Writer) error {
	guard := c.clientGuard(ctx)
	if guard == "" {
		return fmt.Errorf("the mysql client on this server cannot disable its shell commands (--sandbox or --commands); update the MariaDB/MySQL client packages before importing SQL")
	}
	args := filterArgs(c.args(guard, "--", name), "--skip-column-names", "--raw")
	if _, err := c.Env.Runner.RunWith(ctx, "mysql", execx.Opts{Stdin: r, Output: log, Timeout: -1}, args...); err != nil {
		return fmt.Errorf("import failed")
	}
	return nil
}

// Import loads a .sql or .sql.gz file into name.
func (c Client) Import(ctx context.Context, name, source string, log io.Writer) error {
	real, err := c.Env.Root.Path(source)
	if err != nil {
		return err
	}
	f, err := os.Open(real)
	if err != nil {
		return fmt.Errorf("cannot open %s", source)
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(source, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("%s is not a valid gzip file", source)
		}
		defer gz.Close()
		r = gz
	}
	fmt.Fprintf(log, "Importing %s into %s…\n", source, name)
	return c.ImportFrom(ctx, name, r, log)
}

func filterArgs(args []string, drop ...string) []string {
	out := args[:0:0]
	for _, a := range args {
		skip := false
		for _, d := range drop {
			if a == d {
				skip = true
			}
		}
		if !skip {
			out = append(out, a)
		}
	}
	return out
}
