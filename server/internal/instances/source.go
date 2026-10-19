// Package instances is the stored configuration: provider instances and feature
// toggles kept in the SQLite database, with the secret settings (API keys,
// passwords, tokens) encrypted by internal/secrets.
//
// Source implements core.ConfigSource over the database and, read-only, the
// environment. Precedence: an instance in the database hides an environment
// instance with the same id; an environment instance with no database row stays
// as it is, shown as "from environment" and not editable. "Import" copies an
// environment instance into the database, after which the environment values for
// that id are ignored.
//
// The runtime asks the source for the instances on every call and caches clients
// by a hash of the instance's validated config (core.Runtime), so a change made
// here is seen by the next request: no restart, no explicit invalidation, and a
// client already serving a request keeps working until it finishes.
package instances

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
	"github.com/tristanwl/wheelhouse/server/internal/secrets"
	"github.com/tristanwl/wheelhouse/server/internal/store"
)

// Origin says where an instance's configuration comes from.
type Origin string

// The two origins.
const (
	OriginDatabase    Origin = "database"
	OriginEnvironment Origin = "environment"
)

// Errors the callers map to HTTP statuses. None carries a setting's value.
var (
	// ErrNotFound means no instance has that id.
	ErrNotFound = errors.New("no such instance")
	// ErrExists means the id is taken.
	ErrExists = errors.New("an instance with that id already exists")
	// ErrReadOnly means the instance comes from the environment and cannot be
	// changed here; import it first.
	ErrReadOnly = errors.New("this instance comes from the environment and is read-only; import it into the database to edit it")
	// ErrEnvironmentID means a new instance would take the id of an environment
	// instance, which would silently replace it; import it instead.
	ErrEnvironmentID = errors.New("that id is defined by the environment; import that instance to change it, or choose another id")
	// ErrKeyChanged means the database's secret key was changed by another
	// process while this one ran; nothing is saved until Wheelhouse restarts.
	ErrKeyChanged = store.ErrKeyChanged
)

// InvalidError is a request the administrator can fix; Msg says how and
// contains no setting values.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &InvalidError{Msg: fmt.Sprintf(format, args...)}
}

// Entry is an instance with where it comes from.
type Entry struct {
	core.Instance
	Origin Origin
	// OverridesEnvironment is set on a database instance that hides an
	// environment instance with the same id.
	OverridesEnvironment bool
}

// NewInstance is a request to create an instance.
type NewInstance struct {
	ProviderID string
	// ID is optional; without one it is derived from the provider id and name.
	ID      string
	Name    string
	Enabled *bool
	Config  map[string]any
}

// Patch changes an instance. Nil fields stay as they are. Config, when given,
// replaces the settings, except that a secret setting left out keeps its stored
// value (an explicit new value replaces it).
type Patch struct {
	Name    *string
	Enabled *bool
	Config  map[string]any
}

// Change says what an update changed, by setting name, never by value. It is
// what the audit log records.
type Change struct {
	Name    bool
	Enabled bool
	Fields  []string
	Secrets []string
}

// Empty reports whether nothing changed.
func (c Change) Empty() bool {
	return !c.Name && !c.Enabled && len(c.Fields) == 0 && len(c.Secrets) == 0
}

// Options configures a Source.
type Options struct {
	// Environment is the read-only source (core.NewProcessEnvConfigSource()).
	Environment core.ConfigSource
	Logger      *slog.Logger
	// Now is the clock for created/updated times (tests).
	Now func() time.Time
}

// Source is the stored configuration. It is safe for concurrent use.
type Source struct {
	db     *store.DB
	cipher *secrets.Cipher
	reg    *core.Registry
	env    core.ConfigSource
	log    *slog.Logger
	now    func() time.Time

	warned sync.Map // instance id -> struct{}: unreadable secrets are logged once
}

// New returns a Source over db. cipher seals and opens the secret settings.
func New(db *store.DB, cipher *secrets.Cipher, reg *core.Registry, o Options) *Source {
	s := &Source{db: db, cipher: cipher, reg: reg, env: o.Environment, log: o.Logger, now: o.Now}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// Registry is the provider registry the source validates against.
func (s *Source) Registry() *core.Registry { return s.reg }

// Merge validates provided against the provider's schema, filling a secret left
// out of it from stored, and returns the complete settings (secrets included:
// for building a client, never for a response). It is how an edit form's
// unsaved values are tested with the key that is already stored.
func Merge(def *core.Definition, provided, stored map[string]any) (map[string]any, error) {
	plain, sec, err := split(def, provided, stored)
	if err != nil {
		return nil, err
	}
	maps.Copy(plain, sec)
	return plain, nil
}

// Instances implements core.ConfigSource: the merged view the runtime uses.
func (s *Source) Instances(ctx context.Context) ([]core.Instance, error) {
	entries, err := s.Entries(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]core.Instance, len(entries))
	for i, e := range entries {
		out[i] = e.Instance
	}
	return out, nil
}

// Entries lists every instance with its origin: the database ones in the order
// they were added, then the environment ones the database does not override.
func (s *Source) Entries(ctx context.Context) ([]Entry, error) {
	dbRows, err := s.dbInstances(ctx)
	if err != nil {
		return nil, err
	}
	envInstances, err := s.envInstances(ctx)
	if err != nil {
		return nil, err
	}
	inDB := map[string]bool{}
	inEnv := map[string]bool{}
	for _, e := range envInstances {
		inEnv[e.ID] = true
	}
	out := make([]Entry, 0, len(dbRows)+len(envInstances))
	for _, r := range dbRows {
		inDB[r.ID] = true
		out = append(out, Entry{Instance: r, Origin: OriginDatabase, OverridesEnvironment: inEnv[r.ID]})
	}
	for _, e := range envInstances {
		if !inDB[e.ID] {
			out = append(out, Entry{Instance: e, Origin: OriginEnvironment})
		}
	}
	return out, nil
}

// Get returns one instance, from either origin.
func (s *Source) Get(ctx context.Context, id string) (Entry, error) {
	entries, err := s.Entries(ctx)
	if err != nil {
		return Entry{}, err
	}
	for _, e := range entries {
		if e.ID == id {
			return e, nil
		}
	}
	return Entry{}, ErrNotFound
}

func (s *Source) envInstances(ctx context.Context) ([]core.Instance, error) {
	if s.env == nil {
		return nil, nil
	}
	return s.env.Instances(ctx)
}

func (s *Source) envHas(ctx context.Context, id string) (core.Instance, bool, error) {
	list, err := s.envInstances(ctx)
	if err != nil {
		return core.Instance{}, false, err
	}
	for _, e := range list {
		if e.ID == id {
			return e, true, nil
		}
	}
	return core.Instance{}, false, nil
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type queryerFunc func(ctx context.Context, query string, args ...any) (*sql.Rows, error)

func (f queryerFunc) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return f(ctx, query, args...)
}

func (s *Source) dbInstances(ctx context.Context) ([]core.Instance, error) {
	return s.readInstances(ctx, queryerFunc(func(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
		return s.db.Query(ctx, q, a...)
	}), "")
}

// readInstances reads all rows (id "") or one row.
func (s *Source) readInstances(ctx context.Context, q queryer, id string) ([]core.Instance, error) {
	query := `SELECT id, provider_id, name, enabled, config, secrets FROM instances`
	var args []any
	if id != "" {
		query += ` WHERE id = ?`
		args = append(args, id)
	}
	query += ` ORDER BY position, id`
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []core.Instance
	for rows.Next() {
		var (
			inst    core.Instance
			enabled int
			cfg     string
			sealed  []byte
		)
		if err := rows.Scan(&inst.ID, &inst.ProviderID, &inst.Name, &enabled, &cfg, &sealed); err != nil {
			return nil, err
		}
		inst.Enabled = enabled != 0
		inst.Config = map[string]any{}
		if err := json.Unmarshal([]byte(cfg), &inst.Config); err != nil {
			return nil, errors.New("a stored instance has damaged settings")
		}
		if sealed != nil {
			s.mergeSecrets(&inst, sealed)
		}
		out = append(out, inst)
	}
	return out, rows.Err()
}

func secretsAD(id string) []byte { return secrets.AD("instances", id, "secrets") }

// mergeSecrets opens the sealed secret settings into inst.Config. One that
// cannot be opened leaves the instance without its credentials and says why in
// inst.Problem, so the dashboard and the admin page show a plain sentence
// instead of "is required"; nothing else is affected. It is logged once per
// instance. The usual cause is a row that was tampered with or copied from
// elsewhere; the one that matters most is another process having changed the
// database's key under this one (ErrKeyChanged), which gets its own message and
// log line because the fix is a restart, not typing the credentials again.
func (s *Source) mergeSecrets(inst *core.Instance, sealed []byte) {
	pt, err := s.cipher.Open(sealed, secretsAD(inst.ID))
	if err == nil {
		defer clear(pt)
		var m map[string]any
		if json.Unmarshal(pt, &m) == nil {
			maps.Copy(inst.Config, m)
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	changed := errors.Is(s.db.CheckKey(ctx, nil, s.cipher), store.ErrKeyChanged)
	cancel()
	if changed {
		inst.Problem = "The stored credentials cannot be read because the secret key was changed while Wheelhouse was running. Restart Wheelhouse."
	} else {
		inst.Problem = "The stored credentials cannot be read. Enter them again."
	}
	if _, seen := s.warned.LoadOrStore(inst.ID, struct{}{}); !seen {
		if changed {
			s.log.Error("the secret key in the database changed while Wheelhouse was running (rekey or forget-secrets run against a live server?); stored credentials cannot be read and none will be saved until Wheelhouse is restarted", "instance", inst.ID)
		} else {
			s.log.Warn("the stored credentials of an instance cannot be read; enter them again", "instance", inst.ID)
		}
	}
}

// keyCheck refuses a write that seals or merges secrets when the database's key
// is no longer the one this process holds.
func (s *Source) keyCheck(ctx context.Context, tx *sql.Tx) error {
	return s.db.CheckKey(ctx, tx, s.cipher)
}

// ---- writes ----

var idPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// MaxNameLength bounds an instance's display name.
const MaxNameLength = 100

func validID(providerID, id string) error {
	if len(id) > 64 || !idPattern.MatchString(id) {
		return invalid("the id must be lowercase letters, digits and single hyphens, at most 64 characters")
	}
	if id != providerID && !strings.HasPrefix(id, providerID+"-") {
		return invalid("the id must be %q or start with %q (for example %q)", providerID, providerID+"-", providerID+"-4k")
	}
	return nil
}

func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength {
		return "", invalid("the name must be 1 to %d characters", MaxNameLength)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", invalid("the name must not contain control characters")
		}
	}
	return name, nil
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugStrip.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// split validates a provider config built from provided (and, for a secret left
// out of it, from stored) and separates the non-secret settings from the secret
// ones. Errors are *core.ValidationError or *InvalidError and carry no values.
func split(def *core.Definition, provided, stored map[string]any) (plain, sec map[string]any, err error) {
	known := map[string]bool{}
	for _, f := range def.Fields() {
		known[f.Name] = true
	}
	merged := map[string]any{}
	for k, v := range provided {
		if !known[k] {
			return nil, nil, invalid("that setting does not exist for this type of service")
		}
		if v != nil {
			merged[k] = v
		}
	}
	// A secret left out is filled from the stored one only while requests still
	// go where they went when it was stored. Otherwise an administrator (or a
	// stolen session) could point the instance at a server they control, run the
	// test, and read the stored key from that server's logs.
	moved := destinationChanged(def, provided, stored)
	for _, k := range def.SecretKeys() {
		if _, ok := merged[k]; !ok {
			if v, ok := stored[k]; ok {
				if moved {
					return nil, nil, &core.ValidationError{Errors: []core.FieldError{{
						Field: k, Label: def.Label(k), Message: "must be entered again when the URL changes (the stored value is not sent to a new address)",
					}}}
				}
				merged[k] = v
			}
		}
	}
	if _, err := def.Decode(merged); err != nil {
		return nil, nil, err
	}
	isSecret := map[string]bool{}
	for _, k := range def.SecretKeys() {
		isSecret[k] = true
	}
	plain, sec = map[string]any{}, map[string]any{}
	for k, v := range merged {
		if isSecret[k] {
			sec[k] = v
		} else {
			plain[k] = v
		}
	}
	return plain, sec, nil
}

// destinationChanged reports whether provided points a URL setting somewhere
// other than stored does. A URL left out of provided does not count as a change
// (it is then either refused as missing or falls back to the provider's own
// default address). Scheme and host are compared in lower case, a default port
// equals none, and a trailing slash is ignored; anything that does not parse
// counts as changed.
func destinationChanged(def *core.Definition, provided, stored map[string]any) bool {
	for _, f := range def.Fields() {
		if f.Format != "url" {
			continue
		}
		pv, ok := provided[f.Name]
		if !ok || pv == nil {
			continue
		}
		ps, _ := pv.(string)
		ss, _ := stored[f.Name].(string)
		if normalizeDestination(ps) != normalizeDestination(ss) || normalizeDestination(ps) == "" {
			return true
		}
	}
	return false
}

func normalizeDestination(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host + strings.TrimRight(u.EscapedPath(), "/")
}

// splitRaw separates an already-stored raw config (an environment instance being
// imported) without validating it: a half-configured service is imported as it
// is and shows as invalid.
func splitRaw(def *core.Definition, raw map[string]any) (plain, sec map[string]any) {
	isSecret := map[string]bool{}
	known := map[string]bool{}
	isURL := map[string]bool{}
	for _, f := range def.Fields() {
		known[f.Name] = true
		isSecret[f.Name] = f.Secret
		isURL[f.Name] = f.Format == "url"
	}
	plain, sec = map[string]any{}, map[string]any{}
	for k, v := range raw {
		switch {
		case !known[k] || v == nil:
		case isSecret[k]:
			sec[k] = v
		case isURL[k]:
			// A URL's user information is a credential in a plaintext column
			// and is refused on input; an environment value that carries one is
			// imported without it (the instance then needs its real credentials).
			plain[k] = withoutUserInfo(v)
		default:
			plain[k] = v
		}
	}
	return plain, sec
}

func withoutUserInfo(v any) any {
	str, ok := v.(string)
	if !ok {
		return v
	}
	u, err := url.Parse(str)
	if err != nil || u.User == nil {
		return v
	}
	u.User = nil
	return u.String()
}

func (s *Source) seal(id string, sec map[string]any) (any, error) {
	if len(sec) == 0 {
		return nil, nil
	}
	pt, err := json.Marshal(sec)
	if err != nil {
		return nil, err
	}
	defer clear(pt)
	return s.cipher.Seal(pt, secretsAD(id))
}

// Create adds an instance. The config is validated against the provider's
// schema.
func (s *Source) Create(ctx context.Context, in NewInstance) (Entry, error) {
	def, ok := s.reg.Get(in.ProviderID)
	if !ok {
		return Entry{}, invalid("unknown provider")
	}
	name, err := validName(in.Name)
	if err != nil {
		return Entry{}, err
	}
	plain, sec, err := split(def, in.Config, nil)
	if err != nil {
		return Entry{}, err
	}
	envList, err := s.envInstances(ctx)
	if err != nil {
		return Entry{}, err
	}
	inEnv := func(id string) bool {
		return slices.ContainsFunc(envList, func(e core.Instance) bool { return e.ID == id })
	}
	enabled := in.Enabled == nil || *in.Enabled
	var id string
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.keyCheck(ctx, tx); err != nil {
			return err
		}
		taken := func(id string) (bool, error) {
			var n int
			err := tx.QueryRowContext(ctx, `SELECT count(*) FROM instances WHERE id = ?`, id).Scan(&n)
			return n > 0, err
		}
		id = in.ID
		if id == "" {
			for _, cand := range []string{in.ProviderID, in.ProviderID + "-" + slug(name)} {
				if cand == in.ProviderID+"-" {
					continue
				}
				used, err := taken(cand)
				if err != nil {
					return err
				}
				if !used && !inEnv(cand) {
					id = cand
					break
				}
			}
			if id == "" {
				return ErrExists
			}
		}
		if err := validID(in.ProviderID, id); err != nil {
			return err
		}
		if inEnv(id) {
			return ErrEnvironmentID
		}
		if used, err := taken(id); err != nil {
			return err
		} else if used {
			return ErrExists
		}
		sealed, err := s.seal(id, sec)
		if err != nil {
			return err
		}
		cfg, err := json.Marshal(plain)
		if err != nil {
			return err
		}
		now := s.now().Unix()
		_, err = tx.ExecContext(ctx, `INSERT INTO instances (id, provider_id, name, enabled, config, secrets, position, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM instances), ?, ?)`,
			id, in.ProviderID, name, boolInt(enabled), string(cfg), sealed, now, now)
		return err
	})
	if err != nil {
		return Entry{}, err
	}
	return s.entryOf(ctx, id)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Source) entryOf(ctx context.Context, id string) (Entry, error) {
	rows, err := s.readInstances(ctx, queryerFunc(func(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
		return s.db.Query(ctx, q, a...)
	}), id)
	if err != nil {
		return Entry{}, err
	}
	if len(rows) == 0 {
		return Entry{}, ErrNotFound
	}
	_, over, err := s.envHas(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Instance: rows[0], Origin: OriginDatabase, OverridesEnvironment: over}, nil
}

// Update changes a stored instance. It reads and writes in one transaction, so
// two concurrent updates cannot lose each other's secret.
func (s *Source) Update(ctx context.Context, id string, p Patch) (Entry, Change, error) {
	var change Change
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		change = Change{}
		if err := s.keyCheck(ctx, tx); err != nil {
			return err
		}
		rows, err := s.readInstances(ctx, tx, id)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			if _, isEnv, err := s.envHas(ctx, id); err != nil {
				return err
			} else if isEnv {
				return ErrReadOnly
			}
			return ErrNotFound
		}
		cur := rows[0]
		name, enabled := cur.Name, cur.Enabled
		if p.Name != nil {
			n, err := validName(*p.Name)
			if err != nil {
				return err
			}
			change.Name = n != cur.Name
			name = n
		}
		if p.Enabled != nil {
			change.Enabled = *p.Enabled != cur.Enabled
			enabled = *p.Enabled
		}
		var plain, sec map[string]any
		def, known := s.reg.Get(cur.ProviderID)
		if p.Config != nil {
			if !known {
				return invalid("the provider of this instance is not available; it can only be deleted")
			}
			plain, sec, err = split(def, p.Config, cur.Config)
			if err != nil {
				return err
			}
			old, oldSec := splitRaw(def, cur.Config)
			for k := range plain {
				if !reflect.DeepEqual(plain[k], old[k]) {
					change.Fields = append(change.Fields, k)
				}
			}
			for k := range old {
				if _, ok := plain[k]; !ok {
					change.Fields = append(change.Fields, k)
				}
			}
			for k, v := range sec {
				if !reflect.DeepEqual(v, oldSec[k]) {
					change.Secrets = append(change.Secrets, k)
				}
			}
			slices.Sort(change.Fields)
			slices.Sort(change.Secrets)
		}
		if change.Empty() {
			return nil
		}
		set := []string{"name = ?", "enabled = ?", "updated_at = ?"}
		args := []any{name, boolInt(enabled), s.now().Unix()}
		if p.Config != nil {
			sealed, err := s.seal(id, sec)
			if err != nil {
				return err
			}
			cfg, err := json.Marshal(plain)
			if err != nil {
				return err
			}
			set = append(set, "config = ?", "secrets = ?")
			args = append(args, string(cfg), sealed)
		}
		args = append(args, id)
		_, err = tx.ExecContext(ctx, `UPDATE instances SET `+strings.Join(set, ", ")+` WHERE id = ?`, args...) //nolint:gosec // the SET clause is built from constants
		return err
	})
	if err != nil {
		return Entry{}, Change{}, err
	}
	e, err := s.entryOf(ctx, id)
	return e, change, err
}

// Delete removes a stored instance. It reports whether an environment instance
// with the same id is now in effect again.
func (s *Source) Delete(ctx context.Context, id string) (revertsToEnvironment bool, err error) {
	res, err := s.db.Exec(ctx, `DELETE FROM instances WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	_, isEnv, err := s.envHas(ctx, id)
	if err != nil {
		return false, err
	}
	if n == 0 {
		if isEnv {
			return false, ErrReadOnly
		}
		return false, ErrNotFound
	}
	return isEnv, nil
}

// Import copies an environment instance into the database. From then on the
// environment values for that id are ignored. The settings are copied as they
// are, even if incomplete, so a half-configured service can be finished here.
func (s *Source) Import(ctx context.Context, id string) (Entry, error) {
	env, ok, err := s.envHas(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	if !ok {
		if _, err := s.entryOf(ctx, id); err == nil {
			return Entry{}, ErrExists
		}
		return Entry{}, ErrNotFound
	}
	def, known := s.reg.Get(env.ProviderID)
	if !known {
		return Entry{}, invalid("unknown provider")
	}
	plain, sec := splitRaw(def, env.Config)
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.keyCheck(ctx, tx); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM instances WHERE id = ?`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrExists
		}
		sealed, err := s.seal(id, sec)
		if err != nil {
			return err
		}
		cfg, err := json.Marshal(plain)
		if err != nil {
			return err
		}
		now := s.now().Unix()
		_, err = tx.ExecContext(ctx, `INSERT INTO instances (id, provider_id, name, enabled, config, secrets, position, created_at, updated_at)
			VALUES (?, ?, ?, 1, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM instances), ?, ?)`,
			id, env.ProviderID, env.Name, string(cfg), sealed, now, now)
		return err
	})
	if err != nil {
		return Entry{}, err
	}
	return s.entryOf(ctx, id)
}

// ---- feature toggles ----

// DisabledFeatures returns the ids of the features an administrator switched
// off.
func (s *Source) DisabledFeatures(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.Query(ctx, `SELECT feature_id FROM feature_settings WHERE enabled = 0`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// SetFeatures stores the enabled flag of each given feature id in one
// transaction. The caller validates the ids.
func (s *Source) SetFeatures(ctx context.Context, enabled map[string]bool) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		now := s.now().Unix()
		for id, on := range enabled {
			if _, err := tx.ExecContext(ctx, `INSERT INTO feature_settings (feature_id, enabled, updated_at) VALUES (?, ?, ?)
				ON CONFLICT(feature_id) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at`,
				id, boolInt(on), now); err != nil {
				return err
			}
		}
		return nil
	})
}
