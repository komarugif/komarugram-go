// SPDX-License-Identifier: Unlicense OR MIT

package sealedfile

import (
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
)

// TagSize is the size of a chunk's tag.
const TagSize = Overhead

// Tagger tags chunks of a file kept as it is, so that a chunk read back can
// be told from one changed, moved, or of another file: a MAC under a key of
// the file's own (GMAC, or Poly1305, as alg seals), its index the nonce.
// Checking a chunk costs that chunk, never the whole file.
type Tagger struct {
	aead cipher.AEAD
}

// NewTagger tags with alg (AESGCM or ChaCha20Poly1305) under key (32 bytes)
// for context, which names the file.
func NewTagger(key []byte, alg Algorithm, context string) (*Tagger, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("sealedfile: a key of %d bytes", len(key))
	}
	tagKey, err := hkdf.Key(sha256.New, key, nil, fmt.Sprintf("komarugram sealedfile tag %d %s", alg, context), 32)
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(alg, tagKey)
	if err != nil {
		return nil, err
	}
	return &Tagger{aead: aead}, nil
}

// Tag is the tag of chunk i, whose content is data; last marks the file's
// last chunk, so that a file cut short is told too.
func (t *Tagger) Tag(i int, data []byte, last bool) [TagSize]byte {
	var nonce [12]byte
	binary.BigEndian.PutUint64(nonce[4:], uint64(i))
	if last {
		nonce[0] = 1
	}
	var tag [TagSize]byte
	t.aead.Seal(tag[:0], nonce[:], nil, data)
	return tag
}

// Check reports whether tag is chunk i's, data its content.
func (t *Tagger) Check(i int, data []byte, last bool, tag []byte) bool {
	want := t.Tag(i, data, last)
	return len(tag) == TagSize && subtle.ConstantTimeCompare(want[:], tag) == 1
}
