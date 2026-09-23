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

func TestShellOnlyAsUnprivilegedUser(t *testing.T) {
	r := &SystemRunner{paths: map[string]string{"sh": "/bin/sh"}}
	for _, o := range []Opts{{}, {As: &Credential{UID: 0, GID: 0}}, {As: &Credential{UID: 33, GID: 33}}} {
		if _, err := r.RunWith(context.Background(), "sh", o, "-c", "id"); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("shell as %+v: %v", o.As, err)
		}
	}
}

func TestVersionedPHPIDs(t *testing.T) {
	r := &SystemRunner{paths: map[string]string{}}
	if _, err := r.resolve("php8.3; id"); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("injected id: %v", err)
	}
	if _, err := r.resolve("php-fpm8.3"); errors.Is(err, ErrNotAllowed) {
		t.Errorf("php-fpm8.3 should be allowlisted: %v", err)
	}
}
