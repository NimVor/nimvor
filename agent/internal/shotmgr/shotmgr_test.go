package shotmgr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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
	info, err := Capture(context.Background(), h.Env, "shop.test", "example", "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Bytes == 0 || info.TakenAt == "" {
		t.Fatalf("info: %+v", info)
	}
	joined := strings.Join(*args, " ")
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
	left, _ := filepath.Glob(filepath.Join(h.Base, "tmp", "nimvor-shot-*"))
	if len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

func TestCaptureRefusesBadInputAndReportsAFailedBrowser(t *testing.T) {
	h := testhost.New(t)
	fakeBrowser(t, h, color.White)
	if _, err := Capture(context.Background(), h.Env, "../etc", "example", ""); err == nil {
		t.Error("a bad domain was accepted")
	}
	if _, err := Capture(context.Background(), h.Env, "shop.test", "root", ""); err == nil {
		t.Error("a system account was accepted")
	}
	if _, err := Capture(context.Background(), h.Env, "shop.test", "example", "/etc"); err == nil {
		t.Error("a folder outside the websites was accepted")
	}
	// The browser runs but leaves no picture.
	h.Respond = func(id string, args []string) (string, error, bool) { return "boom", nil, id == "chrome" }
	_, err := Capture(context.Background(), h.Env, "shop.test", "example", "")
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
	if _, err := Capture(context.Background(), h.Env, "shop.test", "example", ""); err != nil {
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
