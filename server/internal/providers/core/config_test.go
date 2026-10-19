package core

import (
	"errors"
	"strings"
	"testing"
)

const canary = "CANARY-secret-9f8e7d6c5b"

type validatedConfig struct {
	URL      string `json:"url" label:"URL" config:"required,url"`
	Token    string `json:"token" label:"Token" config:"secret"`
	Port     int    `json:"port" label:"Port"`
	Insecure bool   `json:"insecure" label:"Allow insecure"`
}

func (c validatedConfig) Validate() error {
	if c.Port == 99 {
		return errors.New("port 99 is reserved")
	}
	return nil
}

func validatedDef(t *testing.T) *Definition {
	t.Helper()
	r := mustRegistry(t, Definition{
		ID: "v", Role: RoleMetadata, DisplayName: "V", Config: validatedConfig{},
		Create: func(any, ProviderContext) any { return nil },
	})
	d, _ := r.Get("v")
	return d
}

func TestDecodeAcceptsValidConfigAndIgnoresUnknownKeys(t *testing.T) {
	d := validatedDef(t)
	got, err := d.Decode(map[string]any{
		"url": "http://192.0.2.10:8989/sonarr", "token": "t", "port": float64(8080), "insecure": true,
		"somethingElse": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := validatedConfig{URL: "http://192.0.2.10:8989/sonarr", Token: "t", Port: 8080, Insecure: true}
	if got != want {
		t.Fatalf("got %+v", got)
	}
}

func TestDecodeReportsEveryProblemInDeclarationOrder(t *testing.T) {
	d := validatedDef(t)
	_, err := d.Decode(map[string]any{"url": "not a url", "token": 5, "port": "x", "insecure": "yes"})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v", err)
	}
	want := "URL must be an http or https URL; Token must be a string; " +
		"Port must be a whole number; Allow insecure must be true or false"
	if err.Error() != want {
		t.Fatalf("err = %q", err)
	}

	// The zod test expects both fields named for {url: "not a url"}.
	_, err = fakeDef(t).Decode(map[string]any{"url": "not a url"})
	if err == nil || err.Error() != "URL must be an http or https URL; API key is required" {
		t.Fatalf("err = %v", err)
	}
}

func fakeDef(t *testing.T) *Definition {
	t.Helper()
	d, _ := mustRegistry(t, fakeProvider("sab", RoleDownloadClient).def).Get("sab")
	return d
}

func TestDecodeRequiredEmptyNilAndWrongShapes(t *testing.T) {
	d := fakeDef(t)
	cases := []struct {
		name string
		raw  map[string]any
		want string
	}{
		{"nil map", nil, "URL is required; API key is required"},
		{"explicit nil", map[string]any{"url": nil, "apiKey": nil}, "URL is required; API key is required"},
		{"empty string", map[string]any{"url": "http://h", "apiKey": ""}, "API key must not be empty"},
		{"ftp scheme", map[string]any{"url": "ftp://h", "apiKey": "k"}, "URL must be an http or https URL"},
		{"no host", map[string]any{"url": "http://", "apiKey": "k"}, "URL must be an http or https URL"},
		{"relative", map[string]any{"url": "/x", "apiKey": "k"}, "URL must be an http or https URL"},
		{"credentials", map[string]any{"url": "http://u:CANARY-PASS@h", "apiKey": "k"}, "URL must not contain a username or password"},
		{"fragment swallows the path", map[string]any{"url": "http://h:7878#/x", "apiKey": "k"}, "URL must not contain a query string (?...) or a fragment (#...)"},
		{"empty fragment", map[string]any{"url": "http://h:7878/#", "apiKey": "k"}, "URL must not contain a query string (?...) or a fragment (#...)"},
		{"query", map[string]any{"url": "http://h:7878/radarr?apikey=x", "apiKey": "k"}, "URL must not contain a query string (?...) or a fragment (#...)"},
		{"username only", map[string]any{"url": "http://CANARY-USER@h", "apiKey": "k"}, "URL must not contain a username or password"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := d.Decode(c.raw)
			if err == nil || err.Error() != c.want {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestDecodeRunsTheValidateHookOnlyWhenFieldsPass(t *testing.T) {
	d := validatedDef(t)
	_, err := d.Decode(map[string]any{"url": "http://h", "port": 99})
	if err == nil || err.Error() != "port 99 is reserved" {
		t.Fatalf("err = %v", err)
	}
	_, err = d.Decode(map[string]any{"url": "bad", "port": 99})
	if err == nil || strings.Contains(err.Error(), "reserved") {
		t.Fatalf("hook should not run when fields fail: %v", err)
	}
}

func TestDecodeAcceptsIntegerForms(t *testing.T) {
	d := validatedDef(t)
	for _, n := range []any{7, int64(7), float64(7)} {
		got, err := d.Decode(map[string]any{"url": "http://h", "port": n})
		if err != nil || got.(validatedConfig).Port != 7 {
			t.Fatalf("%T: %v %v", n, got, err)
		}
	}
	if _, err := d.Decode(map[string]any{"url": "http://h", "port": 7.5}); err == nil {
		t.Fatal("7.5 should not be a whole number")
	}
}

// Validation errors name the field and the problem, never the value.
func TestValidationErrorsNeverEchoValues(t *testing.T) {
	d := validatedDef(t)
	bad := []map[string]any{
		{"url": "http://" + canary + "@", "token": canary},
		{"url": canary, "token": canary},
		{"url": "ftp://" + canary, "token": 12345},
		{"url": []string{canary}, "token": map[string]any{"k": canary}, "port": canary, "insecure": canary},
		{"url": "http://h", "token": []any{canary}},
	}
	for _, raw := range bad {
		_, err := d.Decode(raw)
		if err == nil {
			t.Fatalf("expected an error for %v", raw)
		}
		if strings.Contains(err.Error(), canary) {
			t.Fatalf("error echoes a value: %v", err)
		}
	}
}
