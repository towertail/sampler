package daemon

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/towertail/sampler/internal/push"
)

// State is the last known state of the sampler process. The running
// process writes it. `status` reads it.
type State struct {
	PID         int        `json:"pid"`
	Version     string     `json:"version"`
	Endpoint    string     `json:"endpoint"`
	LastPush    *time.Time `json:"last_push"`
	LastStatus  int        `json:"last_status"`
	LastError   string     `json:"last_error"`
	Update      string     `json:"update"`
	Interval    string     `json:"interval"`
	PausedUntil *time.Time `json:"paused_until"`
	Gone        string     `json:"gone"`
	Reboot      string     `json:"reboot"`
}

// Status is the JSON of the status command.
type Status struct {
	Running bool `json:"running"`
	State
}

// StateFile is the state file. It satisfies push.Recorder.
type StateFile struct {
	h  Home
	mu sync.Mutex
}

func (h Home) StateFile() *StateFile { return &StateFile{h: h} }

// Read returns the saved state. A missing file gives an empty state.
func (s *StateFile) Read() (State, error) {
	var st State
	b, err := os.ReadFile(s.h.StatePath())
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(b, &st)
	return st, err
}

// Reset starts a new state for a new process. It drops the orders and
// the results of the earlier process.
func (s *StateFile) Reset(pid int, version, endpoint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(State{PID: pid, Version: version, Endpoint: endpoint})
}

// PushResult merges one push result into the file. An order stays until
// a newer order replaces it.
func (s *StateFile) PushResult(r push.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.Read()
	if err != nil {
		st = State{}
	}
	st.LastStatus = r.Status
	st.LastError = r.Err
	if r.Status >= 200 && r.Status < 300 {
		at := r.At.UTC()
		st.LastPush = &at
		st.PausedUntil = nil
	}
	if r.Update != "" {
		st.Update = r.Update
	}
	if r.Interval > 0 {
		st.Interval = r.Interval.String()
	}
	if !r.PausedUntil.IsZero() {
		until := r.PausedUntil.UTC()
		st.PausedUntil = &until
	}
	if r.Gone != "" {
		st.Gone = r.Gone
	}
	_ = s.save(st)
}

func (s *StateFile) save(st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return s.h.write(s.h.StatePath(), b)
}
