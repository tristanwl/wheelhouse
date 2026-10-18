package core

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

// fakeConfig mirrors the zod schema the TypeScript runtime tests use.
type fakeConfig struct {
	URL    string `json:"url" label:"URL" config:"required,url"`
	APIKey string `json:"apiKey" label:"API key" config:"required,secret"`
}

type fakeClient struct {
	ID   string
	URL  string
	Name string
}

type fakeProviderT struct {
	def     Definition
	creates *atomic.Int32
}

func fakeProvider(id string, role Role) fakeProviderT {
	creates := &atomic.Int32{}
	return fakeProviderT{
		creates: creates,
		def: Definition{
			ID:           id,
			Role:         role,
			DisplayName:  id,
			Config:       fakeConfig{},
			Capabilities: map[string]string{"protocol": "usenet"},
			Create: func(cfg any, pc ProviderContext) any {
				creates.Add(1)
				return &fakeClient{ID: pc.InstanceID, URL: cfg.(fakeConfig).URL, Name: pc.InstanceName}
			},
		},
	}
}

func mustRegistry(t *testing.T, defs ...Definition) *Registry {
	t.Helper()
	r, err := NewRegistry(defs...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type staticSource struct{ instances []Instance }

func (s staticSource) Instances(context.Context) ([]Instance, error) { return s.instances, nil }

func inst(id, providerID string, config map[string]any, enabled bool) Instance {
	return Instance{ID: id, ProviderID: providerID, Name: id, Enabled: enabled, Config: config}
}

func sprint(format string, args ...any) string { return fmt.Sprintf(format, args...) }
