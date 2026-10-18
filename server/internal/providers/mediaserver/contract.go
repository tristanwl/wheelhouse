package mediaserver

import (
	"context"
	"regexp"
	"testing"
)

// configKeyShape matches things that look like environment variable names
// (PLEX_TOKEN): notices must never show them.
var configKeyShape = regexp.MustCompile(`[A-Z]{3,}_[A-Z_]+`)

// RunContract checks the invariants every media-server adapter must satisfy.
// setup returns a client for a healthy, claimed server it is authenticated to
// (and registers its own cleanup). It is the port of describeMediaServerContract.
func RunContract(t *testing.T, name string, setup func(t *testing.T) Client) {
	t.Helper()
	t.Run(name+" satisfies the MediaServer contract", func(t *testing.T) {
		client := setup(t)
		ctx := context.Background()

		t.Run("testConnection reports ok with a version", func(t *testing.T) {
			res := client.TestConnection(ctx)
			if !res.OK {
				t.Fatalf("not ok: %q", res.Error)
			}
			if res.Version == "" {
				t.Error("no version")
			}
		})

		t.Run("status is ok, authenticated and has a version and name", func(t *testing.T) {
			st, err := client.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if st.State != StateOK {
				t.Errorf("state = %q", st.State)
			}
			if !st.Authenticated {
				t.Error("not authenticated")
			}
			if st.Version == "" || st.Name == "" {
				t.Errorf("version %q, name %q", st.Version, st.Name)
			}
		})

		t.Run("session count, when present, is a non-negative integer", func(t *testing.T) {
			st, err := client.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// The contract requires it for an authenticated server.
			if st.ActiveSessions == nil {
				t.Fatal("no session count")
			}
			if *st.ActiveSessions < 0 {
				t.Errorf("sessions = %d", *st.ActiveSessions)
			}
		})

		t.Run("notices never mention configuration key names", func(t *testing.T) {
			st, err := client.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if configKeyShape.MatchString(st.Notice) {
				t.Errorf("notice %q looks like a config key", st.Notice)
			}
		})
	})
}

// CheckNotice fails the test when a notice looks like it names a
// configuration key. Exposed so provider tests can apply the same rule to the
// notices of unhealthy states.
func CheckNotice(t *testing.T, notice string) {
	t.Helper()
	if configKeyShape.MatchString(notice) {
		t.Errorf("notice %q looks like a config key", notice)
	}
}
