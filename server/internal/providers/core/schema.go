package core

// Schema is a small JSON Schema (draft 2020-12) description of a provider's
// config, enough for the admin UI to render a form. It is built only from the
// struct's metadata, never from values.
type Schema struct {
	Schema     string              `json:"$schema"`
	Title      string              `json:"title"`
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
	// Order lists the property names in declaration order (JSON objects are
	// unordered), so forms render fields the way the provider declares them.
	Order []string `json:"x-order"`
}

// Property is one setting in a Schema.
type Property struct {
	Type      FieldType `json:"type"`
	Title     string    `json:"title"`
	Format    string    `json:"format,omitempty"`
	Pattern   string    `json:"pattern,omitempty"`
	MinLength *int      `json:"minLength,omitempty"`
	// WriteOnly marks credentials: shown masked, never returned by an API.
	WriteOnly bool `json:"writeOnly,omitempty"`
	Secret    bool `json:"x-secret,omitempty"`
}

func buildSchema(title string, fields []Field) Schema {
	s := Schema{
		Schema:     "https://json-schema.org/draft/2020-12/schema",
		Title:      title,
		Type:       "object",
		Properties: make(map[string]Property, len(fields)),
		Order:      make([]string, 0, len(fields)),
	}
	for _, f := range fields {
		p := Property{Type: f.Type, Title: f.Label, WriteOnly: f.Secret, Secret: f.Secret}
		if f.Type == FieldString {
			one := 1
			p.MinLength = &one
		}
		if f.Format == "url" {
			p.Format = "uri"
			p.Pattern = "^https?://"
		}
		s.Properties[f.Name] = p
		s.Order = append(s.Order, f.Name)
		if f.Required {
			s.Required = append(s.Required, f.Name)
		}
	}
	return s
}
