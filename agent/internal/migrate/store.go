package migrate

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Task and migration states.
const (
	Pending     = "pending"
	Running     = "running"
	Done        = "done"
	Failed      = "failed"
	Paused      = "paused"
	Cancelled   = "cancelled"
	Interrupted = "interrupted" // the agent stopped while it ran
	Completed   = "completed"
	Created     = "created"
)

var idRe = regexp.MustCompile(`^[a-f0-9]{16}$`)

// Task is one step. Its id says what it does, so a task that is done is
// never repeated and a resumed migration continues where it stopped.
type Task struct {
	ID          string     `json:"id"` // files:<dest root> or db:<dest db>
	Kind        string     `json:"kind"`
	Label       string     `json:"label"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	Error       string     `json:"error,omitempty"`
	Checkpoint  string     `json:"checkpoint,omitempty"` // dumped, importing, imported
	Files       int64      `json:"files,omitempty"`
	Bytes       int64      `json:"bytes,omitempty"`
	SHA256      string     `json:"sha256,omitempty"`
	Differences int        `json:"differences,omitempty"` // files that differed in the verification pass
	Detail      string     `json:"detail,omitempty"`
	Started     *time.Time `json:"started,omitempty"`
	Finished    *time.Time `json:"finished,omitempty"`
}

// Migration is the persisted state of one migration. It never holds a
// credential: continuing after an agent restart takes the login again.
type Migration struct {
	ID       string     `json:"id"`
	Status   string     `json:"status"`
	Panel    string     `json:"panel"`
	Source   string     `json:"source"` // ssh://user@host:port
	Plan     Plan       `json:"plan"`
	Tasks    []Task     `json:"tasks"`
	Created  time.Time  `json:"created"`
	Updated  time.Time  `json:"updated"`
	Finished *time.Time `json:"finished,omitempty"`
	Error    string     `json:"error,omitempty"`
	// Remaining lists what is left to clean up or finish by hand, so a failure
	// never leaves the destination in an unexplained state.
	Remaining []string `json:"remaining,omitempty"`
}

// Store keeps migrations as files in the agent's state folder.
type Store struct {
	Dir string
	mu  sync.Mutex
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Store) path(id string) (string, error) {
	if !idRe.MatchString(id) {
		return "", fmt.Errorf("invalid migration id")
	}
	return filepath.Join(s.Dir, id+".json"), nil
}

// Save writes the state atomically with restricted permissions.
func (s *Store) Save(m *Migration) error {
	p, err := s.path(m.ID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m.Updated = time.Now().UTC()
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Load reads one migration.
func (s *Store) Load(id string) (*Migration, error) {
	p, err := s.path(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("migration not found")
	}
	var m Migration
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("the state of this migration is damaged")
	}
	return &m, nil
}

// List returns every migration, newest first.
func (s *Store) List() []*Migration {
	files, _ := filepath.Glob(filepath.Join(s.Dir, "*.json"))
	var out []*Migration
	for _, f := range files {
		id := filepath.Base(f)
		if m, err := s.Load(id[:len(id)-5]); err == nil {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// Active reports whether a migration is not finished.
func Active(status string) bool {
	switch status {
	case Created, Running, Paused, Interrupted, Failed:
		return true
	}
	return false
}
