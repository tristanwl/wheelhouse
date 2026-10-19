package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
)

// KeySize is the master key length in bytes (AES-256).
const KeySize = 32

// Envelope layout, format 1 (all of it is stored as one opaque value):
//
//	offset  size  field
//	0       1     format: 0x01 = AES-256-GCM, 12-byte nonce, 16-byte tag
//	1       4     key version, big endian
//	5       12    nonce, random per value
//	17      n+16  ciphertext followed by the GCM tag
//
// The GCM additional data is the 5 header bytes followed by the caller's
// associated data (AD), so neither the format nor the key version can be
// changed without the tag failing. A new algorithm gets a new format byte; old
// formats stay readable for as long as the code keeps them.
const (
	formatAESGCM = 0x01
	headerLen    = 5
	nonceLen     = 12
	tagLen       = 16

	// MinEnvelopeLen is the size of the envelope of an empty plaintext.
	MinEnvelopeLen = headerLen + nonceLen + tagLen
)

// Errors are static text: they never contain a key, a plaintext or ciphertext.
var (
	// ErrMalformed means the value is not an envelope this code understands.
	ErrMalformed = errors.New("secrets: not a valid encrypted value")
	// ErrKeyVersion means the value was sealed under another key version than
	// the cipher's (a rotation did not finish, or the wrong key file is in use).
	ErrKeyVersion = errors.New("secrets: encrypted with a different key version")
	// ErrDecrypt means authentication failed: the wrong key, the wrong
	// associated data (the value belongs to another row or field), or a value
	// that was modified.
	ErrDecrypt = errors.New("secrets: cannot decrypt (wrong key, wrong row or field, or the value was modified)")
)

// Cipher seals and opens values under one master key of one version. It is safe
// for concurrent use.
type Cipher struct {
	version uint32
	aead    cipher.AEAD
}

// NewCipher returns a Cipher for key, stamping envelopes with version (the first
// key is version 1).
func NewCipher(key Key, version uint32) (*Cipher, error) {
	if len(key.b) != KeySize {
		return nil, fmt.Errorf("secrets: the key must be exactly %d bytes", KeySize)
	}
	if version == 0 {
		return nil, errors.New("secrets: key versions start at 1")
	}
	block, err := aes.NewCipher(key.b)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{version: version, aead: aead}, nil
}

// Version is the key version stamped on every envelope this Cipher seals.
func (c *Cipher) Version() uint32 { return c.version }

// Seal encrypts plaintext. ad is the associated data: it is authenticated but
// not stored, so it must be the same bytes at Open (build it with AD from the
// table, row id and field). A fresh random nonce is drawn for every call.
func (c *Cipher) Seal(plaintext, ad []byte) ([]byte, error) {
	out := make([]byte, headerLen+nonceLen, headerLen+nonceLen+len(plaintext)+tagLen)
	out[0] = formatAESGCM
	binary.BigEndian.PutUint32(out[1:headerLen], c.version)
	nonce := out[headerLen : headerLen+nonceLen]
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secrets: random source failed: %w", err)
	}
	return c.aead.Seal(out, nonce, plaintext, aad(out[:headerLen], ad)), nil
}

// Open decrypts an envelope made by Seal with the same ad.
func (c *Cipher) Open(envelope, ad []byte) ([]byte, error) {
	version, err := EnvelopeVersion(envelope)
	if err != nil {
		return nil, err
	}
	if version != c.version {
		return nil, ErrKeyVersion
	}
	nonce := envelope[headerLen : headerLen+nonceLen]
	pt, err := c.aead.Open(nil, nonce, envelope[headerLen+nonceLen:], aad(envelope[:headerLen], ad))
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// EnvelopeVersion returns the key version an envelope was sealed under without
// decrypting it.
func EnvelopeVersion(envelope []byte) (uint32, error) {
	if len(envelope) < MinEnvelopeLen || envelope[0] != formatAESGCM {
		return 0, ErrMalformed
	}
	return binary.BigEndian.Uint32(envelope[1:headerLen]), nil
}

func aad(header, ad []byte) []byte {
	out := make([]byte, 0, len(header)+len(ad))
	return append(append(out, header...), ad...)
}

// AD builds the associated data for a value stored in table, in the row with
// the given id, in field. Every part is length-prefixed, so ("a","bc","d") and
// ("a","b","cd") differ. Use the same three parts when sealing and opening.
func AD(table, id, field string) []byte {
	out := make([]byte, 0, 12+len(table)+len(id)+len(field))
	for _, part := range []string{table, id, field} {
		out = binary.BigEndian.AppendUint32(out, uint32(len(part))) //nolint:gosec // a table or row id is never 4 GiB
		out = append(out, part...)
	}
	return out
}

// Item is one stored value to rotate: its envelope and the associated data it
// was sealed with.
type Item struct {
	AD       []byte
	Envelope []byte
}

// Reencrypt opens every item with from and seals the plaintext again with to
// (fresh nonces). It is all or nothing: if any item fails, nothing is returned,
// so the caller writes the new values only when every one is ready, in one
// database transaction. The error names the failing position, never a value.
func Reencrypt(from, to *Cipher, items []Item) ([][]byte, error) {
	out := make([][]byte, len(items))
	for i, it := range items {
		pt, err := from.Open(it.Envelope, it.AD)
		if err != nil {
			return nil, fmt.Errorf("value %d of %d: %w", i+1, len(items), err)
		}
		out[i], err = to.Seal(pt, it.AD)
		clear(pt)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
