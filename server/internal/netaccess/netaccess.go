// Package netaccess is the network side of access control: the stored
// local-network bypass (CIDR ranges whose peers are treated as signed-in
// users), the trusted-proxy list, and the one place that decides which client
// address a request came from.
//
// Two rules hold everything together. The TCP peer address is the only address
// that is believed unless that peer is a trusted proxy. And a forwarded address
// that cannot be understood is never guessed at: the client address is then
// unknown, and unknown matches no range.
package netaccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tristanwl/wheelhouse/server/internal/store"
)

// MaxEntries bounds each list.
const MaxEntries = 64

// Settings are the stored network settings. Prefixes are canonical: masked, and
// IPv4 as IPv4 (never as an IPv4-mapped IPv6 range).
type Settings struct {
	// Enabled turns the bypass on. It is off by default.
	Enabled bool
	// Bypass are the ranges whose clients are treated as signed-in users.
	Bypass []netip.Prefix
	// Proxies are the trusted proxies stored in the database; the environment
	// list (WHEELHOUSE_TRUSTED_PROXIES) is a second, read-only source.
	Proxies []netip.Prefix
	// AllowedHosts are the stored host names (canonical, see ParseHostEntry)
	// whose requests may get the bypass, besides IP literals, localhost and
	// single-label names.
	AllowedHosts []string
}

// Input is a settings update as an administrator sends it: text, parsed and
// checked by Update.
type Input struct {
	Enabled        bool
	CIDRs          []string
	TrustedProxies []string
	// AllowedHosts replaces the stored host list; nil keeps the current one.
	AllowedHosts *[]string
}

// ValidationError is a settings update that was refused; its text is safe to show.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// Service holds the settings and resolves client addresses. Reads never touch
// the database: the settings are cached and replaced on every update, which is
// the only writer.
type Service struct {
	db  *store.DB // nil: settings live in memory only (tests)
	env []netip.Prefix
	// envHosts is WHEELHOUSE_ALLOWED_HOSTS (read-only).
	envHosts []string
	cur      atomic.Pointer[Settings]
	now      func() time.Time
}

// New loads the stored settings. env is WHEELHOUSE_TRUSTED_PROXIES, already parsed.
func New(ctx context.Context, db *store.DB, env []netip.Prefix) (*Service, error) {
	s := &Service{db: db, env: env, now: time.Now}
	st := Settings{}
	if db != nil {
		var err error
		if st, err = load(ctx, db); err != nil {
			return nil, fmt.Errorf("network settings: %w", err)
		}
	}
	s.cur.Store(&st)
	return s, nil
}

// NewMemory is a Service that keeps its settings in memory, for tests of
// callers that have no database.
func NewMemory(env []netip.Prefix) *Service {
	s, _ := New(context.Background(), nil, env)
	return s
}

// Settings returns the current settings.
func (s *Service) Settings() Settings { return *s.cur.Load() }

// EnvProxies are the trusted proxies from the environment.
func (s *Service) EnvProxies() []netip.Prefix { return append([]netip.Prefix(nil), s.env...) }

func load(ctx context.Context, db *store.DB) (Settings, error) {
	var enabled int
	var cidrs, proxies, hosts string
	if err := db.QueryRow(ctx, `SELECT bypass_enabled, bypass_cidrs, trusted_proxies, allowed_hosts FROM network_settings WHERE id = 1`).Scan(&enabled, &cidrs, &proxies, &hosts); err != nil {
		return Settings{}, err
	}
	st := Settings{Enabled: enabled == 1}
	var err error
	if st.Bypass, err = decodeList(cidrs); err != nil {
		return Settings{}, fmt.Errorf("bypass_cidrs: %w", err)
	}
	if st.Proxies, err = decodeList(proxies); err != nil {
		return Settings{}, fmt.Errorf("trusted_proxies: %w", err)
	}
	if err := json.Unmarshal([]byte(hosts), &st.AllowedHosts); err != nil {
		return Settings{}, fmt.Errorf("allowed_hosts: %w", err)
	}
	return st, nil
}

func decodeList(raw string) ([]netip.Prefix, error) {
	var texts []string
	if err := json.Unmarshal([]byte(raw), &texts); err != nil {
		return nil, err
	}
	out := make([]netip.Prefix, 0, len(texts))
	for _, t := range texts {
		p, err := ParseRange(t)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Strings renders prefixes as the text the API and the database use.
func Strings(ps []netip.Prefix) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.String())
	}
	return out
}

// ParseRange parses a CIDR range or a single address ("192.168.1.0/24",
// "192.168.1.5", "fd00::/8") into its canonical form: masked, IPv4-mapped IPv6
// turned into IPv4, a single address as a /32 or /128. A zone id is refused.
func ParseRange(text string) (netip.Prefix, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return netip.Prefix{}, errors.New("empty entry")
	}
	var p netip.Prefix
	if strings.Contains(text, "/") {
		var err error
		if p, err = netip.ParsePrefix(text); err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is not a valid CIDR range", clipText(text))
		}
	} else {
		a, err := netip.ParseAddr(text)
		if err != nil || a.Zone() != "" {
			return netip.Prefix{}, fmt.Errorf("%q is not an address or CIDR range", clipText(text))
		}
		p = netip.PrefixFrom(a, a.BitLen())
	}
	if p.Addr().Is4In6() {
		if p.Bits() < 96 {
			return netip.Prefix{}, fmt.Errorf("%q: an IPv4-mapped IPv6 range must be /96 or longer; write the IPv4 range instead", clipText(text))
		}
		p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
	}
	return p.Masked(), nil
}

func clipText(s string) string {
	if len(s) > 64 {
		return s[:64] + "..."
	}
	return s
}

func parseList(what string, in []string) ([]netip.Prefix, error) {
	if len(in) > MaxEntries {
		return nil, invalid("%s: at most %d entries", what, MaxEntries)
	}
	out := make([]netip.Prefix, 0, len(in))
	seen := map[netip.Prefix]bool{}
	for _, t := range in {
		p, err := ParseRange(t)
		if err != nil {
			return nil, invalid("%s: %v", what, err)
		}
		if p.Bits() == 0 {
			return nil, invalid("%s: %s would match every address and is refused", what, p)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// Update validates and stores new settings, and returns them with the warnings
// to show. A refused update changes nothing.
func (s *Service) Update(ctx context.Context, in Input) (Settings, []string, error) {
	bypass, err := parseList("cidrs", in.CIDRs)
	if err != nil {
		return Settings{}, nil, err
	}
	proxies, err := parseList("trustedProxies", in.TrustedProxies)
	if err != nil {
		return Settings{}, nil, err
	}
	if in.Enabled && len(bypass) == 0 {
		return Settings{}, nil, invalid("cidrs: add at least one range before turning the bypass on")
	}
	hosts := s.cur.Load().AllowedHosts
	if in.AllowedHosts != nil {
		if hosts, err = ParseHostList(*in.AllowedHosts); err != nil {
			return Settings{}, nil, err
		}
	}
	st := Settings{Enabled: in.Enabled, Bypass: bypass, Proxies: proxies, AllowedHosts: hosts}
	if s.db != nil {
		cj, _ := json.Marshal(Strings(bypass))
		pj, _ := json.Marshal(Strings(proxies))
		hj, _ := json.Marshal(append([]string{}, hosts...))
		enabled := 0
		if st.Enabled {
			enabled = 1
		}
		err := s.db.Tx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE network_settings SET bypass_enabled = ?, bypass_cidrs = ?, trusted_proxies = ?, allowed_hosts = ?, updated_at = ? WHERE id = 1`,
				enabled, string(cj), string(pj), string(hj), s.now().Unix())
			return err
		})
		if err != nil {
			return Settings{}, nil, err
		}
	}
	s.cur.Store(&st)
	return st, Warnings(st), nil
}

// privateSpace is what the bypass is meant for: RFC 1918, carrier-grade NAT,
// loopback, link-local and unique-local ranges.
var privateSpace = mustPrefixes("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10",
	"127.0.0.0/8", "169.254.0.0/16", "::1/128", "fc00::/7", "fe80::/10")

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// IsPrivate reports whether p lies entirely inside private address space.
func IsPrivate(p netip.Prefix) bool {
	for _, b := range privateSpace {
		if b.Bits() <= p.Bits() && b.Contains(p.Addr()) {
			return true
		}
	}
	return false
}

// Warnings lists the ranges wider than private address space. They are allowed
// (an administrator may have a routable /48 at home) but shown loudly.
func Warnings(st Settings) []string {
	var out []string
	for _, p := range st.Bypass {
		if !IsPrivate(p) {
			out = append(out, fmt.Sprintf("The bypass range %s is wider than private address space: anyone who can reach this server from that range gets in without a login.", p))
		}
	}
	for _, p := range st.Proxies {
		if !IsPrivate(p) {
			out = append(out, fmt.Sprintf("The trusted proxy range %s is wider than private address space: any host in it can claim to be any client.", p))
		}
	}
	return out
}
