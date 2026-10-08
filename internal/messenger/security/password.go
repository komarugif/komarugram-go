// SPDX-License-Identifier: Unlicense OR MIT

package security

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// sealerPassword names, in the configuration, the root key sealed by the
// master password alone, where there is no TPM: on Haiku, on a computer
// without one, or where it is not given to the user. Whoever copies the
// files can try passwords on their own computer, as fast as Argon2id
// lets them, where a TPM would hold the key and slow the guessing down: a
// long password is the whole protection. Telegram Desktop protects its
// tdata no better with a local passcode (PBKDF2-HMAC-SHA512, 100 000
// iterations), and only formally without one.
const sealerPassword = "password"

// The Argon2id cost where the password is all there is: twice the
// memory, and a pass more, of the TPM's. About 0.2 s on a desktop of 2026,
// 1.6 s on Haiku in a virtual machine.
const (
	passwordArgonTime   = 4
	passwordArgonMemory = 128 * 1024
)

var passwordSealAD = []byte("komarugram-go/password-sealer/v1")

// passwordSealer is the TPM interface without a TPM: the secret is
// encrypted with a key of the authorization, the Argon2id of the password.
// public is the nonce, private the ciphertext with its tag; a wrong
// password fails the tag.
type passwordSealer struct{}

func (passwordSealer) Probe() error { return nil }

func (passwordSealer) Seal(secret, authorization []byte) (public, private []byte, err error) {
	aead, err := passwordAEAD(authorization)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return nonce, aead.Seal(nil, nonce, secret, passwordSealAD), nil
}

func (passwordSealer) Unseal(public, private, authorization []byte) ([]byte, error) {
	aead, err := passwordAEAD(authorization)
	if err != nil {
		return nil, err
	}
	if len(public) != aead.NonceSize() {
		return nil, errors.New("security: invalid sealed key")
	}
	return aead.Open(nil, public, private, passwordSealAD)
}

func passwordAEAD(authorization []byte) (cipher.AEAD, error) {
	key := make([]byte, chacha20poly1305.KeySize)
	defer clear(key)
	if _, err := io.ReadFull(hkdf.New(sha256.New, authorization, nil, passwordSealAD), key); err != nil {
		return nil, err
	}
	return chacha20poly1305.NewX(key)
}
