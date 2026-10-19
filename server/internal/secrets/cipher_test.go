package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"testing"
)

func testCipher(t *testing.T, version uint32) *Cipher {
	t.Helper()
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewCipher(k, version)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRoundTrip(t *testing.T) {
	c := testCipher(t, 1)
	ad := AD("instances", "sonarr", "apiKey")
	for _, pt := range [][]byte{nil, {}, []byte("x"), []byte("canary-api-key-0123456789"), bytes.Repeat([]byte{0xAB}, 100_000)} {
		env, err := c.Seal(pt, ad)
		if err != nil {
			t.Fatal(err)
		}
		if len(env) != MinEnvelopeLen+len(pt) {
			t.Errorf("envelope is %d bytes for %d of plaintext", len(env), len(pt))
		}
		got, err := c.Open(env, ad)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pt) {
			t.Errorf("round trip changed the value")
		}
	}
}

func TestEnvelopeLayout(t *testing.T) {
	c := testCipher(t, 0x01020304)
	env, _ := c.Seal([]byte("hello"), nil)
	if env[0] != 0x01 {
		t.Errorf("format byte %#x", env[0])
	}
	if v := binary.BigEndian.Uint32(env[1:5]); v != 0x01020304 {
		t.Errorf("version %#x", v)
	}
	if v, err := EnvelopeVersion(env); err != nil || v != 0x01020304 {
		t.Errorf("EnvelopeVersion = %#x, %v", v, err)
	}
	if bytes.Contains(env, []byte("hello")) {
		t.Error("plaintext visible in the envelope")
	}
}

// Every byte of the envelope is authenticated: the format and version in the
// header, the nonce, the ciphertext and the tag.
func TestTamperDetectionEveryByte(t *testing.T) {
	c := testCipher(t, 1)
	ad := AD("t", "1", "f")
	env, _ := c.Seal([]byte("a secret value of some length"), ad)
	for i := range env {
		for _, mask := range []byte{0x01, 0x80, 0xFF} {
			bad := bytes.Clone(env)
			bad[i] ^= mask
			if got, err := c.Open(bad, ad); err == nil {
				t.Fatalf("flipping byte %d (mask %#x) was accepted, got %q", i, mask, got)
			}
		}
	}
	// Truncation and extension.
	for _, bad := range [][]byte{env[:len(env)-1], env[:MinEnvelopeLen-1], nil, append(bytes.Clone(env), 0)} {
		if _, err := c.Open(bad, ad); err == nil {
			t.Errorf("a %d byte envelope was accepted", len(bad))
		}
	}
}

func TestTamperErrorKinds(t *testing.T) {
	c := testCipher(t, 1)
	env, _ := c.Seal([]byte("v"), nil)

	bad := bytes.Clone(env)
	bad[0] = 0x02
	if _, err := c.Open(bad, nil); !errors.Is(err, ErrMalformed) {
		t.Errorf("unknown format: %v", err)
	}
	bad = bytes.Clone(env)
	bad[len(bad)-1] ^= 1
	if _, err := c.Open(bad, nil); !errors.Is(err, ErrDecrypt) {
		t.Errorf("flipped tag: %v", err)
	}
	other := testCipher(t, 2)
	if _, err := other.Open(env, nil); !errors.Is(err, ErrKeyVersion) {
		t.Errorf("other version: %v", err)
	}
}

func TestWrongAssociatedData(t *testing.T) {
	c := testCipher(t, 1)
	env, _ := c.Seal([]byte("v"), AD("instances", "radarr", "apiKey"))
	for _, ad := range [][]byte{
		AD("instances", "radarr-4k", "apiKey"), // another row
		AD("instances", "radarr", "password"),  // another field
		AD("users", "radarr", "apiKey"),        // another table
		nil,
		{},
	} {
		if _, err := c.Open(env, ad); !errors.Is(err, ErrDecrypt) {
			t.Errorf("ad %q opened: %v", ad, err)
		}
	}
	if _, err := c.Open(env, AD("instances", "radarr", "apiKey")); err != nil {
		t.Errorf("right ad failed: %v", err)
	}
}

func TestADIsUnambiguous(t *testing.T) {
	if bytes.Equal(AD("a", "bc", "d"), AD("a", "b", "cd")) || bytes.Equal(AD("ab", "", "c"), AD("a", "", "bc")) {
		t.Error("AD parts run together")
	}
}

func TestWrongKey(t *testing.T) {
	a, b := testCipher(t, 1), testCipher(t, 1)
	env, _ := a.Seal([]byte("v"), nil)
	if _, err := b.Open(env, nil); !errors.Is(err, ErrDecrypt) {
		t.Errorf("a different key opened the value: %v", err)
	}
}

func TestNonceUniqueness(t *testing.T) {
	c := testCipher(t, 1)
	seen := map[string]bool{}
	cts := map[string]bool{}
	for range 20_000 {
		env, err := c.Seal([]byte("same plaintext"), []byte("same ad"))
		if err != nil {
			t.Fatal(err)
		}
		n := string(env[5:17])
		if seen[n] {
			t.Fatal("a nonce repeated")
		}
		seen[n] = true
		if cts[string(env[17:])] {
			t.Fatal("the same plaintext sealed to the same ciphertext twice")
		}
		cts[string(env[17:])] = true
	}
}

func TestNewCipherRefusesBadInput(t *testing.T) {
	if _, err := NewCipher(Key{}, 1); err == nil {
		t.Error("the zero key was accepted")
	}
	k, _ := GenerateKey()
	if _, err := NewCipher(k, 0); err == nil {
		t.Error("version 0 was accepted")
	}
	if _, err := NewKey(make([]byte, 32)); err == nil {
		t.Error("an all-zero key was accepted")
	}
	for _, n := range []int{0, 16, 31, 33, 64} {
		b := make([]byte, n)
		_, _ = rand.Read(b)
		if _, err := NewKey(b); err == nil {
			t.Errorf("a %d byte key was accepted", n)
		}
	}
}

func TestReencrypt(t *testing.T) {
	oldC, newC := testCipher(t, 1), testCipher(t, 2)
	var items []Item
	var want [][]byte
	for i, v := range []string{"one", "two", "", "four"} {
		ad := AD("instances", string(rune('a'+i)), "apiKey")
		env, _ := oldC.Seal([]byte(v), ad)
		items = append(items, Item{AD: ad, Envelope: env})
		want = append(want, []byte(v))
	}
	out, err := Reencrypt(oldC, newC, items)
	if err != nil {
		t.Fatal(err)
	}
	for i, env := range out {
		if v, _ := EnvelopeVersion(env); v != 2 {
			t.Errorf("item %d has version %d", i, v)
		}
		got, err := newC.Open(env, items[i].AD)
		if err != nil || !bytes.Equal(got, want[i]) {
			t.Errorf("item %d: %q, %v", i, got, err)
		}
		if bytes.Equal(env, items[i].Envelope) {
			t.Errorf("item %d unchanged", i)
		}
	}

	// All or nothing: one bad item fails the whole set and returns no values.
	items[2].Envelope[len(items[2].Envelope)-1] ^= 1
	out, err = Reencrypt(oldC, newC, items)
	if err == nil || out != nil {
		t.Fatalf("a corrupt item must fail the set, got %v, %v", out, err)
	}
	if !errors.Is(err, ErrDecrypt) {
		t.Errorf("error = %v", err)
	}
	if _, err := Reencrypt(oldC, newC, nil); err != nil {
		t.Errorf("no items: %v", err)
	}
}

func TestSecretsAndKeysRedact(t *testing.T) {
	k, _ := GenerateKey()
	s := NewSecret("canary-plaintext-xyz")
	for _, v := range []any{k, &k, s, &s, struct{ K Key }{k}, struct{ S Secret }{s}} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
			out := sprintf(verb, v)
			if bytes.Contains([]byte(out), []byte("canary-plaintext")) || bytes.Contains([]byte(out), k.b) || bytes.Contains([]byte(out), []byte(sprintf("%x", k.b))) {
				t.Errorf("%s of %T leaked: %q", verb, v, out)
			}
		}
	}
	if s.Reveal() != "canary-plaintext-xyz" || !s.IsSet() || NewSecret("").IsSet() {
		t.Error("Reveal/IsSet broken")
	}
}
