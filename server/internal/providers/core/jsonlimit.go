package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
)

// A byte cap does not bound what a JSON body decodes to: "[{},{},{}...]" is three
// bytes per element on the wire and a whole struct (or map) in memory. A hostile
// or broken upstream could therefore turn an 8 MiB answer into gigabytes. Every
// adapter decodes through DecodeJSON, which first scans the body (no
// allocation, no decoded values) and refuses one with more array elements or
// object members, deeper nesting or longer strings than the call allows, and
// which lets only a few large decodes run at once.

// JSONLimits bound what DecodeJSON accepts. A zero field takes its default.
type JSONLimits struct {
	// MaxElements is the most elements of any one array (and members of any one
	// object).
	MaxElements int
	// MaxDepth is the deepest nesting of arrays and objects.
	MaxDepth int
	// MaxStringBytes is the longest single string, in raw bytes.
	MaxStringBytes int
	// MaxValues is the most array elements and object members in the whole body.
	MaxValues int
}

// The defaults. Real libraries are far below them: 50,000 movies is about 5
// million values.
const (
	DefaultMaxElements    = 200_000
	DefaultMaxDepth       = 32
	DefaultMaxStringBytes = 1 << 20
	DefaultMaxValues      = 12_000_000

	// LargeListBytes is the response cap for an endpoint that legitimately
	// returns a whole library (Radarr and Sonarr movie and series lists,
	// qBittorrent's torrent list). SmallResponseBytes is for status, config,
	// health and queue summaries.
	LargeListBytes     int64 = 96 << 20
	MediumListBytes    int64 = 8 << 20
	SmallResponseBytes int64 = 2 << 20

	// gatedBodyBytes is the size from which a body counts as large and takes a
	// slot in the process-wide decode gate.
	gatedBodyBytes = 256 << 10
)

func (l JSONLimits) norm() JSONLimits {
	if l.MaxElements <= 0 {
		l.MaxElements = DefaultMaxElements
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = DefaultMaxDepth
	}
	if l.MaxStringBytes <= 0 {
		l.MaxStringBytes = DefaultMaxStringBytes
	}
	if l.MaxValues <= 0 {
		l.MaxValues = DefaultMaxValues
	}
	return l
}

// errJSONLimit is why a body was refused; its text names no content.
var errJSONLimit = errors.New("JSON exceeds the allowed size or shape")

// CheckJSON scans body and reports whether it stays within the limits. It does
// not validate the syntax (the decoder does that next); on malformed input it
// simply stops counting at whatever it sees.
func CheckJSON(body []byte, lim JSONLimits) error {
	lim = lim.norm()
	// stack holds the member count of each open array or object.
	stack := make([]int, 0, 16)
	total := 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '"':
			start := i + 1
			i++
			for i < len(body) && body[i] != '"' {
				if body[i] == '\\' {
					i++
				}
				i++
			}
			if i-start > lim.MaxStringBytes {
				return fmt.Errorf("%w: string of %d bytes", errJSONLimit, i-start)
			}
		case '[', '{':
			if len(stack) >= lim.MaxDepth {
				return fmt.Errorf("%w: nesting deeper than %d", errJSONLimit, lim.MaxDepth)
			}
			stack = append(stack, 0)
		case ']', '}':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case ',':
			if n := len(stack); n > 0 {
				stack[n-1]++
				total++
				if stack[n-1] >= lim.MaxElements {
					return fmt.Errorf("%w: more than %d elements", errJSONLimit, lim.MaxElements)
				}
				if total >= lim.MaxValues {
					return fmt.Errorf("%w: more than %d values", errJSONLimit, lim.MaxValues)
				}
			}
		}
	}
	return nil
}

var (
	decodeGate    atomic.Pointer[chan struct{}]
	defaultGateSz = max(2, min(runtime.GOMAXPROCS(0), 4))
)

func gate() chan struct{} {
	if g := decodeGate.Load(); g != nil {
		return *g
	}
	g := make(chan struct{}, defaultGateSz)
	if decodeGate.CompareAndSwap(nil, &g) {
		return g
	}
	return *decodeGate.Load()
}

// SetMaxConcurrentDecodes sets how many large bodies (256 KiB and up) may be
// decoded at the same time across the process (default 2 to 4, by CPU count).
// Further decodes wait their turn. Call it at start, before requests run.
func SetMaxConcurrentDecodes(n int) {
	if n < 1 {
		n = 1
	}
	g := make(chan struct{}, n)
	decodeGate.Store(&g)
}

// InvalidJSONText is the generic end of the error shown when a body did not
// decode: it tells a refused oversized or over-nested body from invalid JSON,
// and never quotes the body.
func InvalidJSONText(err error) string {
	if errors.Is(err, errJSONLimit) {
		return "returned a response that is too large or too deeply nested"
	}
	return "returned invalid JSON"
}

// DecodeJSON decodes body into out after checking it against lim (see
// JSONLimits). A body of 256 KiB or more waits for a slot in the process-wide
// decode gate, so many requests at once cannot multiply the memory of their
// decoded forms. The returned errors are generic: they never quote the body.
func DecodeJSON(ctx context.Context, body []byte, out any, lim JSONLimits) error {
	if err := CheckJSON(body, lim); err != nil {
		return err
	}
	if len(body) >= gatedBodyBytes {
		g := gate()
		select {
		case g <- struct{}{}:
			defer func() { <-g }()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return json.Unmarshal(body, out)
}
