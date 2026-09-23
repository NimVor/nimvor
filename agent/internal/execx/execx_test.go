package execx

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSystemRunnerRejectsUnknownCommands(t *testing.T) {
	r := NewSystemRunner(time.Second)
	for _, id := range []string{"sh", "bash", "/bin/sh", "rm", "curl"} {
		if _, err := r.Run(context.Background(), id, "-c", "id"); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("Run(%q) err = %v, want ErrNotAllowed", id, err)
		}
	}
}

func TestSystemRunnerRejectsControlCharacters(t *testing.T) {
	r := &SystemRunner{paths: map[string]string{"systemctl": "/bin/true"}}
	if _, err := r.Run(context.Background(), "systemctl", "show\nx"); err == nil {
		t.Fatal("expected invalid argument error")
	}
}

func TestLimitedBuffer(t *testing.T) {
	b := limitedBuffer{limit: 4}
	b.Write([]byte("abc"))
	n, _ := b.Write([]byte("defg"))
	if n != 4 || b.String() != "abcd" {
		t.Fatalf("buffer = %q (n=%d)", b.String(), n)
	}
}
