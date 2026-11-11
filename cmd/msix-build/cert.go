// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// defaultPublisher is the subject of the self-signed certificate, and the
// publisher the package names: the two must be the same text.
const defaultPublisher = "CN=KomaruGram"

// identity is what signs a package: a certificate (and the ones that
// vouch for it) with its key.
type identity struct {
	Chain []*x509.Certificate
	Key   *rsa.PrivateKey
}

// publisher is the certificate's subject as the manifest names the
// publisher. The system compares the two as text.
func (id identity) publisher() string { return id.Chain[0].Subject.String() }

// newSelfSigned makes a certificate for signing code that vouches for
// itself: it is a leaf, not a CA, so trusting it trusts nothing else.
func newSelfSigned(subject string, now time.Time) (identity, error) {
	name, err := parseSubject(subject)
	if err != nil {
		return identity{}, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return identity{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return identity{}, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               name,
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return identity{}, err
	}
	cert, err := x509.ParseCertificate(raw)
	if err != nil {
		return identity{}, err
	}
	return identity{Chain: []*x509.Certificate{cert}, Key: key}, nil
}

// parseSubject reads the subjects this tool makes: "CN=name" and, in the
// same form, "CN=name, O=organization".
func parseSubject(subject string) (pkix.Name, error) {
	var name pkix.Name
	for _, part := range strings.Split(subject, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || value == "" {
			return name, fmt.Errorf("publisher %q: expected attributes like CN=Name", subject)
		}
		switch key {
		case "CN":
			name.CommonName = value
		case "O":
			name.Organization = append(name.Organization, value)
		case "OU":
			name.OrganizationalUnit = append(name.OrganizationalUnit, value)
		case "L":
			name.Locality = append(name.Locality, value)
		case "C":
			name.Country = append(name.Country, value)
		case "S", "ST":
			name.Province = append(name.Province, value)
		default:
			return name, fmt.Errorf("publisher %q: unsupported attribute %s", subject, key)
		}
	}
	if name.CommonName == "" {
		return name, fmt.Errorf("publisher %q has no CN", subject)
	}
	return name, nil
}

// Files of the self-signed certificate in the signing directory.
const (
	certFile = "komarugram-selfsigned.pem"
	keyFile  = "komarugram-selfsigned.key"
)

// loadOrCreate returns the self-signed identity kept in dir, making and
// keeping it on the first run. A kept certificate is used as long as it
// has a month to live and its subject is the publisher wanted.
func loadOrCreate(dir, publisher string, now time.Time) (id identity, created bool, err error) {
	certPath, keyPath := filepath.Join(dir, certFile), filepath.Join(dir, keyFile)
	if _, statErr := os.Stat(certPath); statErr == nil {
		id, err = loadIdentity(certPath, keyPath)
		if err != nil {
			return identity{}, false, err
		}
		if got := id.publisher(); got != publisher {
			return identity{}, false, fmt.Errorf("the certificate kept in %s is for %q, not for %q: remove its files to make another", dir, got, publisher)
		}
		if left := id.Chain[0].NotAfter.Sub(now); left < 30*24*time.Hour {
			return identity{}, false, fmt.Errorf("the certificate kept in %s expires on %s: remove its files to make a new one (and trust the new one)", dir, id.Chain[0].NotAfter.Format(time.DateOnly))
		}
		return id, false, nil
	}
	id, err = newSelfSigned(publisher, now)
	if err != nil {
		return identity{}, false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return identity{}, false, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(id.Key)
	if err != nil {
		return identity{}, false, err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return identity{}, false, err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: id.Chain[0].Raw}), 0o644); err != nil {
		return identity{}, false, err
	}
	return id, true, nil
}

// loadIdentity reads a certificate chain (the certificate of the key
// first) and its RSA key, both PEM.
func loadIdentity(certPath, keyPath string) (identity, error) {
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return identity{}, err
	}
	var chain []*x509.Certificate
	for {
		var block *pem.Block
		block, certData = pem.Decode(certData)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return identity{}, fmt.Errorf("%s: %w", certPath, err)
		}
		chain = append(chain, cert)
	}
	if len(chain) == 0 {
		return identity{}, fmt.Errorf("%s: no PEM certificate", certPath)
	}
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return identity{}, err
	}
	block, _ := pem.Decode(keyData)
	if block == nil {
		return identity{}, fmt.Errorf("%s: no PEM key", keyPath)
	}
	var key *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		var k any
		k, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		if rk, ok := k.(*rsa.PrivateKey); ok {
			key = rk
		} else if err == nil {
			err = errors.New("only RSA keys sign packages here")
		}
	default:
		err = fmt.Errorf("unsupported PEM type %q (an unencrypted RSA key is needed)", block.Type)
	}
	if err != nil {
		return identity{}, fmt.Errorf("%s: %w", keyPath, err)
	}
	return identity{Chain: chain, Key: key}, nil
}
