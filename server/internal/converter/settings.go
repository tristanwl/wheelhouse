package converter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/store"
)

// MaxMediaRoots bounds the number of media roots an administrator can save.
const MaxMediaRoots = 32

// Source says where the media roots in force come from.
type Source string

// The sources. The database wins when an administrator has saved roots;
// otherwise WHEELHOUSE_MEDIA_ROOTS (read-only), otherwise the built-in /movies
// and /tv.
const (
	SourceDatabase    Source = "database"
	SourceEnvironment Source = "environment"
	SourceDefault     Source = "default"
)

// State is the converter settings as the admin API shows them.
type State struct {
	// MediaRoots are the roots as configured: the saved list when Source is
	// "database", otherwise the same as EffectiveRoots (what saving them as they
	// are would store).
	MediaRoots []string
	// IncludeHDR lets HDR and Dolby Vision files be conversion candidates.
	IncludeHDR bool
	// EffectiveRoots are the roots conversion is limited to right now.
	EffectiveRoots []string
	Source         Source
}

// ValidationError is a settings update that was refused; its text is safe to
// show.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// Settings holds the converter settings: the media roots (database, then
// environment, then default) and the HDR switch. Reads never touch the
// database: the state is cached and replaced on every update, which is the only
// writer, so a change applies to the next request and the next job without a
// restart.
type Settings struct {
	db        *store.DB // nil: in memory only (tests)
	env       []string  // WHEELHOUSE_MEDIA_ROOTS, nil when unset
	protected []string
	cur       atomic.Pointer[State]
	now       func() time.Time
}

// NewSettings loads the stored settings. env is the roots from the environment
// (nil when WHEELHOUSE_MEDIA_ROOTS is not set); protected are extra folders no
// media root may be, contain or lie inside (the configuration directory).
func NewSettings(ctx context.Context, db *store.DB, env, protected []string) (*Settings, error) {
	s := &Settings{db: db, env: env, protected: protected, now: time.Now}
	var roots []string
	hdr := false
	if db != nil {
		var raw sql.NullString
		var inc int
		if err := db.QueryRow(ctx, `SELECT media_roots, include_hdr FROM converter_settings WHERE id = 1`).Scan(&raw, &inc); err != nil {
			return nil, fmt.Errorf("converter settings: %w", err)
		}
		hdr = inc == 1
		if raw.Valid {
			if err := json.Unmarshal([]byte(raw.String), &roots); err != nil {
				return nil, fmt.Errorf("converter settings: media_roots: %w", err)
			}
		}
	}
	s.cur.Store(s.state(roots, hdr))
	return s, nil
}

// NewMemorySettings keeps the settings in memory, for tests of callers with no
// database. env are the roots "from the environment".
func NewMemorySettings(env []string) *Settings {
	s, _ := NewSettings(context.Background(), nil, env, nil)
	return s
}

func (s *Settings) state(stored []string, includeHDR bool) *State {
	st := &State{IncludeHDR: includeHDR}
	switch {
	case len(stored) > 0:
		st.Source, st.EffectiveRoots = SourceDatabase, append([]string(nil), stored...)
	case len(s.env) > 0:
		st.Source, st.EffectiveRoots = SourceEnvironment, append([]string(nil), s.env...)
	default:
		st.Source, st.EffectiveRoots = SourceDefault, append([]string(nil), DefaultMediaRoots...)
	}
	st.MediaRoots = append([]string(nil), st.EffectiveRoots...)
	return st
}

// State returns the current settings.
func (s *Settings) State() State {
	st := *s.cur.Load()
	st.MediaRoots = append([]string(nil), st.MediaRoots...)
	st.EffectiveRoots = append([]string(nil), st.EffectiveRoots...)
	return st
}

// Roots are the media roots in force: what every path check and every job uses.
func (s *Settings) Roots() []string { return s.State().EffectiveRoots }

// IncludeHDR reports whether HDR and Dolby Vision files may be converted.
func (s *Settings) IncludeHDR() bool { return s.cur.Load().IncludeHDR }

// Update validates and stores new settings. roots nil keeps the saved roots; an
// empty non-nil list removes them (the environment, then the default, apply
// again); includeHDR nil keeps the switch. A refused update changes nothing.
func (s *Settings) Update(ctx context.Context, roots []string, includeHDR *bool) (State, error) {
	cur := s.cur.Load()
	var stored []string
	if cur.Source == SourceDatabase {
		stored = cur.EffectiveRoots
	}
	if roots != nil {
		if len(roots) == 0 {
			stored = nil
		} else {
			v, err := ValidateRoots(roots, s.protected)
			if err != nil {
				return State{}, err
			}
			stored = v
		}
	}
	hdr := cur.IncludeHDR
	if includeHDR != nil {
		hdr = *includeHDR
	}
	if s.db != nil {
		var rootsArg any
		if len(stored) > 0 {
			b, _ := json.Marshal(stored)
			rootsArg = string(b)
		}
		inc := 0
		if hdr {
			inc = 1
		}
		err := s.db.Tx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE converter_settings SET media_roots = ?, include_hdr = ?, updated_at = ? WHERE id = 1`, rootsArg, inc, s.now().Unix())
			return err
		})
		if err != nil {
			return State{}, err
		}
	}
	s.cur.Store(s.state(stored, hdr))
	return s.State(), nil
}

// dangerousRoots are system folders no media root may be, lie inside or
// contain: converting there would rewrite the host, not a library.
var dangerousRoots = []string{
	"/", "/etc", "/proc", "/sys", "/dev", "/root", "/boot", "/bin", "/sbin", "/usr",
	"/lib", "/lib32", "/lib64", "/libx32", "/run", "/var/lib",
}

// overlaps reports whether a and b are the same folder or one is inside the
// other (both cleaned).
func overlaps(a, b string) bool {
	return a == b || within(a, b) || within(b, a)
}

// ValidateRoots checks media roots an administrator typed and returns them
// cleaned. Each must be absolute with no ".." element, must exist and be a
// directory now (symlinks are followed, like the containment checks that use
// the roots later), must not be, contain or lie inside a system folder or a
// protected one, and must not be the same folder as, or nested inside or
// around, another root. Duplicates of the same text are dropped.
func ValidateRoots(in, protected []string) ([]string, error) {
	if len(in) == 0 {
		return nil, invalid("mediaRoots: add at least one folder")
	}
	if len(in) > MaxMediaRoots {
		return nil, invalid("mediaRoots: at most %d folders", MaxMediaRoots)
	}
	type root struct{ clean, real string }
	var out []root
	seen := map[string]bool{}
	for _, raw := range in {
		p := strings.TrimSpace(raw)
		if p == "" || strings.ContainsRune(p, 0) || len(p) > 4096 {
			return nil, invalid("mediaRoots: a folder path is empty or not valid")
		}
		if !filepath.IsAbs(p) {
			return nil, invalid("mediaRoots: %q is not an absolute path", clipPath(p))
		}
		for _, part := range strings.Split(filepath.ToSlash(p), "/") {
			if part == ".." {
				return nil, invalid("mediaRoots: %q contains \"..\"", clipPath(p))
			}
		}
		clean := filepath.Clean(p)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		real, err := filepath.EvalSymlinks(clean)
		if err != nil {
			return nil, invalid("mediaRoots: %q does not exist or cannot be read by Wheelhouse (is the folder mounted into the container?)", clipPath(clean))
		}
		info, err := os.Stat(real)
		switch {
		case err != nil:
			return nil, invalid("mediaRoots: %q cannot be read by Wheelhouse", clipPath(clean))
		case !info.IsDir():
			return nil, invalid("mediaRoots: %q is not a folder", clipPath(clean))
		}
		for _, d := range append(append([]string(nil), dangerousRoots...), protected...) {
			d = filepath.Clean(d)
			// "/" is dangerous only as a root itself: everything is inside it.
			if d == "/" {
				if clean == "/" || real == "/" {
					return nil, invalid("mediaRoots: %q is a system folder and cannot be a media folder", clipPath(clean))
				}
				continue
			}
			if overlaps(clean, d) || overlaps(real, d) {
				return nil, invalid("mediaRoots: %q is, contains or lies inside a system or Wheelhouse folder (%s) and cannot be a media folder", clipPath(clean), d)
			}
		}
		out = append(out, root{clean, real})
	}
	for i, a := range out {
		for _, b := range out[i+1:] {
			if overlaps(a.clean, b.clean) || overlaps(a.real, b.real) {
				return nil, invalid("mediaRoots: %q and %q are the same folder or one is inside the other; list only the outer one", clipPath(a.clean), clipPath(b.clean))
			}
		}
	}
	res := make([]string, len(out))
	for i, r := range out {
		res[i] = r.clean
	}
	return res, nil
}

func clipPath(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

// EnvRoots returns the media roots from WHEELHOUSE_MEDIA_ROOTS, or nil when it
// is unset or holds no absolute path. It is an operator-controlled seed and
// override of the default, not validated like what an administrator types.
func EnvRoots(getenv func(string) string) []string {
	var roots []string
	for _, r := range filepath.SplitList(getenv(RootsEnv)) {
		if filepath.IsAbs(r) {
			roots = append(roots, filepath.Clean(r))
		}
	}
	return roots
}
