package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fuzzCipher(tb testing.TB, version uint32) *Cipher {
	tb.Helper()
	raw := bytes.Repeat([]byte{0x5a}, KeySize)
	raw[0] = byte(version)
	key, err := NewKey(raw)
	if err != nil {
		tb.Fatal(err)
	}
	c, err := NewCipher(key, version)
	if err != nil {
		tb.Fatal(err)
	}
	return c
}

// FuzzOpen: the envelope decoder meets arbitrary bytes.
//
// Invariants: it never panics; anything it did not seal itself fails with one of
// the three static errors (malformed, wrong key version, cannot decrypt), whose
// text names no key, plaintext or ciphertext; EnvelopeVersion agrees with Open on
// what is malformed; and, for the one real envelope the fixture holds, no
// mutation of the bytes opens.
func FuzzOpen(f *testing.F) {
	c := fuzzCipher(f, 1)
	real, err := c.Seal([]byte("the plaintext"), AD("t", "row", "field"))
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, envelope, ad []byte) {
		pt, err := c.Open(envelope, ad)
		if err == nil {
			// Only a genuine envelope under this key and these AD opens.
			if !bytes.Equal(envelope, real) || !bytes.Equal(ad, AD("t", "row", "field")) || string(pt) != "the plaintext" {
				t.Fatalf("opened %x with ad %x to %q", envelope, ad, pt)
			}
			return
		}
		if !errors.Is(err, ErrMalformed) && !errors.Is(err, ErrKeyVersion) && !errors.Is(err, ErrDecrypt) {
			t.Fatalf("unexpected error %v", err)
		}
		msg := err.Error()
		if len(msg) > 200 || (len(envelope) >= 4 && bytes.Contains([]byte(msg), envelope)) {
			t.Fatalf("error text %q", msg)
		}
		if v, verr := EnvelopeVersion(envelope); (verr != nil) != errors.Is(err, ErrMalformed) {
			t.Fatalf("EnvelopeVersion(%x) = %d, %v but Open says %v", envelope, v, verr, err)
		}
		if pt != nil {
			t.Fatalf("a failed open returned %q", pt)
		}
	})
}

// FuzzSealOpenTamper: a sealed value always opens to what went in, and any
// change to it, to its associated data, or to the cipher fails.
//
// Invariants: Seal then Open round-trips for every plaintext and AD; two seals
// of one plaintext differ (a fresh nonce); the envelope never contains the
// plaintext; flipping any single bit of the envelope (header, nonce, ciphertext
// or tag), cutting or extending it, changing one byte of the AD, or opening
// under another key version or another key, always fails; and Reencrypt moves a
// value to the new key without ever returning the old envelope.
func FuzzSealOpenTamper(f *testing.F) {
	c := fuzzCipher(f, 1)
	other := fuzzCipher(f, 2)
	f.Fuzz(func(t *testing.T, plaintext, ad []byte, pos uint16, bit uint8, tamperAD bool) {
		env, err := c.Seal(plaintext, ad)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Open(env, ad)
		if err != nil || !bytes.Equal(got, plaintext) {
			t.Fatalf("round trip: %q, %v", got, err)
		}
		if len(plaintext) >= 8 && bytes.Contains(env, plaintext) {
			t.Fatal("the plaintext is visible in the envelope")
		}
		env2, _ := c.Seal(plaintext, ad)
		if bytes.Equal(env, env2) {
			t.Fatal("two seals are identical")
		}

		bad := bytes.Clone(env)
		bad[int(pos)%len(bad)] ^= 1 << (bit % 8)
		if _, err := c.Open(bad, ad); err == nil {
			t.Fatalf("a flipped bit at %d opened", int(pos)%len(bad))
		}
		if _, err := c.Open(env[:int(pos)%len(env)], ad); err == nil {
			t.Fatal("a truncated envelope opened")
		}
		if _, err := c.Open(append(bytes.Clone(env), byte(pos)), ad); err == nil {
			t.Fatal("an extended envelope opened")
		}
		if tamperAD {
			badAD := append(bytes.Clone(ad), 0)
			if len(ad) > 0 {
				badAD = bytes.Clone(ad)
				badAD[int(pos)%len(badAD)] ^= 1 << (bit % 8)
			}
			if _, err := c.Open(env, badAD); err == nil {
				t.Fatal("opened with other associated data")
			}
		}
		if _, err := other.Open(env, ad); err == nil {
			t.Fatal("opened under another key")
		}
		moved, err := Reencrypt(c, other, []Item{{AD: ad, Envelope: env}})
		if err != nil || len(moved) != 1 || bytes.Equal(moved[0], env) {
			t.Fatalf("Reencrypt: %v", err)
		}
		if back, err := other.Open(moved[0], ad); err != nil || !bytes.Equal(back, plaintext) {
			t.Fatalf("re-encrypted value does not open: %v", err)
		}
		if _, err := Reencrypt(c, other, []Item{{AD: ad, Envelope: bad}}); err == nil {
			t.Fatal("Reencrypt accepted a damaged envelope")
		} else if strings.Contains(err.Error(), string(plaintext)) && len(plaintext) >= 4 {
			t.Fatalf("Reencrypt error repeats the plaintext: %v", err)
		}
	})
}

// FuzzAD: the associated data of a stored value.
//
// Invariants: it is injective in (table, id, field): two different triples never
// give the same bytes (the parts are length-prefixed, so "a|bc" and "ab|c" cannot
// collide).
func FuzzAD(f *testing.F) {
	f.Fuzz(func(t *testing.T, a1, b1, c1, a2, b2, c2 string) {
		same := a1 == a2 && b1 == b2 && c1 == c2
		if bytes.Equal(AD(a1, b1, c1), AD(a2, b2, c2)) != same {
			t.Fatalf("AD(%q,%q,%q) vs AD(%q,%q,%q)", a1, b1, c1, a2, b2, c2)
		}
	})
}

// FuzzKeyFile: what a key file may hold.
//
// Invariants: a file is a key if and only if it has exactly 32 bytes that are
// not all zero and mode that gives group and others nothing; a refusal is an
// error that does not contain the file's bytes; the key never prints.
func FuzzKeyFile(f *testing.F) {
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, content []byte, loose bool) {
		path := filepath.Join(dir, "key")
		mode := os.FileMode(0o600)
		if loose {
			mode = 0o644
		}
		if err := os.WriteFile(path, content, mode); err != nil {
			t.Skip()
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Skip()
		}
		k, err := ReadKeyFile(path)
		want := len(content) == KeySize && !bytes.Equal(content, make([]byte, KeySize)) && !loose
		if (err == nil) != want {
			t.Fatalf("ReadKeyFile(%d bytes, loose=%v) = %v, want ok=%v", len(content), loose, err, want)
		}
		if err != nil {
			if len(content) >= 8 && strings.Contains(err.Error(), string(content)) {
				t.Fatalf("the error repeats the file: %v", err)
			}
			return
		}
		for _, s := range []string{fmt.Sprint(k), fmt.Sprintf("%v %+v %#v %s %x", k, k, k, k, k)} {
			if strings.Contains(s, string(content)) || !strings.Contains(s, "redacted") {
				t.Fatalf("the key prints as %q", s)
			}
		}
	})
}
