package main

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/secrets"
	"github.com/tristanwl/wheelhouse/server/internal/store"
)

func TestForgetSecretsCommand(t *testing.T) {
	ctx := context.Background()
	raw1 := make([]byte, secrets.KeySize)
	raw2 := make([]byte, secrets.KeySize)
	for i := range raw1 {
		raw1[i], raw2[i] = byte(i+1), byte(200-i)
	}
	k1, err := secrets.NewKey(raw1)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := secrets.NewKey(raw2)
	if err != nil {
		t.Fatal(err)
	}
	b64 := func(raw []byte) string { return base64.StdEncoding.EncodeToString(raw) }

	e, out := newEnv(t, map[string]string{secrets.EnvKey: b64(raw1)})
	db, err := store.Open(ctx, e.dbPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UnlockSecrets(ctx, secrets.Loaded{Key: k1, Source: secrets.SourceEnv}, true); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	// Without --yes nothing happens; with a key that works there is nothing to forget.
	if err := runForgetSecrets(ctx, e, nil); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("without --yes: %v", err)
	}
	if err := runForgetSecrets(ctx, e, []string{"--yes"}); err == nil || !strings.Contains(err.Error(), "nothing to forget") {
		t.Errorf("with a working key: %v", err)
	}

	// The key is lost: a different one is configured now.
	lost := e
	lost.getenv = func(k string) string {
		if k == secrets.EnvKey {
			return b64(raw2)
		}
		return ""
	}
	if err := runForgetSecrets(ctx, lost, []string{"--yes"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Discarded 0 stored secret value(s)") || strings.Contains(out.String(), b64(raw2)) {
		t.Errorf("output: %q", out.String())
	}
	db, err = store.Open(ctx, e.dbPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.UnlockSecrets(ctx, secrets.Loaded{Key: k2, Source: secrets.SourceEnv}, false); err != nil {
		t.Errorf("the new key does not open the database: %v", err)
	}
}
