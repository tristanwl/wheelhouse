package core

import (
	"fmt"
	"math"
	"net/url"
	"reflect"
	"strings"
)

// A provider describes its settings as a plain struct. Each exported field
// needs a `json` tag (the setting's name) and may carry:
//
//	label:"API key"                      the label an admin form shows
//	config:"required,secret,url"         options, comma separated
//
// Options: "required" (must be present), "secret" (masked in the UI, stripped
// from errors) and "url" (an http or https URL with a host). Supported field
// types are string, bool and int. The optional method
//
//	Validate() error
//
// on the struct runs after every field passed, for rules that span fields.
//
//	type SonarrConfig struct {
//		URL    string `json:"url" label:"URL" config:"required,url"`
//		APIKey string `json:"apiKey" label:"API key" config:"required,secret"`
//	}
//
// Like the zod schemas this replaces, unknown keys in the raw config are
// ignored, and a string setting that is present must not be empty.

// FieldType is the JSON Schema type of a setting.
type FieldType string

// The supported setting types.
const (
	FieldString  FieldType = "string"
	FieldBoolean FieldType = "boolean"
	FieldInteger FieldType = "integer"
)

// Field describes one setting of a provider.
type Field struct {
	Name     string    `json:"name"`
	Label    string    `json:"label"`
	Type     FieldType `json:"type"`
	Required bool      `json:"required"`
	Secret   bool      `json:"secret"`
	// Format is "url" or empty.
	Format string `json:"format,omitempty"`

	index int // position in the struct
}

// FieldError is one problem with one setting. Message never contains the value.
//
// Field is the setting's key in the stored configuration (it is how the admin
// form finds the input the problem belongs to, and is never shown). Label is
// the human name the admin form shows ("API key"), and Text() is the sentence
// to show ("API key is required"): user-facing text never names a key.
type FieldError struct {
	Field   string
	Label   string
	Message string
}

// Text is the problem as a sentence for people: the label, then the message.
// A problem that is not about one setting (Label empty) is just the message.
func (fe FieldError) Text() string {
	if fe.Label == "" {
		return fe.Message
	}
	return fe.Label + " " + fe.Message
}

// ValidationError lists everything wrong with a config. It is built from static
// text and labels only, so it is safe to show and to log.
type ValidationError struct {
	Errors []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Errors))
	for i, fe := range e.Errors {
		parts[i] = fe.Text()
	}
	return strings.Join(parts, "; ")
}

// configSpec is the parsed shape of a provider's config struct.
type configSpec struct {
	typ    reflect.Type
	fields []Field
}

func newConfigSpec(proto any) (*configSpec, error) {
	t := reflect.TypeOf(proto)
	if t == nil || t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("config must be a struct value, got %T", proto)
	}
	spec := &configSpec{typ: t}
	seen := map[string]bool{}
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			return nil, fmt.Errorf("config field %s needs a json tag", sf.Name)
		}
		if seen[name] {
			return nil, fmt.Errorf("config field %q is declared twice", name)
		}
		seen[name] = true
		f := Field{Name: name, Label: sf.Tag.Get("label"), index: i}
		if f.Label == "" {
			// Errors and notices name a setting by its label, never its key.
			return nil, fmt.Errorf("config field %q needs a label tag (the name people see, such as label:\"API key\")", name)
		}
		switch sf.Type.Kind() {
		case reflect.String:
			f.Type = FieldString
		case reflect.Bool:
			f.Type = FieldBoolean
		case reflect.Int:
			f.Type = FieldInteger
		default:
			return nil, fmt.Errorf("config field %q has unsupported type %s", name, sf.Type)
		}
		if opts := sf.Tag.Get("config"); opts != "" {
			for opt := range strings.SplitSeq(opts, ",") {
				switch opt {
				case "required":
					f.Required = true
				case "secret":
					f.Secret = true
				case "url":
					if f.Type != FieldString {
						return nil, fmt.Errorf("config field %q: url needs a string", name)
					}
					f.Format = "url"
				default:
					return nil, fmt.Errorf("config field %q has unknown option %q", name, opt)
				}
			}
		}
		spec.fields = append(spec.fields, f)
	}
	if len(spec.fields) == 0 {
		return nil, fmt.Errorf("config %s has no fields", t)
	}
	return spec, nil
}

type validator interface{ Validate() error }

// decode checks the raw settings and returns the typed config struct (a value,
// not a pointer). Errors are built from field names and fixed text only.
func (s *configSpec) decode(raw map[string]any) (any, error) {
	out := reflect.New(s.typ).Elem()
	var problems []FieldError
	fail := func(f Field, msg string) {
		problems = append(problems, FieldError{Field: f.Name, Label: f.Label, Message: msg})
	}

	for _, f := range s.fields {
		v, present := raw[f.Name]
		if !present || v == nil {
			if f.Required {
				fail(f, "is required")
			}
			continue
		}
		dst := out.Field(f.index)
		switch f.Type {
		case FieldString:
			str, ok := v.(string)
			switch {
			case !ok:
				fail(f, "must be a string")
			case str == "":
				fail(f, "must not be empty")
			case f.Format == "url" && !validHTTPURL(str):
				fail(f, "must be an http or https URL")
			case f.Format == "url" && hasQueryOrFragment(str):
				fail(f, "must not contain a query string (?...) or a fragment (#...)")
			case f.Format == "url" && hasURLCredentials(str):
				fail(f, "must not contain a username or password")
			default:
				dst.SetString(str)
			}
		case FieldBoolean:
			if b, ok := v.(bool); ok {
				dst.SetBool(b)
			} else {
				fail(f, "must be true or false")
			}
		case FieldInteger:
			if n, ok := toInt(v); ok {
				dst.SetInt(int64(n))
			} else {
				fail(f, "must be a whole number")
			}
		}
	}
	if len(problems) == 0 {
		if v, ok := out.Interface().(validator); ok {
			if err := v.Validate(); err != nil {
				problems = append(problems, FieldError{Field: "config", Message: err.Error()})
			}
		}
	}
	if len(problems) > 0 {
		return nil, &ValidationError{Errors: problems}
	}
	return out.Interface(), nil
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if n == math.Trunc(n) && math.Abs(n) < 1<<53 {
			return int(n), true
		}
	}
	return 0, false
}

// validHTTPURL accepts absolute http and https URLs with a host. (Stricter than
// zod's z.url(), which accepts any scheme.)
func validHTTPURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
}

// hasQueryOrFragment reports whether a URL has a "?" or "#" part. Instance URLs
// are bases that API paths are appended to: a fragment would swallow the path
// (the request would go to the base address alone, with the credentials), and a
// query string would be attached to every request or break the join.
func hasQueryOrFragment(s string) bool {
	return strings.ContainsAny(s, "?#")
}

// hasURLCredentials reports whether a URL carries "user:pass@". The Next app
// never worked with these (fetch refuses them), and a credential belongs in the
// provider's own secret setting, not in a URL that is logged and linked.
func hasURLCredentials(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.User != nil
}

// secretValues returns the values of the secret settings present in raw, for
// redacting any error text built from that config.
func (s *configSpec) secretValues(raw map[string]any) []string {
	var out []string
	for _, f := range s.fields {
		if !f.Secret {
			continue
		}
		if str, ok := raw[f.Name].(string); ok {
			out = append(out, str)
		}
	}
	return out
}
