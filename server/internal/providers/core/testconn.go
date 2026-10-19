package core

import (
	"context"
	"time"
)

// ConnectionTester is what every role's client offers: a cheap authenticated
// call that says whether the service answers and accepts the credentials. It
// never returns an error; failures are in the result.
type ConnectionTester interface {
	TestConnection(ctx context.Context) ConnectionResult
}

// testTimeout bounds one connection test, a little over one upstream request.
const testTimeout = 12 * time.Second

// TestConnection builds a client from raw (unsaved) settings and calls its
// TestConnection. The settings are validated first: a *ValidationError is
// returned for a config that does not pass, with no values in it. Otherwise the
// result is redacted against the config's secrets and so safe to return to the
// administrator and to log; a client that cannot be tested reports that in the
// result. The request goes through the provider's own client, so it uses the
// core HTTP client and its limits (timeout, response size, denied networks).
func (d *Definition) TestConnection(ctx context.Context, instanceID, instanceName string, raw map[string]any) (ConnectionResult, error) {
	cfg, err := d.Decode(raw)
	if err != nil {
		return ConnectionResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	client := d.Create(cfg, ProviderContext{InstanceID: instanceID, InstanceName: instanceName})
	tester, ok := client.(ConnectionTester)
	if !ok {
		return ConnectionResult{OK: false, Error: "This service cannot be tested"}, nil
	}
	secrets := d.spec.secretValues(raw)
	res := tester.TestConnection(ctx)
	res.Error = Redact(res.Error, secrets...)
	res.Version = Redact(res.Version, secrets...)
	return res, nil
}
