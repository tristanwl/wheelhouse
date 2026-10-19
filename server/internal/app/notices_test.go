package app

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/providers/all"
)

// Issue #85: a refused configuration is answered with the field's key (so the
// form can put the problem next to the right input) and human text that never
// shows that key.
func TestFormErrorsMapToTheFieldKeyButReadAsHumanText(t *testing.T) {
	reg, err := all.Registry()
	if err != nil {
		t.Fatal(err)
	}
	def, _ := reg.Get("radarr")
	_, err = def.Decode(map[string]any{"url": "http://192.0.2.10:7878"})
	if err == nil {
		t.Fatal("a Radarr without a key was accepted")
	}
	a := &configAPI{d: deps{log: slog.New(slog.DiscardHandler)}}
	rec := httptest.NewRecorder()
	a.writeConfigError(rec, err)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Error  string `json:"error"`
		Fields []struct{ Field, Message string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Fields) != 1 || body.Fields[0].Field != "apiKey" || body.Fields[0].Message != "API key is required" {
		t.Errorf("fields = %+v", body.Fields)
	}
	if strings.Contains(body.Error, "apiKey") || strings.Contains(body.Fields[0].Message, "apiKey") {
		t.Errorf("the text names the key: %s", rec.Body.String())
	}
}
