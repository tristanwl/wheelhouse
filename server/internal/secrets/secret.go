// Package secrets encrypts secret values (provider API keys, passwords) before
// they are stored, and loads the master key that does it.
//
// Values are sealed with AES-256-GCM from the standard library, a fresh random
// nonce each time, and caller-provided associated data that ties a ciphertext to
// the row and field it belongs to (see AD), so a ciphertext copied to another
// row does not open. The output is a versioned envelope (see Cipher.Seal) so the
// algorithm can change later without a flag day.
//
// Nothing here ever logs or prints a key or a plaintext. Go values that hold
// one (Key, Secret) redact themselves under every fmt verb, in slog and in JSON.
package secrets

import (
	"fmt"
	"log/slog"
)

const redacted = "[redacted]"

// Secret is a plaintext secret held in memory (an API key, a password). It
// prints as "[redacted]" with every fmt verb, in slog and in JSON; the only way
// to the value is Reveal, so a leak is a visible call in a code review.
type Secret struct{ v string }

// NewSecret wraps a plaintext value.
func NewSecret(v string) Secret { return Secret{v: v} }

// Reveal returns the plaintext. Pass it straight to the code that needs it
// (an upstream request header, a Seal call) and do not store or log it.
func (s Secret) Reveal() string { return s.v }

// IsSet reports whether the secret is non-empty.
func (s Secret) IsSet() bool { return s.v != "" }

// String implements fmt.Stringer.
func (Secret) String() string { return redacted }

// GoString keeps %#v from printing the value.
func (Secret) GoString() string { return redacted }

// Format redacts every fmt verb, including %x, %q and %+v.
func (Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(redacted)) }

// LogValue keeps slog from recording the value.
func (Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalJSON keeps the value out of any JSON body.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// MarshalText keeps the value out of text encodings (maps keyed by it, YAML).
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
