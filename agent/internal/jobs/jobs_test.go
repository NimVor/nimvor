package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func mgr(t *testing.T) *Manager {
	m, err := NewManager(t.TempDir(), 2, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestJobLifecycleAndRedaction(t *testing.T) {
	m := mgr(t)
	j := m.Start("demo", "site:1", func(ctx context.Context, log io.Writer) (any, error) {
		fmt.Fprintln(log, "step one")
		fmt.Fprintln(log, "mysql -pHunter2 db")
		return map[string]int{"n": 1}, nil
	})
	done, err := m.Wait(context.Background(), j.ID)
	if err != nil || done.Status != Succeeded {
		t.Fatalf("job = %+v, %v", done, err)
	}
	out, next, _ := m.Log(j.ID, 0)
	if !strings.Contains(out, "step one") || strings.Contains(out, "Hunter2") || next == 0 {
		t.Fatalf("log = %q", out)
	}
	more, _, _ := m.Log(j.ID, next)
	if more != "" {
		t.Fatalf("offset not honoured: %q", more)
	}
}

func TestFailureAndSerialisationPerTarget(t *testing.T) {
	m := mgr(t)
	order := make(chan string, 2)
	a := m.Start("a", "same", func(ctx context.Context, log io.Writer) (any, error) {
		time.Sleep(50 * time.Millisecond)
		order <- "a"
		return nil, errors.New("boom")
	})
	time.Sleep(5 * time.Millisecond)
	b := m.Start("b", "same", func(ctx context.Context, log io.Writer) (any, error) {
		order <- "b"
		return nil, nil
	})
	ja, _ := m.Wait(context.Background(), a.ID)
	m.Wait(context.Background(), b.ID)
	if ja.Status != Failed || ja.Error != "boom" {
		t.Fatalf("a = %+v", ja)
	}
	if first := <-order; first != "a" {
		t.Fatal("jobs on the same target must not overlap")
	}
	if _, _, err := m.Log("../../etc/passwd", 0); !errors.Is(err, ErrUnknownJob) {
		t.Fatal("log id must be validated")
	}
}
