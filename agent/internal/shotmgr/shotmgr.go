// Package shotmgr takes the picture of a website's home page that the
// panel shows on the website's card. A headless browser opens the site's
// own address (mapped to this server, so it never goes through DNS or a
// CDN) as the website's own account inside the usual service sandbox, so a
// hostile page can do no more than the site's PHP already can. The picture
// is scaled down and kept as a small JPEG in the agent's state folder.
package shotmgr

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

const (
	// WindowW and WindowH are the browser window: the picture shows the top of the page.
	WindowW, WindowH = 1280, 800
	// ThumbW is the width of the kept picture.
	ThumbW = 640
	// maxPNG bounds what is read back from the (untrusted) screenshot file.
	maxPNG = 24 << 20
	// captureTimeout bounds one picture.
	captureTimeout = 80 * time.Second
)

// Info says when a picture was taken and how big it is.
type Info struct {
	TakenAt string `json:"taken_at"`
	Bytes   int    `json:"bytes"`
}

func dir(env *host.Env) string { return filepath.Join(env.Cfg.StateDir, "screenshots") }

func file(env *host.Env, domain string) string { return filepath.Join(dir(env), domain+".jpg") }

// Installed reports whether a browser is available.
func Installed(env *host.Env) bool { return env.Runner.Available("chrome") }

// Capture photographs the home page of the website with this domain, user and folder.
func Capture(ctx context.Context, env *host.Env, domain, user, root, ipv4 string) (Info, error) {
	if err := validate.Domain("domain", domain); err != nil {
		return Info{}, host.Invalid(err)
	}
	// A website with its own address is served only there (its Nginx "listen" names that address), so the
	// browser is pointed at it; otherwise at this server's loopback address.
	addrs := []string{"127.0.0.1"}
	if ipv4 != "" {
		ip := net.ParseIP(ipv4)
		if ip == nil || ip.To4() == nil {
			return Info{}, actions.InvalidParams("ipv4: not an IPv4 address")
		}
		addrs = []string{ip.To4().String(), "127.0.0.1"}
	}
	if root == "" {
		root = filepath.Join(env.Cfg.SitesRoot, domain)
	}
	if err := validate.Within("root", env.Cfg.SitesRoot, root); err != nil {
		return Info{}, host.Invalid(err)
	}
	if !Installed(env) {
		return Info{}, actions.Conflict("No browser is installed to take the picture. An administrator can install one on the Websites page.")
	}
	cred, _, err := env.Credential(user)
	if err != nil {
		return Info{}, actions.InvalidParams("user: " + err.Error())
	}
	tok := make([]byte, 8)
	if _, err := rand.Read(tok); err != nil {
		return Info{}, err
	}
	// The folder lives in the agent's run directory, not in /tmp: the agent has a private /tmp that the
	// transient unit (started by PID 1) does not see, so the browser could not even enter its working folder.
	tmp := execx.RunDir + "/shot-" + hex.EncodeToString(tok)
	realParent, err := env.Root.Path(execx.RunDir)
	if err != nil {
		return Info{}, err
	}
	if err := os.MkdirAll(realParent, 0o711); err != nil {
		return Info{}, fmt.Errorf("create %s: %w", execx.RunDir, err)
	}
	if err := os.Chmod(realParent, 0o711); err != nil {
		return Info{}, err
	}
	realTmp, err := env.Root.Path(tmp)
	if err != nil {
		return Info{}, err
	}
	// A private folder for the account: its profile and the picture.
	if err := os.Mkdir(realTmp, 0o700); err != nil {
		return Info{}, fmt.Errorf("create %s: %w", tmp, err)
	}
	defer os.RemoveAll(realTmp)
	if err := os.Chown(realTmp, int(cred.UID), int(cred.GID)); err != nil && os.Geteuid() == 0 {
		return Info{}, fmt.Errorf("give %s to the website's account: %w", tmp, err)
	}
	shot := tmp + "/shot.png"
	// The page is opened over HTTPS first and, for a website without a
	// certificate (or one that does not answer on 443), over plain HTTP; each at the website's own address
	// first when it has one.
	var (
		img     image.Image
		out     []byte
		runErr  error
		readErr error
		notes   []string
	)
try:
	for _, addr := range addrs {
		for _, scheme := range []string{"https", "http"} {
			os.Remove(realTmp + "/shot.png")
			args := []string{
				"--headless=new", "--disable-gpu", "--hide-scrollbars", "--no-first-run", "--no-default-browser-check",
				"--disable-extensions", "--disable-dev-shm-usage", "--mute-audio", "--ignore-certificate-errors",
				// The service sandbox the command runs in (own account, no capabilities, no new privileges) is the
				// containment; the browser's own sandbox cannot be started inside it.
				"--no-sandbox", "--disable-crash-reporter", "--disable-background-networking",
				fmt.Sprintf("--window-size=%d,%d", WindowW, WindowH), "--virtual-time-budget=6000",
				"--user-data-dir=" + tmp + "/profile", "--screenshot=" + shot,
				"--host-resolver-rules=MAP " + domain + " " + addr,
				scheme + "://" + domain + "/",
			}
			cctx, cancel := context.WithTimeout(ctx, captureTimeout)
			out, runErr = env.Runner.RunWith(cctx, "chrome", execx.Opts{As: cred, Dir: tmp, Env: []string{"HOME=" + tmp}, Timeout: captureTimeout}, args...)
			cancel()
			img, readErr = readPNG(realTmp+"/shot.png", cred.UID)
			if readErr == nil {
				break try
			}
			notes = append(notes, attemptNote(scheme+" via "+addr, runErr, readErr, out, realTmp))
		}
	}
	if readErr != nil {
		msg := "The browser did not produce a picture"
		if runErr != nil {
			msg += ": " + runErr.Error()
		}
		return Info{}, actions.Failed(msg+".", strings.Join(notes, "\n"))
	}
	jpg, err := thumbnail(img)
	if err != nil {
		return Info{}, err
	}
	if _, err := env.Writer.MkdirAll(dir(env), 0o755, agentfs.RootOwner); err != nil {
		return Info{}, err
	}
	if _, err := env.Writer.WriteFile(file(env, domain), jpg, 0o644, agentfs.RootOwner); err != nil {
		return Info{}, err
	}
	return Info{TakenAt: time.Now().UTC().Format(time.RFC3339), Bytes: len(jpg)}, nil
}

// readPNG reads the picture the account's browser left: a regular file the
// account owns, opened without following links, decoded as an image (so
// nothing else it might point at can leak).
func readPNG(path string, uid uint32) (image.Image, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Size() > maxPNG || (ok && os.Geteuid() == 0 && st.Uid != uid) {
		return nil, errors.New("not the browser's picture")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	return png.Decode(io.LimitReader(f, maxPNG))
}

// thumbnail scales the picture down to ThumbW wide by averaging the source
// pixels each new pixel covers, and encodes it as a JPEG.
func thumbnail(src image.Image) ([]byte, error) {
	b := src.Bounds()
	if b.Dx() < 1 || b.Dy() < 1 {
		return nil, errors.New("empty picture")
	}
	w := ThumbW
	if b.Dx() < w {
		w = b.Dx()
	}
	h := b.Dy() * w / b.Dx()
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1 := b.Min.Y+y*b.Dy()/h, b.Min.Y+(y+1)*b.Dy()/h
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < w; x++ {
			x0, x1 := b.Min.X+x*b.Dx()/w, b.Min.X+(x+1)*b.Dx()/w
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					cr, cg, cb, _ := src.At(xx, yy).RGBA()
					r, g, bl, n = r+uint64(cr>>8), g+uint64(cg>>8), bl+uint64(cb>>8), n+1
				}
			}
			dst.Set(x, y, color.RGBA{uint8(r / n), uint8(g / n), uint8(bl / n), 255})
		}
	}
	var buf limitedBuf
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 78}); err != nil {
		return nil, err
	}
	return buf.b, nil
}

type limitedBuf struct{ b []byte }

func (l *limitedBuf) Write(p []byte) (int, error) { l.b = append(l.b, p...); return len(p), nil }

// Register adds the screenshot actions.
func Register(r *actions.Registry, env *host.Env) {
	registerInstall(r, env)
	r.Register(actions.Action{Name: "screenshot.status", Description: "Whether a browser is installed for website pictures (read-only).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"installed": Installed(env)}, nil
	}})
	r.Register(actions.Action{Name: "screenshot.capture", Mutating: true, Timeout: captureTimeout + 20*time.Second, Description: "Take a picture of a website's home page with a headless browser run as the website's own account in the service sandbox, scaled down and kept for the panel.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
			User   string `json:"user"`
			Root   string `json:"root"`
			IPv4   string `json:"ipv4"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return Capture(ctx, env, in.Domain, in.User, in.Root, in.IPv4)
	}})
	r.Register(actions.Action{Name: "screenshot.get", Description: "The kept picture of a website's home page (JPEG, base64) and when it was taken; exists is false when there is none (read-only).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("domain", in.Domain); err != nil {
			return nil, host.Invalid(err)
		}
		path := file(env, in.Domain)
		info, err := env.Root.Stat(path)
		if err != nil {
			return map[string]any{"exists": false}, nil
		}
		b, err := env.Root.ReadFile(path)
		if err != nil {
			return map[string]any{"exists": false}, nil
		}
		return map[string]any{"exists": true, "taken_at": info.ModTime().UTC().Format(time.RFC3339), "jpeg": base64.StdEncoding.EncodeToString(b)}, nil
	}})
}

// browserTail is what the browser printed, without the desktop-bus noise a
// server has no use for, cut to the last lines: the part that says why it
// stopped.
func browserTail(out []byte) string {
	var keep []string
	for _, l := range strings.Split(host.Output(out), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.Contains(l, "dbus/") || strings.Contains(l, "zygote") {
			continue
		}
		keep = append(keep, l)
	}
	if len(keep) > 12 {
		keep = keep[len(keep)-12:]
	}
	t := strings.Join(keep, "\n")
	if len(t) > 1500 {
		t = t[len(t)-1500:]
	}
	return t
}

// attemptNote says what one try came to: how the browser ended, why the
// picture could not be used, what the browser printed and what is in its
// folder (name, owner, size), so a failure can be understood from the
// message alone.
func attemptNote(scheme string, runErr, readErr error, out []byte, folder string) string {
	run := "the browser ended normally"
	if runErr != nil {
		run = "the browser ended with: " + runErr.Error()
	}
	var files []string
	if entries, err := os.ReadDir(folder); err == nil {
		for _, e := range entries {
			if info, err := e.Info(); err == nil {
				owner := "?"
				if st, ok := info.Sys().(*syscall.Stat_t); ok {
					owner = fmt.Sprint(st.Uid)
				}
				files = append(files, fmt.Sprintf("%s (owner %s, %d bytes)", e.Name(), owner, info.Size()))
			}
		}
	}
	note := fmt.Sprintf("%s: %s; the picture: %v; the browser's folder holds: %s", scheme, run, readErr, strings.Join(files, ", "))
	if t := browserTail(out); t != "" {
		note += "\n" + t
	}
	return note
}
