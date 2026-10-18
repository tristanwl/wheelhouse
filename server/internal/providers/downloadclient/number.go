package downloadclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
)

func errorAsProvider(err error) (*core.ProviderError, bool) {
	var pe *core.ProviderError
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}

// Number decodes a JSON number or a numeric string ("51200.00": SABnzbd sends
// most numbers as strings). Anything else, null included, decodes without error
// but is not Valid, so a service that changes a field's type degrades one value
// instead of failing the whole response.
type Number struct {
	v     float64
	valid bool
}

// UnmarshalJSON implements json.Unmarshaler.
func (n *Number) UnmarshalJSON(data []byte) error {
	*n = Number{}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] == 'n' { // null
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return nil //nolint:nilerr // degrade, do not fail the response
		}
		s = strings.TrimSpace(s)
		if s == "" {
			// JavaScript's Number("") is 0, and the adapters treat that as "zero".
			n.v, n.valid = 0, true
			return nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
			n.v, n.valid = f, true
		}
		return nil
	}
	var f float64
	if err := json.Unmarshal(data, &f); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
		n.v, n.valid = f, true
	}
	return nil
}

// Valid reports whether the value was present and finite.
func (n Number) Valid() bool { return n.valid }

// Float is the value, or 0 when it was not Valid (the TypeScript toNumber).
func (n Number) Float() float64 { return n.v }

// Whole is the value rounded to a whole number, clamped to 0..MaxSafe: the
// bytes and speeds the adapters report are never negative.
func (n Number) Whole() int64 {
	r := RoundHalfUp(n.v)
	if r <= 0 || math.IsNaN(r) {
		return 0
	}
	if r > MaxSafe {
		return MaxSafe
	}
	return int64(r)
}

// Times is the value multiplied by k, rounded and clamped like Whole.
func (n Number) Times(k float64) int64 { return Number{v: n.v * k, valid: n.valid}.Whole() }

// Clamp01 clamps f to 0..1.
func Clamp01(f float64) float64 { return math.Min(math.Max(f, 0), 1) }
