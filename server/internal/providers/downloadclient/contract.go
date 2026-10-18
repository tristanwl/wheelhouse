package downloadclient

import (
	"context"
	"slices"
	"testing"
	"time"
)

// RunContract runs the invariants every download-client adapter must satisfy,
// whatever the backend. setup returns a client pointed at a service that has at
// least one active job, and the protocol it should report. Register cleanup
// with t.Cleanup.
//
// It lives in a non-test file so the adapters' test packages can import it.
func RunContract(t *testing.T, name string, setup func(t *testing.T) (Client, Protocol)) {
	t.Helper()
	t.Run(name+" satisfies the Client contract", func(t *testing.T) {
		client, protocol := setup(t)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		t.Run("TestConnection reports ok with a version", func(t *testing.T) {
			res := client.TestConnection(ctx)
			if !res.OK {
				t.Fatalf("not ok: %q", res.Error)
			}
			if res.Error != "" {
				t.Errorf("error set on success: %q", res.Error)
			}
			if res.Version == "" {
				t.Error("no version")
			}
		})

		t.Run("Status returns sane, normalized values", func(t *testing.T) {
			s, err := client.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if s.SpeedBytesPerSec < 0 {
				t.Errorf("negative speed %d", s.SpeedBytesPerSec)
			}
			if s.QueueCount < 0 {
				t.Errorf("negative queue count %d", s.QueueCount)
			}
			if s.ETASeconds != nil && *s.ETASeconds <= 0 {
				t.Errorf("eta must be nil or positive, got %d", *s.ETASeconds)
			}
			if s.Warnings == nil {
				t.Error("warnings must be an empty list, not nil")
			}
			for _, w := range s.Warnings {
				if w.Message == "" {
					t.Error("empty warning message")
				}
				if w.Severity != SeverityWarning && w.Severity != SeverityError {
					t.Errorf("bad severity %q", w.Severity)
				}
				if w.At != "" {
					if _, err := time.Parse(time.RFC3339Nano, w.At); err != nil {
						t.Errorf("warning time %q is not ISO 8601", w.At)
					}
				}
			}
		})

		t.Run("Items honour the unit and range rules", func(t *testing.T) {
			items, err := client.Items(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) == 0 {
				t.Fatal("expected at least one item")
			}
			for _, item := range items {
				if item.ID == "" || item.Name == "" {
					t.Errorf("item needs an id and a name: %+v", item)
				}
				if item.Protocol != protocol {
					t.Errorf("%s: protocol %q, want %q", item.ID, item.Protocol, protocol)
				}
				if !slices.Contains(States, item.State) {
					t.Errorf("%s: unknown state %q", item.ID, item.State)
				}
				if item.SizeBytes < 0 || item.RemainingBytes < 0 || item.RemainingBytes > item.SizeBytes {
					t.Errorf("%s: size %d, remaining %d", item.ID, item.SizeBytes, item.RemainingBytes)
				}
				if item.Progress < 0 || item.Progress > 1 {
					t.Errorf("%s: progress %v outside 0..1", item.ID, item.Progress)
				}
				if item.ETASeconds != nil && *item.ETASeconds <= 0 {
					t.Errorf("%s: eta must be nil or positive, got %d", item.ID, *item.ETASeconds)
				}
				if (item.Torrent != nil) != (protocol == Torrent) {
					t.Errorf("%s: torrent details present = %v for protocol %q", item.ID, item.Torrent != nil, protocol)
				}
			}
		})

		t.Run("item ids are unique", func(t *testing.T) {
			items, err := client.Items(ctx)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for _, item := range items {
				if seen[item.ID] {
					t.Errorf("duplicate id %q", item.ID)
				}
				seen[item.ID] = true
			}
		})
	})
}
