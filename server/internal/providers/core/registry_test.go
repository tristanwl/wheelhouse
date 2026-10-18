package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRegistryRejectsDuplicateIDs(t *testing.T) {
	a := fakeProvider("dup", RoleDownloadClient).def
	_, err := NewRegistry(a, a)
	if err == nil || err.Error() != "duplicate provider id: dup" {
		t.Fatalf("err = %v", err)
	}
}

func TestRegistryLooksUpByIDAndRole(t *testing.T) {
	sab := fakeProvider("sab", RoleDownloadClient).def
	plex := fakeProvider("plex", RoleMediaServer).def
	r := mustRegistry(t, sab, plex)
	if d, ok := r.Get("plex"); !ok || d.ID != "plex" {
		t.Fatalf("Get(plex) = %v, %v", d, ok)
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("Get(nope) found something")
	}
	got := r.ByRole(RoleDownloadClient)
	if len(got) != 1 || got[0].ID != "sab" {
		t.Fatalf("ByRole = %v", got)
	}
	if len(r.All()) != 2 || r.All()[0].ID != "sab" {
		t.Fatal("All should keep registration order")
	}
}

func TestRegistryRejectsMalformedDefinitions(t *testing.T) {
	good := fakeProvider("ok", RoleMetadata).def
	cases := map[string]func(*Definition){
		"bad id":              func(d *Definition) { d.ID = "Has Space" },
		"empty id":            func(d *Definition) { d.ID = "" },
		"bad role":            func(d *Definition) { d.Role = "converter" },
		"no create":           func(d *Definition) { d.Create = nil },
		"config not a struct": func(d *Definition) { d.Config = "nope" },
		"nil config":          func(d *Definition) { d.Config = nil },
		"pointer config":      func(d *Definition) { d.Config = &fakeConfig{} },
		"unknown option": func(d *Definition) {
			d.Config = struct {
				X string `json:"x" config:"requird"`
			}{}
		},
		"missing json tag": func(d *Definition) {
			d.Config = struct{ X string }{}
		},
		"unsupported type": func(d *Definition) {
			d.Config = struct {
				X []string `json:"x"`
			}{}
		},
		"url on bool": func(d *Definition) {
			d.Config = struct {
				X bool `json:"x" config:"url"`
			}{}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := good
			mutate(&d)
			if _, err := NewRegistry(d); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestDefinitionFieldsSecretKeysAndSchema(t *testing.T) {
	r := mustRegistry(t, fakeProvider("sab", RoleDownloadClient).def)
	d, _ := r.Get("sab")

	if keys := d.SecretKeys(); len(keys) != 1 || keys[0] != "apiKey" {
		t.Fatalf("SecretKeys = %v", keys)
	}
	fields := d.Fields()
	if len(fields) != 2 || fields[0].Name != "url" || fields[0].Label != "URL" ||
		fields[0].Format != "url" || !fields[0].Required || !fields[1].Secret {
		t.Fatalf("Fields = %+v", fields)
	}

	raw, err := json.Marshal(d.Schema())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"$schema":"https://json-schema.org/draft/2020-12/schema","title":"sab","type":"object",` +
		`"properties":{` +
		`"apiKey":{"type":"string","title":"API key","minLength":1,"writeOnly":true,"x-secret":true},` +
		`"url":{"type":"string","title":"URL","format":"uri","pattern":"^https?://","minLength":1}},` +
		`"required":["url","apiKey"],"x-order":["url","apiKey"]}`
	if string(raw) != want {
		t.Fatalf("schema =\n%s\nwant\n%s", raw, want)
	}
}

func TestDefinitionMarshalJSONIsPublic(t *testing.T) {
	r := mustRegistry(t, fakeProvider("sab", RoleDownloadClient).def)
	d, _ := r.Get("sab")
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"id", "role", "displayName", "configSchema", "secretKeys", "capabilities"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing %q in %s", k, raw)
		}
	}
	if strings.Contains(string(raw), "Create") {
		t.Error("factory leaked into JSON")
	}
}

func TestSchemaForOptionalAndOtherTypes(t *testing.T) {
	type cfg struct {
		Token   string `json:"token" label:"Token" config:"secret"`
		Port    int    `json:"port" label:"Port"`
		Verbose bool   `json:"verbose"`
	}
	r := mustRegistry(t, Definition{
		ID: "x", Role: RoleMetadata, DisplayName: "X", Config: cfg{},
		Create: func(any, ProviderContext) any { return nil },
	})
	d, _ := r.Get("x")
	s := d.Schema()
	if len(s.Required) != 0 {
		t.Fatalf("required = %v", s.Required)
	}
	if s.Properties["port"].Type != FieldInteger || s.Properties["port"].MinLength != nil {
		t.Fatalf("port = %+v", s.Properties["port"])
	}
	if s.Properties["verbose"].Type != FieldBoolean || s.Properties["verbose"].Title != "verbose" {
		t.Fatalf("verbose = %+v", s.Properties["verbose"])
	}
	raw, _ := json.Marshal(s)
	if strings.Contains(string(raw), `"required"`) {
		t.Fatalf("empty required should be omitted: %s", raw)
	}
}
