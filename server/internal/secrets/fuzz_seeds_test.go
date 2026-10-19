package secrets

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/fuzzseed"
)

// TestWriteFuzzSeeds regenerates testdata/fuzz:
//
//	WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run TestWriteFuzzSeeds ./internal/secrets
func TestWriteFuzzSeeds(t *testing.T) {
	w := fuzzseed.New(t)
	c1, c2 := fuzzCipher(t, 1), fuzzCipher(t, 2)

	ad := AD("instances", "radarr", "secrets")
	sealed := func(c *Cipher, pt string, ad []byte) []byte {
		e, err := c.Seal([]byte(pt), ad)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	good := sealed(c1, "the plaintext", AD("t", "row", "field"))
	flip := func(e []byte, i int) []byte { b := bytes.Clone(e); b[i] ^= 0x01; return b }
	for _, c := range []struct {
		env []byte
		ad  []byte
	}{
		{sealed(c1, "the plaintext", ad), AD("t", "row", "field")},                      // a real envelope of another row
		{sealed(c1, "", AD("t", "row", "field")), AD("t", "row", "other")},              // empty plaintext, minimum size
		{sealed(c2, "the plaintext", AD("t", "row", "field")), AD("t", "row", "field")}, // another key version
		{flip(good, 0), AD("t", "row", "field")},                                        // format byte
		{flip(good, 3), AD("t", "row", "field")},                                        // key version
		{flip(good, 6), AD("t", "row", "field")},                                        // nonce
		{flip(good, 20), AD("t", "row", "field")},                                       // ciphertext
		{flip(good, len(good)-1), AD("t", "row", "field")},                              // tag
		{good[:len(good)-1], AD("t", "row", "field")},                                   // truncated
		{good[:MinEnvelopeLen-1], AD("t", "row", "field")},                              // shorter than the minimum
		{append(bytes.Clone(good), 0), AD("t", "row", "field")},                         // extended
		{good, AD("t", "row", "other")},                                                 // wrong field
		{good, nil},                                                                     // no AD
		{nil, nil}, {[]byte{formatAESGCM}, nil}, {bytes.Repeat([]byte{0}, 64), nil}, {bytes.Repeat([]byte{0xff}, 64), nil},
		{[]byte("plaintext password, not an envelope at all"), nil},
	} {
		w.Add("FuzzOpen", "envelope", c.env, c.ad)
	}

	for _, p := range []string{"", "x", "CANARY-key/+&\"9f8e7d6c", strings.Repeat("k", 1000), "ünï-côdé-🔑", "\x00\x01\x02"} {
		w.Add("FuzzSealOpenTamper", "plaintext", []byte(p), ad, uint16(len(p)*7), uint8(len(p)), true)
		w.Add("FuzzSealOpenTamper", "plaintext-noad", []byte(p), []byte(nil), uint16(0), uint8(3), false)
	}

	for _, c := range [][6]string{
		{"instances", "radarr", "secrets", "instances", "radarr", "secrets"}, {"a", "bc", "d", "ab", "c", "d"}, {"a", "b", "cd", "a", "bc", "d"},
		{"", "", "", "", "", ""}, {"t", "", "f", "", "t", "f"}, {"\x00", "x", "y", "\x00x", "", "y"}, {"a\x00b", "c", "d", "a", "b\x00c", "d"},
	} {
		w.Add("FuzzAD", "triple", c[0], c[1], c[2], c[3], c[4], c[5])
	}

	for _, c := range []struct {
		b     []byte
		loose bool
	}{
		{bytes.Repeat([]byte{0x5a}, 32), false}, {bytes.Repeat([]byte{0x5a}, 31), false}, {bytes.Repeat([]byte{0x5a}, 33), false},
		{bytes.Repeat([]byte{0}, 32), false}, {bytes.Repeat([]byte{0x5a}, 32), true}, {append(bytes.Repeat([]byte{0x5a}, 32), '\n'), false},
		{[]byte("c2VjcmV0LWtleS1zZWNyZXQta2V5LXNlY3JldC1rZXk="), false}, {nil, false}, {[]byte("0123456789abcdef0123456789abcdef"), false},
	} {
		w.Add("FuzzKeyFile", "key", c.b, c.loose)
	}
}
