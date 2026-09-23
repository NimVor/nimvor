package execx

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SimRunner simulates system tools for development against a fixture tree
// (root_dir != "/"). It never executes anything. Account changes are
// applied to <Base>/etc/passwd so the rest of the agent behaves normally.
type SimRunner struct {
	Base    string
	NextUID int
	mu      sync.Mutex
	Log     io.Writer
}

// Available reports every allowlisted command as installed.
func (s *SimRunner) Available(id string) bool {
	_, known := defaultCandidates[id]
	return known || phpID.MatchString(id) || phpFPMID.MatchString(id)
}

// Run simulates a command.
func (s *SimRunner) Run(ctx context.Context, id string, args ...string) ([]byte, error) {
	return s.RunWith(ctx, id, Opts{}, args...)
}

// RunWith simulates a command and returns plausible output.
func (s *SimRunner) RunWith(_ context.Context, id string, o Opts, args ...string) ([]byte, error) {
	if !s.Available(id) {
		return nil, fmt.Errorf("%w: %s", ErrNotAllowed, id)
	}
	if err := checkArgs(id, o, args); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(id + " " + strings.Join(args, " "))
	if s.Log != nil {
		fmt.Fprintf(s.Log, "[simulated] %s\n", key)
	}
	out := "[simulated] " + key + "\n"
	switch {
	case key == "nginx -v":
		out = "nginx version: nginx/1.24.0 (simulated)"
	case id == "nginx" && len(args) > 0 && args[0] == "-t":
		out = "nginx: configuration file test is successful (simulated)"
	case id == "systemctl" && len(args) > 0 && args[0] == "is-active":
		out = "active"
	case id == "useradd":
		s.addUser(args[len(args)-1])
	case id == "userdel":
		s.delUser(args[len(args)-1])
	}
	if o.Output != nil {
		io.WriteString(o.Output, out)
	}
	return []byte(out), nil
}

func (s *SimRunner) passwd() string { return filepath.Join(s.Base, "etc/passwd") }

func (s *SimRunner) addUser(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := os.ReadFile(s.passwd())
	if strings.Contains(string(b), "\n"+name+":") || strings.HasPrefix(string(b), name+":") {
		return
	}
	uid := s.NextUID
	if uid == 0 {
		uid = 1500
	}
	for strings.Contains(string(b), fmt.Sprintf(":%d:%d:", uid, uid)) {
		uid++
	}
	s.NextUID = uid + 1
	gid := uid
	if os.Geteuid() != 0 {
		uid, gid = os.Getuid(), os.Getgid() // cannot chown to others
	}
	f, err := os.OpenFile(s.passwd(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s:x:%d:%d:simulated:/home/whacp:/usr/sbin/nologin\n", name, uid, gid)
}

func (s *SimRunner) delUser(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := os.ReadFile(s.passwd())
	var keep []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if !strings.HasPrefix(l, name+":") {
			keep = append(keep, l)
		}
	}
	os.WriteFile(s.passwd(), []byte(strings.Join(keep, "\n")+"\n"), 0o644)
}
