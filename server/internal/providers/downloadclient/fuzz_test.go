package downloadclient

import (
	"encoding/json"
	"math"
	"testing"
)

// FuzzNumber: the flexible number every download-client adapter reads sizes,
// speeds and progress with ("51200.00" as a string, 51200 as a number, null,
// anything).
//
// Invariants: decoding never fails (a wrong type degrades the one value) and
// never panics; a valid value is finite; Whole and Times are never negative and
// never above MaxSafe whatever the value, factor, sign or magnitude (no wrap
// into a small or negative number); Clamp01 stays in 0..1; an invalid value is 0.
func FuzzNumber(f *testing.F) {
	f.Fuzz(func(t *testing.T, raw []byte, k float64) {
		var n Number
		if err := n.UnmarshalJSON(raw); err != nil {
			t.Fatalf("UnmarshalJSON(%q) = %v", raw, err)
		}
		if n.Valid() && (math.IsNaN(n.Float()) || math.IsInf(n.Float(), 0)) {
			t.Fatalf("%q is valid but %v", raw, n.Float())
		}
		if !n.Valid() && (n.Float() != 0 || n.Whole() != 0) {
			t.Fatalf("%q is invalid but reads %v", raw, n.Float())
		}
		if w := n.Whole(); w < 0 || w > MaxSafe {
			t.Fatalf("Whole() = %d for %q", w, raw)
		}
		if w := n.Times(k); w < 0 || w > MaxSafe {
			t.Fatalf("Times(%v) = %d for %q", k, w, raw)
		}
		if c := Clamp01(n.Float() * k); math.IsNaN(c) == false && (c < 0 || c > 1) {
			t.Fatalf("Clamp01 = %v", c)
		}
		// Decoding inside a struct behaves the same as decoding alone.
		var s struct {
			V Number `json:"v"`
		}
		if json.Valid(raw) {
			_ = json.Unmarshal([]byte(`{"v":`+string(raw)+`}`), &s)
			if s.V != n && !(s.V.valid == n.valid && (s.V.v == n.v || (math.IsNaN(s.V.v) && math.IsNaN(n.v)))) {
				t.Fatalf("in a struct %q reads %+v, alone %+v", raw, s.V, n)
			}
		}
	})
}
