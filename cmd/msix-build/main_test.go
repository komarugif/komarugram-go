// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// parseSignature is the inverse of signRSA: it returns the hashes the
// signature covers, and verifies the signature against the certificate it
// carries.
func parseSignature(p7x []byte) (packageHashes, *x509.Certificate, error) {
	var h packageHashes
	if !bytes.HasPrefix(p7x, []byte("PKCX")) {
		return h, nil, errors.New("signature does not start with PKCX")
	}
	var outer struct {
		Type    asn1.ObjectIdentifier
		Content asn1.RawValue `asn1:"explicit,tag:0"`
	}
	if _, err := asn1.Unmarshal(p7x[4:], &outer); err != nil {
		return h, nil, err
	}
	var signed struct {
		Version int
		Digests asn1.RawValue
		Content struct {
			Type asn1.ObjectIdentifier
			Data asn1.RawValue `asn1:"explicit,tag:0"`
		}
		Certs   asn1.RawValue `asn1:"tag:0"`
		Signers asn1.RawValue
	}
	if _, err := asn1.Unmarshal(outer.Content.Bytes, &signed); err != nil {
		return h, nil, err
	}
	certs, err := x509.ParseCertificates(signed.Certs.Bytes)
	if err != nil || len(certs) == 0 {
		return h, nil, fmt.Errorf("certificates: %v", err)
	}
	var info indirectData
	if _, err := asn1.Unmarshal(signed.Content.Data.Bytes, &info); err != nil {
		return h, nil, err
	}
	content := info.Digest.Digest
	if len(content) != 4+4*36 || string(content[:4]) != "APPX" {
		return h, nil, errors.New("unexpected digest content")
	}
	for i, dst := range []*[32]byte{&h.Files, &h.Directory, &h.ContentTypes, &h.BlockMap} {
		at := 4 + i*36
		copy(dst[:], content[at+4:at+36])
	}
	var signer struct {
		Version int
		Issuer  asn1.RawValue
		Digest  algorithm
		Attrs   asn1.RawValue `asn1:"tag:0"`
		SigAlg  algorithm
		Sig     []byte
	}
	var signerSet asn1.RawValue
	if _, err := asn1.Unmarshal(signed.Signers.FullBytes, &signerSet); err != nil {
		return h, nil, err
	}
	if _, err := asn1.Unmarshal(signerSet.Bytes, &signer); err != nil {
		return h, nil, err
	}
	// The signature is over the attributes as a SET.
	attrs := append([]byte{0x31}, signer.Attrs.FullBytes[1:]...)
	sum := sha256.Sum256(attrs)
	pub, ok := certs[0].PublicKey.(*rsa.PublicKey)
	if !ok {
		return h, nil, errors.New("not an RSA key")
	}
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], signer.Sig); err != nil {
		return h, nil, fmt.Errorf("signature: %w", err)
	}
	// The attributes must carry the digest of the content, which is taken
	// without the content's own tag and length.
	_, header := derHeader(signed.Content.Data.Bytes)
	want := sha256.Sum256(signed.Content.Data.Bytes[header:])
	if !bytes.Contains(signer.Attrs.Bytes, want[:]) {
		return h, nil, errors.New("the signed attributes do not hold the content's digest")
	}
	return h, certs[0], nil
}

func testIdentity(t *testing.T) identity {
	t.Helper()
	id, _, err := loadOrCreate(t.TempDir(), defaultPublisher, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func testPackage(t *testing.T, id identity) ([]byte, []part) {
	t.Helper()
	payload := []part{
		{"KomaruGram.exe", bytes.Repeat([]byte("MZ"), 70_000)}, // 140 000 bytes: three blocks
		{"Assets/empty.png", nil},
		{"Assets/a.png", []byte("png")},
	}
	xml, err := manifest(manifestInfo{Name: "KomaruGram", Publisher: id.publisher(), PublisherDisplay: "KomaruGram", Version: "1.2.3.0", Arch: "x64"})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := buildPackage(xml, payload, signRSA(id.Chain, id.Key, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	return pkg, payload
}

func readZip(t *testing.T, pkg []byte) map[string][]byte {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(pkg), int64(len(pkg)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc) // the reader checks the CRC
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		out[f.Name] = data
	}
	return out
}

func TestManifestPublisherIsTheCertificatesSubject(t *testing.T) {
	id := testIdentity(t)
	pkg, _ := testPackage(t, id)
	data := readZip(t, pkg)[manifestName]
	var m struct {
		Identity struct {
			Publisher string `xml:"Publisher,attr"`
			Version   string `xml:"Version,attr"`
			Arch      string `xml:"ProcessorArchitecture,attr"`
		}
	}
	if err := xml.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.Identity.Publisher != id.Chain[0].Subject.String() || m.Identity.Publisher != "CN=KomaruGram" {
		t.Errorf("publisher %q, certificate %q", m.Identity.Publisher, id.Chain[0].Subject)
	}
	if m.Identity.Version != "1.2.3.0" || m.Identity.Arch != "x64" {
		t.Errorf("identity %+v", m.Identity)
	}
}

// The data of the client must stay in the real profile.
func TestManifestOptsOutOfVirtualization(t *testing.T) {
	data, err := manifest(manifestInfo{Name: "KomaruGram", Publisher: "CN=A & B", PublisherDisplay: "A", Version: "1.0.0.0", Arch: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`unvirtualizedResources`,
		`<desktop6:FileSystemWriteVirtualization>disabled<`,
		`<desktop6:RegistryWriteVirtualization>disabled<`,
		`runFullTrust`,
		`MinVersion="10.0.19041.0"`,
		`Publisher="CN=A &amp; B"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("manifest lacks %s", want)
		}
	}
	if err := xml.Unmarshal(data, new(struct{})); err != nil {
		t.Errorf("not well-formed: %v", err)
	}
}

func TestBlockMapHashesEachBlock(t *testing.T) {
	big := bytes.Repeat([]byte{7}, 2*blockSize+10)
	data := blockMap([]part{{"Assets/big.bin", big}, {"empty", nil}})
	var m struct {
		Files []struct {
			Name   string `xml:"Name,attr"`
			Size   int    `xml:"Size,attr"`
			Lfh    int    `xml:"LfhSize,attr"`
			Hashes []struct {
				Hash string `xml:"Hash,attr"`
			} `xml:"Block"`
		} `xml:"File"`
	}
	if err := xml.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 2 {
		t.Fatalf("files: %+v", m.Files)
	}
	f := m.Files[0]
	if f.Name != `Assets\big.bin` || f.Size != len(big) || f.Lfh != 30+len("Assets/big.bin") || len(f.Hashes) != 3 {
		t.Fatalf("file: %+v", f)
	}
	for i, want := range [][]byte{big[:blockSize], big[blockSize : 2*blockSize], big[2*blockSize:]} {
		sum := sha256.Sum256(want)
		if f.Hashes[i].Hash != base64.StdEncoding.EncodeToString(sum[:]) {
			t.Errorf("block %d: wrong hash", i)
		}
	}
	if len(m.Files[1].Hashes) != 0 {
		t.Error("an empty file has blocks")
	}
}

func TestPackageLayout(t *testing.T) {
	pkg, payload := testPackage(t, testIdentity(t))
	files := readZip(t, pkg)
	for _, p := range payload {
		if !bytes.Equal(files[p.Name], p.Data) {
			t.Errorf("%s differs", p.Name)
		}
	}
	for _, name := range []string{manifestName, blockMapName, contentTypesName, signatureName} {
		if _, ok := files[name]; !ok {
			t.Errorf("no %s", name)
		}
	}
	// The block map's header sizes are the real ones: every entry's data
	// begins after the entries before it and its own header.
	r, _ := zip.NewReader(bytes.NewReader(pkg), int64(len(pkg)))
	var end int64
	for _, f := range r.File {
		off, err := f.DataOffset()
		if err != nil {
			t.Fatal(err)
		}
		if off != end+int64(lfhSize(f.Name)) {
			t.Errorf("%s: data at %d, header predicted to end at %d", f.Name, off, end+int64(lfhSize(f.Name)))
		}
		end = off + int64(f.CompressedSize64)
	}
	if r.File[len(r.File)-1].Name != signatureName {
		t.Error("the signature is not the last entry")
	}
	// Nothing is compressed: the block map has no sizes of compressed blocks.
	for _, f := range r.File {
		if f.Method != zip.Store {
			t.Errorf("%s is compressed", f.Name)
		}
	}
}

func TestPackageIsReproducible(t *testing.T) {
	payload := []part{{"a.bin", []byte("x")}}
	a, _ := buildPackage([]byte("<m/>"), payload, nil)
	b, _ := buildPackage([]byte("<m/>"), payload, nil)
	if !bytes.Equal(a, b) {
		t.Error("two packages of the same files differ")
	}
	if _, ok := readZip(t, a)[signatureName]; ok {
		t.Error("an unsigned package has a signature")
	}
}

// The signature covers what the platform recomputes from the package
// without the signature entry.
func TestSignatureCoversThePackage(t *testing.T) {
	id := testIdentity(t)
	pkg, _ := testPackage(t, id)
	files := readZip(t, pkg)
	hashes, cert, err := parseSignature(files[signatureName])
	if err != nil {
		t.Fatal(err)
	}
	if !cert.Equal(id.Chain[0]) {
		t.Error("the signature carries another certificate")
	}
	r, _ := zip.NewReader(bytes.NewReader(pkg), int64(len(pkg)))
	sig := r.File[len(r.File)-1]
	z64 := len(pkg) - (56 + 20 + 22) // the ZIP64 end record
	cdOffset := int(binary.LittleEndian.Uint64(pkg[z64+48:]))
	cd := pkg[cdOffset:z64]
	// The directory without the signature's record, with the end of the ZIP
	// as it is without the signature.
	cd = cd[:len(cd)-(46+len(sig.Name))]
	// The files without the signature's entry.
	sigStart := cdOffset - (lfhSize(signatureName) + len(files[signatureName]))
	cd = append(append([]byte{}, cd...), endRecords(len(r.File)-1, len(cd), sigStart)...)
	want := packageHashes{
		Files:        sha256.Sum256(pkg[:sigStart]),
		Directory:    sha256.Sum256(cd),
		ContentTypes: sha256.Sum256(files[contentTypesName]),
		BlockMap:     sha256.Sum256(files[blockMapName]),
	}
	if hashes != want {
		t.Errorf("signed hashes %x, package's %x", hashes, want)
	}
	// A changed file is no longer what was signed.
	files[blockMapName][10] ^= 1
	if sha256.Sum256(files[blockMapName]) == hashes.BlockMap {
		t.Error("the block map's hash did not change")
	}
}

func TestSignatureIsRefusedWhenTampered(t *testing.T) {
	id := testIdentity(t)
	pkg, _ := testPackage(t, id)
	sig := readZip(t, pkg)[signatureName]
	// The signed attributes are inside: flip a byte of the signing time.
	i := bytes.LastIndex(sig, []byte{0x17, 0x0d}) // UTCTime of 13 bytes
	if i < 0 {
		t.Fatal("no signing time")
	}
	sig[i+4] ^= 1
	if _, _, err := parseSignature(sig); err == nil {
		t.Error("a changed signature was accepted")
	}
}

func TestSelfSignedCertificate(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	id, created, err := loadOrCreate(dir, defaultPublisher, now)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	c := id.Chain[0]
	if c.IsCA || len(c.ExtKeyUsage) != 1 || c.ExtKeyUsage[0] != x509.ExtKeyUsageCodeSigning || c.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Errorf("not a code signing leaf: %+v", c)
	}
	again, created, err := loadOrCreate(dir, defaultPublisher, now)
	if err != nil || created || !again.Chain[0].Equal(c) {
		t.Errorf("the kept certificate was not reused: created=%v err=%v", created, err)
	}
	if _, _, err := loadOrCreate(dir, "CN=Other", now); err == nil {
		t.Error("a kept certificate was used for another publisher")
	}
	if _, _, err := loadOrCreate(dir, defaultPublisher, now.AddDate(6, 0, 0)); err == nil {
		t.Error("an expired certificate was used")
	}
	if _, err := os.Stat(filepath.Join(dir, keyFile)); err != nil {
		t.Error(err)
	}
}

func TestSignerRefusesAnotherKey(t *testing.T) {
	a, b := testIdentity(t), testIdentity(t)
	_, err := buildPackage([]byte("<m/>"), nil, signRSA(a.Chain, b.Key, time.Now()))
	if err == nil {
		t.Error("signed with a key of another certificate")
	}
}

func TestVersion(t *testing.T) {
	for v, ok := range map[string]bool{"1.2.3.4": true, "0.0.1.0": true, "65535.0.0.0": true, "65536.0.0.0": false, "1.2.3": false, "a.b.c.d": false} {
		if (validVersion(v) == nil) != ok {
			t.Errorf("validVersion(%q) = %v", v, validVersion(v))
		}
	}
}

// The whole build, with an executable given in place of building one.
func TestBuildWithGivenExecutable(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "KomaruGram.exe")
	if err := os.WriteFile(exe, []byte("MZ fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out", "p.msix")
	err := build(options{out: out, arch: "arm64", exe: exe, version: "1.0.0.0", name: "KomaruGram", display: "KomaruGram", signingDir: filepath.Join(dir, "keys")})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	files := readZip(t, pkg)
	if string(files["KomaruGram.exe"]) != "MZ fake" {
		t.Error("the executable is not in the package")
	}
	cer, err := os.ReadFile(filepath.Join(dir, "out", "KomaruGram.cer"))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(cer)
	if err != nil {
		t.Fatal(err)
	}
	_, signedBy, err := parseSignature(files[signatureName])
	if err != nil || !signedBy.Equal(cert) {
		t.Errorf("the .cer is not the signing certificate: %v", err)
	}
	if !strings.Contains(string(files[manifestName]), `ProcessorArchitecture="arm64"`) {
		t.Error("architecture")
	}
}

// The system hashes the directory with its records as the packaging tool
// writes them, whose version made by is 45: with another value it takes the
// signature for a changed package (checked with the system's own verifier).
func TestRecordsAreWrittenAsThePackagingToolDoes(t *testing.T) {
	var buf bytes.Buffer
	rec := centralRecord(writeEntry(&buf, "a", []byte("x")))
	if got := binary.LittleEndian.Uint16(rec[4:]); got != 45 {
		t.Errorf("version made by %d", got)
	}
	end := endRecords(3, 100, 200)
	if len(end) != 56+20+22 || !bytes.Equal(end[len(end)-22+8:len(end)-22+20], bytes.Repeat([]byte{0xff}, 12)) {
		t.Error("the ZIP is not ended with ZIP64 records")
	}
}
