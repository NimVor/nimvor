package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// maxRead caps what one call reads of a website's access log.
const maxRead = 512 << 20

// Position is where the previous read of a website's access log stopped.
type Position struct {
	Domain string `json:"domain"`
	Root   string `json:"root"`
	Inode  uint64 `json:"inode"`
	Offset int64  `json:"offset"`
}

// Traffic is what a read found: bytes sent per month ("2026-09"), and
// where to continue next time.
type Traffic struct {
	Domain   string           `json:"domain"`
	Inode    uint64           `json:"inode"`
	Offset   int64            `json:"offset"`
	Months   map[string]int64 `json:"months"`
	NotFound bool             `json:"not_found,omitempty"`
}

var monthNum = map[string]string{"Jan": "01", "Feb": "02", "Mar": "03", "Apr": "04", "May": "05", "Jun": "06", "Jul": "07", "Aug": "08", "Sep": "09", "Oct": "10", "Nov": "11", "Dec": "12"}

// LineBytes returns the month and body bytes of an Nginx "combined" log
// line: `ip - user [27/Sep/2026:10:00:00 +0000] "GET / HTTP/1.1" 200 512 …`.
func LineBytes(line string) (string, int64, bool) {
	open := strings.IndexByte(line, '[')
	if open < 0 || len(line) < open+12 || line[open+3] != '/' || line[open+7] != '/' {
		return "", 0, false
	}
	mon, ok := monthNum[line[open+4:open+7]]
	if !ok {
		return "", 0, false
	}
	month := line[open+8:open+12] + "-" + mon
	// The request is the first quoted field; status and bytes follow it.
	q := strings.IndexByte(line[open:], '"')
	if q < 0 {
		return "", 0, false
	}
	rest := line[open+q+1:]
	end := strings.Index(rest, "\" ")
	if end < 0 {
		return "", 0, false
	}
	f := strings.Fields(rest[end+2:])
	if len(f) < 2 {
		return "", 0, false
	}
	n, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil || n < 0 {
		return month, 0, true // "-": nothing sent
	}
	return month, n, true
}

// ReadTraffic continues reading a log from pos (from the start when the
// file was replaced or truncated) and sums bytes per month.
func ReadTraffic(f *os.File, pos Position) (Traffic, error) {
	info, err := f.Stat()
	if err != nil {
		return Traffic{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return Traffic{}, errors.New("not a regular file")
	}
	t := Traffic{Domain: pos.Domain, Inode: st.Ino, Months: map[string]int64{}}
	offset := pos.Offset
	if st.Ino != pos.Inode || offset > info.Size() || offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return t, err
	}
	r := bufio.NewReaderSize(io.LimitReader(f, maxRead), 64<<10)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			// A partial last line is read again next time.
			break
		}
		offset += int64(len(line))
		if m, n, ok := LineBytes(line); ok {
			t.Months[m] += n
		}
	}
	t.Offset = offset
	return t, nil
}

// registerBandwidth adds sites.bandwidth.
func registerBandwidth(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "sites.bandwidth", Timeout: 5 * time.Minute, Description: "Bytes sent by websites per month, read from their access logs from where the last read stopped (read-only).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Sites []Position `json:"sites"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if len(in.Sites) > 1000 {
			return nil, actions.InvalidParams("sites: at most 1000")
		}
		out := []Traffic{}
		for _, s := range in.Sites {
			if err := validate.Domain("domain", s.Domain); err != nil {
				return nil, host.Invalid(err)
			}
			if s.Root == "" {
				s.Root = filepath.Join(env.Cfg.SitesRoot, s.Domain)
			}
			if err := validate.Within("root", env.Cfg.SitesRoot, s.Root); err != nil {
				return nil, host.Invalid(err)
			}
			if ctx.Err() != nil {
				break
			}
			t, err := readSite(env, s)
			if err != nil {
				out = append(out, Traffic{Domain: s.Domain, NotFound: true, Months: map[string]int64{}})
				continue
			}
			out = append(out, t)
		}
		return out, nil
	}})
}

// readSite opens logs/access.log inside the website folder (no symlink
// leads out of it; the log itself must be a regular file).
func readSite(env *host.Env, s Position) (Traffic, error) {
	real, err := env.Root.Path(s.Root)
	if err != nil {
		return Traffic{}, err
	}
	if info, err := os.Lstat(real); err != nil || !info.IsDir() {
		return Traffic{}, os.ErrNotExist
	}
	root, err := os.OpenRoot(real)
	if err != nil {
		return Traffic{}, err
	}
	defer root.Close()
	name := filepath.Join("logs", "access.log")
	if info, err := root.Lstat(name); err != nil || !info.Mode().IsRegular() {
		return Traffic{}, os.ErrNotExist
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Traffic{}, err
	}
	defer f.Close()
	return ReadTraffic(f, s)
}
