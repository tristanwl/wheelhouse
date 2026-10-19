package core

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckJSONLimits(t *testing.T) {
	flat := func(n int) []byte { return []byte("[" + strings.Repeat("{},", n-1) + "{}]") }
	cases := []struct {
		name string
		body []byte
		lim  JSONLimits
		ok   bool
	}{
		{"empty array", []byte("[]"), JSONLimits{}, true},
		{"at the element limit", flat(100), JSONLimits{MaxElements: 100}, true},
		{"over the element limit", flat(101), JSONLimits{MaxElements: 100}, false},
		{"object members count", []byte(`{` + strings.Repeat(`"a":1,`, 10) + `"b":1}`), JSONLimits{MaxElements: 5}, false},
		{"commas inside strings do not count", []byte(`["` + strings.Repeat(",", 1000) + `"]`), JSONLimits{MaxElements: 5}, true},
		{"escaped quote does not end a string", []byte(`["a\",\",\",\",\",\",b"]`), JSONLimits{MaxElements: 3}, true},
		{"depth at the limit", []byte("[[[]]]"), JSONLimits{MaxDepth: 3}, true},
		{"depth over the limit", []byte("[[[[]]]]"), JSONLimits{MaxDepth: 3}, false},
		{"long string", []byte(`["` + strings.Repeat("a", 50) + `"]`), JSONLimits{MaxStringBytes: 49}, false},
		{"string at the limit", []byte(`["` + strings.Repeat("a", 50) + `"]`), JSONLimits{MaxStringBytes: 50}, true},
		{"too many values in total", []byte("[[1,2,3],[1,2,3],[1,2,3]]"), JSONLimits{MaxValues: 8}, false},
		{"unterminated input does not hang or panic", []byte(`["abc\`), JSONLimits{}, true},
		{"nested flood", bytes.Repeat([]byte("["), 500000), JSONLimits{}, false},
	}
	for _, c := range cases {
		err := CheckJSON(c.body, c.lim)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
		if err != nil && (!errors.Is(err, errJSONLimit) || InvalidJSONText(err) == "returned invalid JSON") {
			t.Errorf("%s: not reported as a limit: %v", c.name, err)
		}
	}
	if InvalidJSONText(errors.New("x")) != "returned invalid JSON" {
		t.Error("an ordinary decode error keeps its text")
	}
}

func TestDecodeJSONNeverQuotesTheBody(t *testing.T) {
	var out []struct{}
	err := DecodeJSON(context.Background(), []byte(`[{"SECRET-BODY":`+strings.Repeat("[", 100)), &out, JSONLimits{})
	if err == nil || strings.Contains(err.Error(), "SECRET-BODY") {
		t.Errorf("err = %v", err)
	}
}

func TestDecodeGateBoundsConcurrentLargeDecodes(t *testing.T) {
	SetMaxConcurrentDecodes(2)
	t.Cleanup(func() { SetMaxConcurrentDecodes(defaultGateSz) })
	var inFlight, peak atomic.Int32
	big := append(append([]byte("["), bytes.Repeat([]byte("1,"), gatedBodyBytes)...), []byte("1]")...)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			var out slowOut
			out.enter = func() {
				n := inFlight.Add(1)
				for {
					p := peak.Load()
					if n <= p || peak.CompareAndSwap(p, n) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				inFlight.Add(-1)
			}
			_ = DecodeJSON(context.Background(), big, &out, JSONLimits{MaxElements: 1 << 20})
		})
	}
	wg.Wait()
	if peak.Load() > 2 || peak.Load() < 1 {
		t.Errorf("peak concurrent decodes = %d, want at most 2", peak.Load())
	}
}

// slowOut is a decode target that notes when decoding reaches it.
type slowOut struct{ enter func() }

func (s *slowOut) UnmarshalJSON([]byte) error { s.enter(); return nil }

func TestDecodeGateWaitHonoursTheContext(t *testing.T) {
	SetMaxConcurrentDecodes(1)
	t.Cleanup(func() { SetMaxConcurrentDecodes(defaultGateSz) })
	g := gate()
	g <- struct{}{}
	defer func() { <-g }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	big := append(append([]byte("["), bytes.Repeat([]byte("1,"), gatedBodyBytes)...), []byte("1]")...)
	var out []int
	if err := DecodeJSON(ctx, big, &out, JSONLimits{MaxElements: 1 << 20}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
}

func TestPerCallMaxBytesMayRaiseTheDefaultButNotAnExplicitCeiling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte(" "), 300))
	}))
	defer srv.Close()
	ctx := context.Background()

	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "x"})
	c.maxBytes = 100 // stands in for a small default
	c.capSet = false
	if _, err := c.Do(ctx, "/", Request{}); err == nil {
		t.Error("the default cap was not applied")
	}
	if _, err := c.Do(ctx, "/", Request{MaxBytes: 1000}); err != nil {
		t.Errorf("an endpoint cap did not raise the default: %v", err)
	}

	c = newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "x", MaxResponseBytes: 100})
	if _, err := c.Do(ctx, "/", Request{MaxBytes: 1000}); err == nil {
		t.Error("a per-call cap raised an explicit client ceiling")
	}
}

func TestJSONHelperRefusesAFloodWithAGenericMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[" + strings.Repeat("{},", 1000) + "{}]"))
	}))
	defer srv.Close()
	c := newClient(t, HTTPOptions{BaseURL: srv.URL, Label: "x"})
	var out []struct{}
	err := c.JSON(context.Background(), "/list", Request{Limits: JSONLimits{MaxElements: 100}}, &out)
	if err == nil || err.Error() != "x /list returned a response that is too large or too deeply nested" {
		t.Errorf("err = %v", err)
	}
	if err := c.JSON(context.Background(), "/list", Request{}, &out); err != nil || len(out) != 1001 {
		t.Errorf("default limits must accept this: %v %d", err, len(out))
	}
}
