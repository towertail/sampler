// Package daemon owns the files of the background sampler under
// ~/.towertail: the lock, the pid, the log, the saved config, the token
// and the state of the last push.
package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrRunning means that another sampler process holds the lock.
	ErrRunning = errors.New("sampler already runs")
	// ErrUnsupported is the error of the daemon commands on Windows.
	ErrUnsupported = errors.New("unsupported on windows")
	// ErrStartTimeout means that the process did not take the lock in time.
	ErrStartTimeout = errors.New("sampler did not start")
)

// Home is the file layout under the sampler folder.
type Home struct {
	Dir string
}

// NewHome returns the layout of dir. An empty dir is $HOME/.towertail.
func NewHome(dir string) Home {
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".towertail")
		} else {
			dir = ".towertail"
		}
	}
	return Home{Dir: dir}
}

func (h Home) Bin() string        { return filepath.Join(h.Dir, "bin", "towertail-sampler") }
func (h Home) LockPath() string   { return filepath.Join(h.Dir, "sampler.lock") }
func (h Home) PIDPath() string    { return filepath.Join(h.Dir, "sampler.pid") }
func (h Home) LogPath() string    { return filepath.Join(h.Dir, "sampler.log") }
func (h Home) ConfigPath() string { return filepath.Join(h.Dir, "sampler.json") }
func (h Home) TokenPath() string  { return filepath.Join(h.Dir, "sampler.token") }
func (h Home) StatePath() string  { return filepath.Join(h.Dir, "sampler.state.json") }

// StartConfig is the saved config of `start`. It holds no token.
type StartConfig struct {
	Endpoint      string        `json:"endpoint"`
	Interval      time.Duration `json:"interval"`
	AllowControl  bool          `json:"allow_control"`
	AllowDiskScan bool          `json:"allow_disk_scan"`
	Insecure      bool          `json:"insecure"`
	// Boot is the reboot entry that is set: linger, launchagent, cron, none.
	Boot string `json:"boot"`
}

func (h Home) SaveConfig(c StartConfig) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return h.write(h.ConfigPath(), b)
}

// LoadConfig reads the saved config. It returns os.ErrNotExist when no
// config was saved.
func (h Home) LoadConfig() (StartConfig, error) {
	var c StartConfig
	b, err := os.ReadFile(h.ConfigPath())
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	return c, err
}

// SetBoot stores the reboot entry in the saved config. It does nothing
// when no config was saved.
func (h Home) SetBoot(mode string) error {
	c, err := h.LoadConfig()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	c.Boot = mode
	return h.SaveConfig(c)
}

func (h Home) SaveToken(tok string) error {
	return h.write(h.TokenPath(), []byte(tok+"\n"))
}

func (h Home) LoadToken() (string, error) {
	b, err := os.ReadFile(h.TokenPath())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// Forget removes the config, the token and the pid. It keeps the log,
// the state and the binary.
func (h Home) Forget() error {
	if h.Dir == "" {
		return nil
	}
	var errs []error
	for _, p := range []string{h.ConfigPath(), h.TokenPath(), h.PIDPath()} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h Home) WritePID(pid int) error {
	return h.write(h.PIDPath(), []byte(strconv.Itoa(pid)+"\n"))
}

func (h Home) RemovePID() error {
	if err := os.Remove(h.PIDPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// write replaces path with data, in mode 0600, through a temp file.
func (h Home) write(path string, data []byte) error {
	if err := os.MkdirAll(h.Dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(h.Dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
