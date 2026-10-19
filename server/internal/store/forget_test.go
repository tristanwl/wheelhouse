package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/secrets"
)

// A lost master key: the stored secrets are discarded, accounts are untouched,
// and the database works under the new key from then on.
func TestForgetSecretsAfterKeyLoss(t *testing.T) {
	db := openTemp(t)
	createSecretsTable(t, db)
	oldKey := mustKey(t)
	c := unlock(t, db, oldKey)
	putSecret(t, db, c, "a", "api_key", "provider-key-1")
	putSecret(t, db, c, "a", "password", "provider-password-1")
	putSecret(t, db, c, "b", "api_key", "provider-key-2")
	addUser(t, db, idA, "alice", "admin", 0)

	lost := mustKey(t) // what a freshly generated key file would be
	if _, err := db.UnlockSecrets(ctx(t), secrets.Loaded{Key: lost, Source: secrets.SourceEnv}, true); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("unlock with the wrong key: %v", err)
	}

	next, n, err := db.forgetSecrets(ctx(t), lost, testCols)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("discarded %d values, want 3", n)
	}
	if next.Version() != c.Version()+1 {
		t.Errorf("key version %d, want %d", next.Version(), c.Version()+1)
	}
	var nonNull int
	if err := db.QueryRow(ctx(t), "SELECT count(*) FROM test_secrets WHERE api_key IS NOT NULL OR password IS NOT NULL").Scan(&nonNull); err != nil || nonNull != 0 {
		t.Fatalf("%d values left, %v", nonNull, err)
	}
	// Unaffected: the account (a password hash, never encrypted).
	if count(t, db, "users") != 1 || count(t, db, "test_secrets") != 2 {
		t.Fatalf("users %d, rows %d", count(t, db, "users"), count(t, db, "test_secrets"))
	}
	// The new key is the database's key now, and new secrets work under it.
	again, err := db.UnlockSecrets(ctx(t), secrets.Loaded{Key: lost, Source: secrets.SourceEnv}, false)
	if err != nil {
		t.Fatalf("unlock with the new key: %v", err)
	}
	putSecret(t, db, again, "a", "api_key", "entered-again")
	if got, err := getSecret(t, db, again, "a", "api_key"); err != nil || got != "entered-again" {
		t.Fatalf("%q %v", got, err)
	}
	// The old key is no longer the key.
	if _, err := db.UnlockSecrets(ctx(t), secrets.Loaded{Key: oldKey, Source: secrets.SourceEnv}, false); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("the old key still opens the database: %v", err)
	}
}

func TestForgetSecretsRefusesWhileTheKeyStillWorks(t *testing.T) {
	db := openTemp(t)
	createSecretsTable(t, db)
	k := mustKey(t)
	c := unlock(t, db, k)
	putSecret(t, db, c, "a", "api_key", "provider-key-1")
	_, _, err := db.forgetSecrets(ctx(t), k, testCols)
	if err == nil || !strings.Contains(err.Error(), "nothing to forget") {
		t.Fatalf("err = %v", err)
	}
	if got, err := getSecret(t, db, c, "a", "api_key"); err != nil || got != "provider-key-1" {
		t.Fatalf("a readable secret was discarded: %q %v", got, err)
	}
}
