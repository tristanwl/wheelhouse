package mediamanager_test

import (
	"context"
	"strings"
	"testing"

	mm "github.com/tristanwl/wheelhouse/server/internal/providers/mediamanager"
)

type stubManager struct{ mm.Manager }

// A panic in one instance's callback is that instance's problem, not the server's.
func TestCollectFromManagersSurvivesAPanickingCallback(t *testing.T) {
	entries := []mm.Entry{
		{InstanceID: "a", Name: "A", Client: stubManager{}},
		{InstanceID: "b", Name: "B", Client: stubManager{}},
	}
	results, problems := mm.CollectFromManagers(context.Background(), entries,
		func(_ context.Context, _ mm.Manager, e mm.Entry) (int, error) {
			if e.InstanceID == "a" {
				panic("boom CANARY-PANIC")
			}
			return 1, nil
		})
	if len(results) != 1 || results[0].Entry.InstanceID != "b" || len(problems) != 1 || problems[0].InstanceID != "a" {
		t.Fatalf("results %v problems %v", results, problems)
	}
	if strings.Contains(problems[0].Error, "CANARY-PANIC") {
		t.Errorf("panic text reached the problem: %q", problems[0].Error)
	}
}
