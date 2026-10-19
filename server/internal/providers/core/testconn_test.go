package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type testerClient struct{ res ConnectionResult }

func (c testerClient) TestConnection(context.Context) ConnectionResult { return c.res }

func TestDefinitionTestConnectionValidatesAndRedacts(t *testing.T) {
	const key = "canary-test-key-9f8e7d"
	reg := mustRegistry(t, Definition{
		ID: "svc", Role: RoleMediaManager, DisplayName: "Svc", Config: fakeConfig{},
		Create: func(cfg any, _ ProviderContext) any {
			// A careless client that echoes the key in its error.
			return testerClient{ConnectionResult{Error: "rejected " + cfg.(fakeConfig).APIKey, Version: "1.0"}}
		},
	}, Definition{
		ID: "plain", Role: RoleMetadata, DisplayName: "Plain", Config: fakeConfig{},
		Create: func(any, ProviderContext) any { return struct{}{} },
	})
	def, _ := reg.Get("svc")
	res, err := def.TestConnection(context.Background(), "svc", "Svc", map[string]any{"url": "http://192.0.2.1", "apiKey": key})
	if err != nil || res.OK || strings.Contains(res.Error, key) || res.Version != "1.0" {
		t.Errorf("result = %+v, %v", res, err)
	}
	var ve *ValidationError
	if _, err := def.TestConnection(context.Background(), "svc", "Svc", map[string]any{"url": "x"}); !errors.As(err, &ve) {
		t.Errorf("invalid config: %v", err)
	}
	other, _ := reg.Get("plain")
	if res, err := other.TestConnection(context.Background(), "p", "P", map[string]any{"url": "http://192.0.2.1", "apiKey": key}); err != nil || res.OK {
		t.Errorf("untestable: %+v %v", res, err)
	}
}
