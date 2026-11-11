// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"path"
	"strings"
)

// Names of the parts of a package that are not its payload.
const (
	manifestName     = "AppxManifest.xml"
	blockMapName     = "AppxBlockMap.xml"
	contentTypesName = "[Content_Types].xml"
	signatureName    = "AppxSignature.p7x"
)

// blockSize is the size of the pieces of a file the block map keeps a hash
// of.
const blockSize = 64 << 10

// part is a file of the package. Name has forward slashes.
type part struct {
	Name string
	Data []byte
}

// lfhSize is the size of a file's local header in the ZIP the package is
// written as, which the block map tells for each file.
func lfhSize(name string) int { return 30 + len(name) }

// contentTypes lists the media type of every extension and of the parts
// that are not found by their extension.
func contentTypes(payload []part) []byte {
	known := map[string]string{
		"png": "image/png",
		"exe": "application/x-msdownload",
		"dll": "application/x-msdownload",
		"xml": "application/xml",
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	seen := map[string]bool{}
	for _, p := range payload {
		ext := strings.TrimPrefix(path.Ext(p.Name), ".")
		if ext == "" || seen[ext] {
			continue
		}
		seen[ext] = true
		typ, ok := known[strings.ToLower(ext)]
		if !ok {
			typ = "application/octet-stream"
		}
		fmt.Fprintf(&b, `<Default Extension="%s" ContentType="%s"/>`, ext, typ)
	}
	b.WriteString(`<Override PartName="/AppxManifest.xml" ContentType="application/vnd.ms-appx.manifest+xml"/>`)
	b.WriteString(`<Override PartName="/AppxBlockMap.xml" ContentType="application/vnd.ms-appx.blockmap+xml"/>`)
	b.WriteString(`<Override PartName="/AppxSignature.p7x" ContentType="application/vnd.ms-appx.signature"/>`)
	b.WriteString(`</Types>`)
	return []byte(b.String())
}

// blockMap tells, for each file of the payload, its size, the size of its
// header in the ZIP and the SHA-256 of each 64 KiB block of it. The files
// are stored without compression, so the blocks carry no compressed size.
func blockMap(payload []part) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	b.WriteString(`<BlockMap xmlns="http://schemas.microsoft.com/appx/2010/blockmap" HashMethod="http://www.w3.org/2001/04/xmlenc#sha256">`)
	for _, p := range payload {
		fmt.Fprintf(&b, `<File Name="%s" Size="%d" LfhSize="%d">`, xmlEscape(strings.ReplaceAll(p.Name, "/", `\`)), len(p.Data), lfhSize(p.Name))
		for off := 0; off < len(p.Data); off += blockSize {
			sum := sha256.Sum256(p.Data[off:min(off+blockSize, len(p.Data))])
			fmt.Fprintf(&b, `<Block Hash="%s"/>`, base64.StdEncoding.EncodeToString(sum[:]))
		}
		b.WriteString(`</File>`)
	}
	b.WriteString(`</BlockMap>`)
	return []byte(b.String())
}

// zipEntry is a stored file in the central directory.
type zipEntry struct {
	name   string
	crc    uint32
	size   uint32
	offset uint32
}

// The date every entry carries, so that a package of the same files is the
// same bytes: 2020-01-01 00:00.
const (
	dosDate = (2020-1980)<<9 | 1<<5 | 1
	dosTime = 0
)

// writeEntry appends a stored file to buf and returns its record.
func writeEntry(buf *bytes.Buffer, name string, data []byte) zipEntry {
	e := zipEntry{name: name, crc: crc32.ChecksumIEEE(data), size: uint32(len(data)), offset: uint32(buf.Len())}
	h := make([]byte, 30)
	binary.LittleEndian.PutUint32(h[0:], 0x04034b50)
	binary.LittleEndian.PutUint16(h[4:], 20) // version needed
	// flags 0, method 0 (stored)
	binary.LittleEndian.PutUint16(h[10:], dosTime)
	binary.LittleEndian.PutUint16(h[12:], dosDate)
	binary.LittleEndian.PutUint32(h[14:], e.crc)
	binary.LittleEndian.PutUint32(h[18:], e.size)
	binary.LittleEndian.PutUint32(h[22:], e.size)
	binary.LittleEndian.PutUint16(h[26:], uint16(len(name)))
	buf.Write(h)
	buf.WriteString(name)
	buf.Write(data)
	return e
}

// centralRecord is the entry's record of the central directory.
func centralRecord(e zipEntry) []byte {
	h := make([]byte, 46)
	binary.LittleEndian.PutUint32(h[0:], 0x02014b50)
	// The system takes the hash of the directory with the version made by of
	// its records as 45, as the packaging tool writes it.
	binary.LittleEndian.PutUint16(h[4:], 45) // version made by
	binary.LittleEndian.PutUint16(h[6:], 20) // version needed
	binary.LittleEndian.PutUint16(h[12:], dosTime)
	binary.LittleEndian.PutUint16(h[14:], dosDate)
	binary.LittleEndian.PutUint32(h[16:], e.crc)
	binary.LittleEndian.PutUint32(h[20:], e.size)
	binary.LittleEndian.PutUint32(h[24:], e.size)
	binary.LittleEndian.PutUint16(h[28:], uint16(len(e.name)))
	binary.LittleEndian.PutUint32(h[42:], e.offset)
	return append(h, e.name...)
}

// endRecords close the ZIP: the ZIP64 end of central directory record and
// its locator, which every package has, and the end record with its fields
// set to the values that say to look there.
func endRecords(entries, cdSize, cdOffset int) []byte {
	z := make([]byte, 56)
	binary.LittleEndian.PutUint32(z[0:], 0x06064b50)
	binary.LittleEndian.PutUint64(z[4:], 44)
	binary.LittleEndian.PutUint16(z[12:], 45) // version made by
	binary.LittleEndian.PutUint16(z[14:], 45) // version needed
	binary.LittleEndian.PutUint64(z[24:], uint64(entries))
	binary.LittleEndian.PutUint64(z[32:], uint64(entries))
	binary.LittleEndian.PutUint64(z[40:], uint64(cdSize))
	binary.LittleEndian.PutUint64(z[48:], uint64(cdOffset))
	l := make([]byte, 20)
	binary.LittleEndian.PutUint32(l[0:], 0x07064b50)
	binary.LittleEndian.PutUint64(l[8:], uint64(cdOffset+cdSize)) // where the record above is
	binary.LittleEndian.PutUint32(l[16:], 1)
	e := make([]byte, 22)
	binary.LittleEndian.PutUint32(e[0:], 0x06054b50)
	for i := 8; i < 20; i++ {
		e[i] = 0xff
	}
	return bytes.Join([][]byte{z, l, e}, nil)
}

// signer makes the signature part of a package from the hashes of its
// parts; nil leaves the package unsigned.
type signer func(hashes packageHashes) ([]byte, error)

// packageHashes are what a package's signature covers: the SHA-256 of the
// ZIP's files, of its central directory, of [Content_Types].xml and of
// AppxBlockMap.xml, all taken before the signature is added. The central
// directory is taken with the records that end the ZIP.
type packageHashes struct {
	Files, Directory, ContentTypes, BlockMap [32]byte
}

// buildPackage writes the package: the payload, the manifest, the content
// types, the block map and, when sign is set, the signature, as a ZIP.
func buildPackage(manifest []byte, payload []part, sign signer) ([]byte, error) {
	files := append(append([]part{}, payload...), part{manifestName, manifest})
	types := contentTypes(files)
	blocks := blockMap(files)
	var buf bytes.Buffer
	var entries []zipEntry
	for _, p := range files {
		entries = append(entries, writeEntry(&buf, p.Name, p.Data))
	}
	entries = append(entries, writeEntry(&buf, contentTypesName, types))
	entries = append(entries, writeEntry(&buf, blockMapName, blocks))
	var dir bytes.Buffer
	for _, e := range entries {
		dir.Write(centralRecord(e))
	}
	// The directory is hashed with the end of the ZIP as it is without the
	// signature, the way the system takes the hash again.
	end := endRecords(len(entries), dir.Len(), buf.Len())
	if sign != nil {
		sig, err := sign(packageHashes{
			Files:        sha256.Sum256(buf.Bytes()),
			Directory:    sha256.Sum256(append(append([]byte{}, dir.Bytes()...), end...)),
			ContentTypes: sha256.Sum256(types),
			BlockMap:     sha256.Sum256(blocks),
		})
		if err != nil {
			return nil, err
		}
		// The signature goes where the directory began, and the directory
		// is written after it, with its record.
		entries = append(entries, writeEntry(&buf, signatureName, sig))
		dir.Write(centralRecord(entries[len(entries)-1]))
	}
	cdOffset := buf.Len()
	buf.Write(dir.Bytes())
	buf.Write(endRecords(len(entries), dir.Len(), cdOffset))
	return buf.Bytes(), nil
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
