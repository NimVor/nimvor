package shotmgr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func fakeBrowser(t *testing.T, h *testhost.Host, paint color.Color) *[]string {
	t.Helper()
	os.MkdirAll(filepath.Join(h.Base, "tmp"), 0o755)
	var seen []string
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id != "chrome" {
			return "", nil, false
		}
		seen = args
		for _, a := range args {
			if p, ok := strings.CutPrefix(a, "--screenshot="); ok {
				real, _ := h.Env.Root.Path(p)
				img := image.NewRGBA(image.Rect(0, 0, WindowW, WindowH))
				for y := 0; y < WindowH; y++ {
					for x := 0; x < WindowW; x++ {
						img.Set(x, y, paint)
					}
				}
				var b bytes.Buffer
				png.Encode(&b, img)
				os.WriteFile(real, b.Bytes(), 0o644)
				os.Chown(real, h.UID, h.GID) // the browser runs as the website's account
			}
		}
		return "", nil, true
	}
	return &seen
}

func TestCaptureKeepsAScaledPictureAndNothingBehind(t *testing.T) {
	h := testhost.New(t)
	args := fakeBrowser(t, h, color.RGBA{200, 40, 40, 255})
	info, err := Capture(context.Background(), h.Env, "shop.test", "example", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Bytes == 0 || info.TakenAt == "" {
		t.Fatalf("info: %+v", info)
	}
	joined := strings.Join(*args, " ")
	// Not in /tmp: the agent's /tmp is private and the unit PID 1 starts does not see it.
	if strings.Contains(joined, "--screenshot=/tmp") || !strings.Contains(joined, "--screenshot=/run/nimvor-run/shot-") || !strings.Contains(joined, "--user-data-dir=/run/nimvor-run/shot-") {
		t.Errorf("the browser's folder is not in the run directory: %s", joined)
	}
	for _, want := range []string{"--headless=new", "--host-resolver-rules=MAP shop.test 127.0.0.1", "https://shop.test/", "--window-size=1280,800"} {
		if !strings.Contains(joined, want) {
			t.Errorf("browser command lacks %q: %s", want, joined)
		}
	}
	raw, err := os.ReadFile(filepath.Join(h.Base, "var/lib/nimvor-agent/screenshots/shop.test.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != ThumbW || b.Dy() != ThumbW*WindowH/WindowW {
		t.Errorf("size %v", b)
	}
	r, g, _, _ := img.At(10, 10).RGBA()
	if r>>8 < 150 || g>>8 > 100 {
		t.Errorf("colour lost: %d %d", r>>8, g>>8)
	}
	// The private folder is gone.
	left, _ := filepath.Glob(filepath.Join(h.Base, "run", "nimvor-run", "shot-*"))
	if len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

func TestCaptureRefusesBadInputAndReportsAFailedBrowser(t *testing.T) {
	h := testhost.New(t)
	fakeBrowser(t, h, color.White)
	if _, err := Capture(context.Background(), h.Env, "../etc", "example", "", ""); err == nil {
		t.Error("a bad domain was accepted")
	}
	if _, err := Capture(context.Background(), h.Env, "shop.test", "root", "", ""); err == nil {
		t.Error("a system account was accepted")
	}
	if _, err := Capture(context.Background(), h.Env, "shop.test", "example", "/etc", ""); err == nil {
		t.Error("a folder outside the websites was accepted")
	}
	// The browser runs but leaves no picture.
	h.Respond = func(id string, args []string) (string, error, bool) { return "boom", nil, id == "chrome" }
	_, err := Capture(context.Background(), h.Env, "shop.test", "example", "", "")
	if err == nil || !strings.Contains(err.Error(), "did not produce a picture") {
		t.Errorf("err = %v", err)
	}
}

func TestScreenshotGetAnswersWithoutAPicture(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	out := call(t, r, "screenshot.get", `{"domain":"none.test"}`)
	if out["exists"] != false {
		t.Errorf("out: %v", out)
	}
	fakeBrowser(t, h, color.RGBA{10, 20, 30, 255})
	if _, err := Capture(context.Background(), h.Env, "shop.test", "example", "", ""); err != nil {
		t.Fatal(err)
	}
	out = call(t, r, "screenshot.get", `{"domain":"shop.test"}`)
	b, err := base64.StdEncoding.DecodeString(out["jpeg"].(string))
	if out["exists"] != true || err != nil || len(b) == 0 || out["taken_at"] == "" {
		t.Errorf("out: %v", out)
	}
}

func call(t *testing.T, r *actions.Registry, name, params string) map[string]any {
	t.Helper()
	a, err := r.Lookup(name)
	if err != nil {
		t.Fatalf("no %s", name)
	}
	res, err := a.Handler(context.Background(), json.RawMessage(params))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	b, _ := json.Marshal(res)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

func TestCaptureOpensThePageOverHTTPWhenHTTPSGivesNoPicture(t *testing.T) {
	h := testhost.New(t)
	fakeBrowser(t, h, color.RGBA{10, 120, 200, 255})
	paint := h.Respond
	var urls []string
	h.Respond = func(id string, args []string) (string, error, bool) {
		page := args[len(args)-1]
		urls = append(urls, page)
		if strings.HasPrefix(page, "https://") {
			return "ERROR: ssl handshake failed\n[1:1:dbus/bus.cc:405] no bus\n", errors.New("exit status 1"), true
		}
		return paint(id, args)
	}
	if _, err := Capture(context.Background(), h.Env, "plain.test", "example", "", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Join(urls, " ") != "https://plain.test/ http://plain.test/" {
		t.Errorf("pages opened: %v", urls)
	}
}

func TestCaptureFailureShowsWhatTheBrowserSaid(t *testing.T) {
	h := testhost.New(t)
	os.MkdirAll(filepath.Join(h.Base, "tmp"), 0o755)
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id != "chrome" {
			return "", nil, false
		}
		return "[1:1:dbus/bus.cc:405] no bus\nFatal: shared library libnss3.so is missing\n", errors.New("exit status 1"), true
	}
	_, err := Capture(context.Background(), h.Env, "x.test", "example", "", "")
	var ae *actions.Error
	if !errors.As(err, &ae) {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(ae.Details, "libnss3.so is missing") || strings.Contains(ae.Details, "dbus") {
		t.Errorf("details: %q", ae.Details)
	}
	if !strings.Contains(ae.Message, "exit status 1") {
		t.Errorf("message: %q", ae.Message)
	}
	// Each try says how the browser ended and why there was no usable picture.
	for _, want := range []string{"https via 127.0.0.1: the browser ended with: exit status 1", "http via 127.0.0.1: the browser ended with: exit status 1", "the picture:", "the browser's folder holds:"} {
		if !strings.Contains(ae.Details, want) {
			t.Errorf("details lack %q: %q", want, ae.Details)
		}
	}
}

func TestCaptureUsesTheWebsitesOwnAddressFirst(t *testing.T) {
	h := testhost.New(t)
	fakeBrowser(t, h, color.RGBA{10, 120, 200, 255})
	paint := h.Respond
	var maps []string
	h.Respond = func(id string, args []string) (string, error, bool) {
		for _, a := range args {
			if strings.HasPrefix(a, "--host-resolver-rules=") {
				maps = append(maps, a)
			}
		}
		// Only the website's own address answers.
		if !strings.Contains(strings.Join(args, " "), " 127.0.0.2") {
			return "Page load failed: net::ERR_CONNECTION_REFUSED\n", nil, true
		}
		return paint(id, args)
	}
	if _, err := Capture(context.Background(), h.Env, "own.test", "example", "", "127.0.0.2"); err != nil {
		t.Fatal(err)
	}
	if len(maps) == 0 || maps[0] != "--host-resolver-rules=MAP own.test 127.0.0.2" {
		t.Errorf("maps: %v", maps)
	}
}

func TestCaptureFallsBackToLoopbackAndRefusesABadAddress(t *testing.T) {
	h := testhost.New(t)
	fakeBrowser(t, h, color.RGBA{10, 120, 200, 255})
	paint := h.Respond
	var maps []string
	h.Respond = func(id string, args []string) (string, error, bool) {
		maps = append(maps, args[len(args)-2])
		if !strings.Contains(args[len(args)-2], " 127.0.0.1") {
			return "Page load failed: net::ERR_CONNECTION_REFUSED\n", nil, true
		}
		return paint(id, args)
	}
	if _, err := Capture(context.Background(), h.Env, "own.test", "example", "", "127.0.0.2"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(maps, ","); !strings.Contains(got, "MAP own.test 127.0.0.2") || !strings.Contains(got, "MAP own.test 127.0.0.1") {
		t.Errorf("maps: %s", got)
	}
	if _, err := Capture(context.Background(), h.Env, "own.test", "example", "", "not-an-ip"); err == nil {
		t.Error("a bad address was accepted")
	}
}
