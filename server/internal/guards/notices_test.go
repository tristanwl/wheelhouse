package guards

import (
	"fmt"
	"go/ast"
	"go/token"
	"regexp"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/providers/all"
	"github.com/tristanwl/wheelhouse/server/internal/providers/core"
)

// camelKeys are the setting keys of every provider that are camelCase ("apiKey",
// "baseUrl"): the JSON names of the stored configuration, which people never
// see. All-lowercase keys ("url", "token") read as ordinary words, so they are
// covered by the format check below instead.
func camelKeys(t *testing.T) map[string]bool {
	t.Helper()
	reg, err := all.Registry()
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, def := range reg.All() {
		for _, f := range def.Fields() {
			if f.Name != strings.ToLower(f.Name) {
				keys[f.Name] = true
			}
		}
	}
	if len(keys) == 0 {
		t.Fatal("no camelCase setting key found in the registry; the guard would check nothing")
	}
	return keys
}

// Notices and errors name a setting by the label people see ("API key"), never
// by its key ("apiKey", issue #85). Statically: no string literal in production
// code that is more than the bare key may contain a camelCase key of the
// registry (a bare literal is a map key or a lookup, not text).
func TestNoUserFacingStringNamesACamelCaseSettingKey(t *testing.T) {
	keys := camelKeys(t)
	production(t, func(path string, fset *token.FileSet, f *ast.File) {
		tags := map[*ast.BasicLit]bool{} // struct tags carry the key by design
		ast.Inspect(f, func(n ast.Node) bool {
			if field, ok := n.(*ast.Field); ok && field.Tag != nil {
				tags[field.Tag] = true
			}
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || tags[lit] {
				return true
			}
			if s, ok := literal(lit); ok {
				for k := range keys {
					if s != k && strings.Contains(s, k) {
						t.Errorf("%s: the string %q names the setting key %q; use the field's label (core.Definition.Label)", fset.Position(lit.Pos()), s, k)
					}
				}
			}
			return true
		})
	})
}

// Dynamically: every way a provider's configuration can be refused yields text
// that names the setting by its label, never by its key or in the old
// "key: message" form, in the error and in each field's message.
func TestEveryConfigurationProblemIsWrittenWithLabels(t *testing.T) {
	reg, err := all.Registry()
	if err != nil {
		t.Fatal(err)
	}
	camel := camelKeys(t)
	for _, def := range reg.All() {
		valid := map[string]any{}
		for _, f := range def.Fields() {
			switch f.Type {
			case core.FieldString:
				if f.Format == "url" {
					valid[f.Name] = "http://192.0.2.10:1234"
				} else {
					valid[f.Name] = "value"
				}
			case core.FieldBoolean:
				valid[f.Name] = true
			case core.FieldInteger:
				valid[f.Name] = 5
			}
		}
		if _, err := def.Decode(valid); err != nil {
			t.Fatalf("%s: the valid config was refused: %v", def.ID, err)
		}
		cases := map[string]map[string]any{"empty": {}}
		for _, f := range def.Fields() {
			bad := func(name string, v any) {
				m := map[string]any{}
				for k, x := range valid {
					m[k] = x
				}
				m[f.Name] = v
				cases[f.Name+" "+name] = m
			}
			bad("wrong type", struct{}{})
			bad("nil", nil)
			bad("empty", "")
			bad("text", "not a url")
			bad("query", "http://192.0.2.10/x?y=1")
			bad("credentials", "http://user:pw@192.0.2.10")
			bad("number", 1.5)
		}
		refused := 0
		for name, raw := range cases {
			_, err := def.Decode(raw)
			if err == nil {
				continue
			}
			refused++
			ve, ok := err.(*core.ValidationError)
			if !ok {
				t.Errorf("%s %s: %T", def.ID, name, err)
				continue
			}
			texts := []string{err.Error()}
			for _, fe := range ve.Errors {
				texts = append(texts, fe.Text())
				if fe.Field != "config" && fe.Label == "" {
					t.Errorf("%s %s: the problem with %q carries no label", def.ID, name, fe.Field)
				}
			}
			for _, text := range texts {
				for k := range camel {
					if strings.Contains(text, k) {
						t.Errorf("%s %s: %q names the key %q", def.ID, name, text, k)
					}
				}
				for _, f := range def.Fields() {
					if regexp.MustCompile(`\b` + regexp.QuoteMeta(f.Name) + `:`).MatchString(text) {
						t.Errorf("%s %s: %q is in the old \"key: message\" form", def.ID, name, text)
					}
				}
			}
		}
		if refused == 0 {
			t.Errorf("%s: nothing was ever refused; the cases do not exercise validation", def.ID)
		}
		// The missing required settings read as sentences.
		_, err = def.Decode(map[string]any{})
		want := []string{}
		for _, f := range def.Fields() {
			if f.Required {
				want = append(want, fmt.Sprintf("%s is required", f.Label))
			}
		}
		if len(want) > 0 && (err == nil || err.Error() != strings.Join(want, "; ")) {
			t.Errorf("%s: an empty config says %v, want %q", def.ID, err, strings.Join(want, "; "))
		}
	}
}
