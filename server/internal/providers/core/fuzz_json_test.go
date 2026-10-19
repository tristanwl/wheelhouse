package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// shape is what a JSON document measures, computed with encoding/json's own
// tokenizer (so independently of CheckJSON's byte scan).
type shape struct {
	maxElems int // most elements or members of any one array or object
	maxDepth int // deepest nesting of arrays and objects
	maxStr   int // longest string (key or value), decoded bytes
	total    int // all array elements and object members in the body
	escapes  bool
}

func measureJSON(body []byte) (shape, bool) {
	if !json.Valid(body) {
		return shape{}, false
	}
	type frame struct {
		obj       bool
		n         int
		expectKey bool
	}
	var sh shape
	var stack []frame
	dec := json.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if d, ok := tok.(json.Delim); ok && (d == ']' || d == '}') {
			stack = stack[:len(stack)-1]
			continue
		}
		if n := len(stack); n > 0 && stack[n-1].obj && stack[n-1].expectKey {
			stack[n-1].expectKey = false
			if s, ok := tok.(string); ok {
				sh.maxStr = max(sh.maxStr, len(s))
			}
			continue
		}
		if n := len(stack); n > 0 {
			stack[n-1].n++
			sh.total++
			sh.maxElems = max(sh.maxElems, stack[n-1].n)
			stack[n-1].expectKey = stack[n-1].obj
		}
		switch v := tok.(type) {
		case string:
			sh.maxStr = max(sh.maxStr, len(v))
		case json.Delim:
			stack = append(stack, frame{obj: v == '{', expectKey: v == '{'})
			sh.maxDepth = max(sh.maxDepth, len(stack))
		}
	}
	sh.escapes = bytes.IndexByte(body, '\\') >= 0
	return sh, true
}

// smallLimit maps a fuzzed byte to a limit from 1 to n, or to 0 (the default,
// which real fixtures fit in) for 200 and up.
func smallLimit(x uint8, n int) int {
	if x >= 200 {
		return 0
	}
	return int(x)%n + 1
}

// FuzzDecodeJSON drives the decoder every adapter uses on upstream bodies, with
// small limits derived from the input so the boundaries are reached.
//
// Invariants: a document the independent measurement finds over any limit is
// always refused (the scan cannot be fooled into under-counting); an accepted
// document decodes to exactly what encoding/json decodes; a refusal names no
// content of the body; and the memory the decode allocates stays within a
// constant factor of the input size.
func FuzzDecodeJSON(f *testing.F) {
	f.Fuzz(func(t *testing.T, body []byte, e, d, s, v uint8) {
		lim := JSONLimits{
			MaxElements:    smallLimit(e, 12),
			MaxDepth:       smallLimit(d, 8),
			MaxStringBytes: smallLimit(s, 24),
			MaxValues:      smallLimit(v, 48),
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		var got any
		err := DecodeJSON(context.Background(), body, &got, lim)
		runtime.ReadMemStats(&after)

		sh, valid := measureJSON(body)
		norm := lim.norm()
		overLimit := valid && (sh.maxDepth > norm.MaxDepth || sh.maxElems > norm.MaxElements || sh.total > norm.MaxValues ||
			(utf8.Valid(body) && sh.maxStr > norm.MaxStringBytes))

		if err == nil {
			if overLimit {
				t.Fatalf("accepted a document over the limits %+v: %+v", lim, sh)
			}
			var want any
			if jerr := json.Unmarshal(body, &want); jerr != nil {
				t.Fatalf("accepted what encoding/json refuses: %v", jerr)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("decoded value differs from encoding/json: %#v vs %#v", got, want)
			}
			if alloc := int(after.TotalAlloc - before.TotalAlloc); alloc > 512*len(body)+(1<<20) {
				t.Fatalf("a %d byte body took %d bytes to decode", len(body), alloc)
			}
			return
		}

		limitErr := errors.Is(err, errJSONLimit)
		if limitErr != (InvalidJSONText(err) != "returned invalid JSON") {
			t.Fatalf("InvalidJSONText disagrees with the error class for %v", err)
		}
		if valid && !limitErr {
			t.Fatalf("valid JSON refused as invalid: %v", err)
		}
		if valid && limitErr && !overLimit && !sh.escapes && utf8.Valid(body) {
			// Strings without escapes measure the same raw and decoded, so a
			// refusal here means the scan is stricter than the documented limits.
			t.Fatalf("refused a document within the limits %+v: %+v (%v)", lim, sh, err)
		}
		// The error never quotes the body (sliding windows: a decoder error that
		// echoed part of the input would contain one).
		msg := err.Error()
		if len(msg) > 200 {
			t.Fatalf("error text of %d bytes", len(msg))
		}
		const window = 16
		for i := 0; i+window <= len(body); i++ {
			if strings.Contains(msg, string(body[i:i+window])) {
				t.Fatalf("error %q quotes the body", msg)
			}
		}
	})
}
