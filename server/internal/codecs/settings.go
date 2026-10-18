package codecs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// DefaultConfigDir is where settings.json lives: a bind mount to a host
// directory (see deploy/ and docker-compose.example.yml) so settings survive
// container rebuilds.
const DefaultConfigDir = "/config"

// Store reads and writes the codec preferences in <dir>/settings.json.
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore returns a store over dir.
func NewStore(dir string) *Store { return &Store{dir: dir} }

func (s *Store) path() string { return filepath.Join(s.dir, "settings.json") }

type settingsFile struct {
	CodecPreferences *struct {
		HD  Preferable `json:"hd"`
		UHD Preferable `json:"uhd"`
	} `json:"codecPreferences,omitempty"`
}

// Get returns the saved preferences, with the default for anything missing or
// invalid, and the defaults when the file is absent or unreadable.
func (s *Store) Get() Preferences {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get()
}

func (s *Store) get() Preferences {
	prefs := DefaultPreferences
	raw, err := os.ReadFile(s.path())
	if err != nil {
		return prefs
	}
	var f settingsFile
	if json.Unmarshal(raw, &f) != nil || f.CodecPreferences == nil {
		return prefs
	}
	if f.CodecPreferences.HD.Valid() {
		prefs.HD = f.CodecPreferences.HD
	}
	if f.CodecPreferences.UHD.Valid() {
		prefs.UHD = f.CodecPreferences.UHD
	}
	return prefs
}

// Set saves the preferences. The file is written to a temporary name and
// renamed over the old one, so a crash never leaves it half-written.
func (s *Store) Set(p Preferences) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.MkdirAll(s.dir, 0o750)
	data, err := json.MarshalIndent(map[string]Preferences{"codecPreferences": p}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".settings-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := firstErr(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	// settings.json holds no secret; make it readable like a normally created file.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { //nolint:gosec // see above
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), s.path()); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
