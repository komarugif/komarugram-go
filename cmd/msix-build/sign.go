// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"slices"
	"time"
)

// The signature part of a package, AppxSignature.p7x, is "PKCX" and a
// PKCS #7 SignedData of the kind Authenticode makes, whose content tells
// the hashes of the package's parts (SpcIndirectDataContent with the
// SpcSipInfo of the package format). It is written here, with the
// standard library, so that building a package needs no SDK.
var (
	oidSignedData     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidSHA256         = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidRSA            = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidContentType    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDigest  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidSigningTime    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 5}
	oidIndirectData   = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 2, 1, 4}
	oidSipInfo        = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 2, 1, 30}
	oidOpusInfo       = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 2, 1, 12}
	oidStatementType  = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 2, 1, 11}
	oidIndividualCode = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 2, 1, 21}
)

// sipGUID is {0AC5DF4B-CE07-4DE2-B76E-23C839A09FD1}, the package format's
// subject interface package, as bytes of a GUID.
var sipGUID = []byte{0x4B, 0xDF, 0xC5, 0x0A, 0x07, 0xCE, 0xE2, 0x4D, 0xB7, 0x6E, 0x23, 0xC8, 0x39, 0xA0, 0x9F, 0xD1}

// sipInfo is SpcSipInfo.
type sipInfo struct {
	Version int
	GUID    []byte
	A, B, C int
	D, E    int
}

type algorithm struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue
}

type indirectData struct {
	Data struct {
		Type  asn1.ObjectIdentifier
		Value sipInfo
	}
	Digest struct {
		Algorithm algorithm
		Digest    []byte
	}
}

// der builds a constructed value of a tag from encoded parts.
func der(tag byte, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	out := []byte{tag}
	switch n := len(body); {
	case n < 0x80:
		out = append(out, byte(n))
	case n < 0x100:
		out = append(out, 0x81, byte(n))
	case n < 0x10000:
		out = append(out, 0x82, byte(n>>8), byte(n))
	default:
		out = append(out, 0x83, byte(n>>16), byte(n>>8), byte(n))
	}
	return append(out, body...)
}

func marshal(v any) []byte {
	b, err := asn1.Marshal(v)
	if err != nil {
		// Everything marshaled here is of fixed types.
		panic(err)
	}
	return b
}

func sequence(parts ...[]byte) []byte { return der(0x30, parts...) }
func set(parts ...[]byte) []byte      { return der(0x31, parts...) }

// attribute is an authenticated attribute: its type and one value.
func attribute(oid asn1.ObjectIdentifier, value []byte) []byte {
	return sequence(marshal(oid), set(value))
}

func sha256Algorithm() algorithm {
	return algorithm{oidSHA256, asn1.NullRawValue}
}

// contentOf is the digest's content for the hashes of a package: "APPX"
// and the four hashes, each after its name.
func contentOf(h packageHashes) []byte {
	var b bytes.Buffer
	b.WriteString("APPX")
	for _, p := range []struct {
		name string
		sum  [32]byte
	}{{"AXPC", h.Files}, {"AXCD", h.Directory}, {"AXCT", h.ContentTypes}, {"AXBM", h.BlockMap}} {
		b.WriteString(p.name)
		b.Write(p.sum[:])
	}
	return b.Bytes()
}

// signRSA is a signer that signs with an RSA key. chain starts with the
// certificate of the key, and goes on with those that vouch for it, which
// the signature carries.
func signRSA(chain []*x509.Certificate, key *rsa.PrivateKey, now time.Time) signer {
	return func(h packageHashes) ([]byte, error) {
		if len(chain) == 0 {
			return nil, errors.New("no certificate to sign with")
		}
		leaf := chain[0]
		if pub, ok := leaf.PublicKey.(*rsa.PublicKey); !ok || !pub.Equal(&key.PublicKey) {
			return nil, errors.New("the certificate is not the key's")
		}
		var info indirectData
		info.Data.Type = oidSipInfo
		info.Data.Value = sipInfo{Version: 0x01010000, GUID: sipGUID}
		info.Digest.Algorithm = sha256Algorithm()
		info.Digest.Digest = contentOf(h)
		indirect := marshal(info)
		// The signed digest is of the content without its own tag and
		// length, as Authenticode does it.
		_, header := derHeader(indirect)
		contentDigest := sha256.Sum256(indirect[header:])

		attrs := [][]byte{
			attribute(oidContentType, marshal(oidIndirectData)),
			attribute(oidSigningTime, marshal(now.UTC())),
			attribute(oidMessageDigest, marshal(contentDigest[:])),
			attribute(oidOpusInfo, sequence()),
			attribute(oidStatementType, sequence(marshal(oidIndividualCode))),
		}
		// A SET OF is in the order of its encodings.
		slices.SortFunc(attrs, bytes.Compare)
		toSign := set(attrs...)
		sum := sha256.Sum256(toSign)
		sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, sum[:])
		if err != nil {
			return nil, err
		}
		signerInfo := sequence(
			marshal(1),
			sequence(leaf.RawIssuer, marshal(leaf.SerialNumber)),
			marshal(sha256Algorithm()),
			der(0xA0, attrs...),
			marshal(algorithm{oidRSA, asn1.NullRawValue}),
			marshal(sig),
		)
		var certs [][]byte
		for _, c := range chain {
			certs = append(certs, c.Raw)
		}
		signedData := sequence(
			marshal(1),
			set(marshal(sha256Algorithm())),
			sequence(marshal(oidIndirectData), der(0xA0, indirect)),
			der(0xA0, certs...),
			set(signerInfo),
		)
		out := []byte("PKCX")
		return append(out, sequence(marshal(oidSignedData), der(0xA0, signedData))...), nil
	}
}

// derHeader is the length of the tag and the length of a DER value.
func derHeader(b []byte) (tag byte, header int) {
	if len(b) < 2 {
		return 0, len(b)
	}
	if b[1] < 0x80 {
		return b[0], 2
	}
	return b[0], 2 + int(b[1]&0x7f)
}
