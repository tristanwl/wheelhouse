package app

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/instances"
)

// A save refused because another process changed the key under this server is
// a 503 that says what to do, and is logged; it is not a bare 500.
func TestSaveRefusedAfterTheKeyChangedSaysWhatToDo(t *testing.T) {
	var logs bytes.Buffer
	a := &configAPI{d: deps{log: slog.New(slog.NewTextHandler(&logs, nil))}}
	rec := httptest.NewRecorder()
	a.writeConfigError(rec, instances.ErrKeyChanged)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "Restart Wheelhouse") || strings.Contains(body, "rekey") {
		t.Errorf("body = %s", body)
	}
	if !strings.Contains(logs.String(), "secret key") {
		t.Errorf("log = %q", logs.String())
	}
}
