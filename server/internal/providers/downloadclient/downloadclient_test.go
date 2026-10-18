package downloadclient

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
)

func TestDescribeState(t *testing.T) {
	for _, tc := range []struct {
		state  State
		detail string
		want   string
	}{
		{StatePostprocessing, "Extracting", "Extracting"},
		{StatePostprocessing, "", "Processing"},
		{StateQueued, "", "Queued"},
		{StateQueued, "Grabbing", "Grabbing"},
		{StateDownloading, "", "Downloading"},
		{StateStalled, "", "Stalled"},
		{StateSeeding, "", "Seeding"},
		{StatePaused, "", "Paused"},
		{StateCompleted, "", "Completed"},
		{StateFailed, "Files missing", "Files missing"},
		{StateFailed, "", "Failed"},
	} {
		if got := DescribeState(tc.state, tc.detail); got != tc.want {
			t.Errorf("DescribeState(%s, %q) = %q, want %q", tc.state, tc.detail, got, tc.want)
		}
	}
}

func TestNumber(t *testing.T) {
	var v struct {
		A, B, C, D, E, F, G Number
	}
	in := `{"A":"51200.00","B":12.5,"C":null,"D":"abc","E":"","F":{"x":1},"G":" 7 "}`
	if err := json.Unmarshal([]byte(in), &v); err != nil {
		t.Fatal(err)
	}
	if !v.A.Valid() || v.A.Float() != 51200 || v.B.Float() != 12.5 || v.G.Float() != 7 {
		t.Errorf("%+v", v)
	}
	if v.C.Valid() || v.D.Valid() || v.F.Valid() || v.C.Float() != 0 {
		t.Errorf("garbage should be invalid: %+v", v)
	}
	if !v.E.Valid() || v.E.Float() != 0 { // JavaScript: Number("") === 0
		t.Errorf("%+v", v.E)
	}
	if v.B.Whole() != 13 || v.A.Times(1024) != 51200*1024 {
		t.Errorf("rounding: %d %d", v.B.Whole(), v.A.Times(1024))
	}
	var neg, huge Number
	_ = json.Unmarshal([]byte(`-5`), &neg)
	_ = json.Unmarshal([]byte(`1e300`), &huge)
	if neg.Whole() != 0 || huge.Whole() != MaxSafe {
		t.Errorf("clamping: %d %d", neg.Whole(), huge.Whole())
	}
	if Clamp01(2) != 1 || Clamp01(-1) != 0 || Clamp01(0.5) != 0.5 {
		t.Error("Clamp01")
	}
}

func TestFailedClient(t *testing.T) {
	c := Failed(&core.ProviderError{Message: "x: invalid base URL"})
	if r := c.TestConnection(context.Background()); r.OK || r.Error != "x: invalid base URL" {
		t.Errorf("%+v", r)
	}
	if _, err := c.Status(context.Background()); err == nil {
		t.Error("Status")
	}
	if _, err := c.Items(context.Background()); err == nil {
		t.Error("Items")
	}
	// Only ProviderErrors are shown: anything else could be unredacted.
	if got := ErrorText(errors.New("dial tcp http://host/?apikey=secret")); got != "unknown error" {
		t.Errorf("%q", got)
	}
}

func TestSecretFormsAndLeaked(t *testing.T) {
	const s = `k/+&"x-123`
	if Leaked("nothing here", s) != "" {
		t.Error("false positive")
	}
	for _, form := range SecretForms(s) {
		if Leaked("prefix "+form+" suffix", s) == "" {
			t.Errorf("missed %q", form)
		}
	}
	if !strings.Contains(strings.Join(SecretForms(s), "|"), `k/+&\"x-123`) {
		t.Error("JSON-escaped form missing")
	}
}

// fakeClient is a well-behaved client, for the contract suite itself.
type fakeClient struct{}

func (fakeClient) TestConnection(context.Context) core.ConnectionResult {
	return core.ConnectionResult{OK: true, Version: "1"}
}
func (fakeClient) Status(context.Context) (Status, error) {
	return Status{Warnings: []Warning{{Message: "m", Severity: SeverityWarning, At: "2026-10-18T07:40:24.000Z"}}}, nil
}
func (fakeClient) Items(context.Context) ([]Item, error) {
	return []Item{{ID: "a", Name: "A", Protocol: Usenet, State: StateQueued, Progress: 0.5, SizeBytes: 10, RemainingBytes: 5}}, nil
}

func TestContractAcceptsAWellBehavedClient(t *testing.T) {
	RunContract(t, "fake", func(*testing.T) (Client, Protocol) { return fakeClient{}, Usenet })
}
