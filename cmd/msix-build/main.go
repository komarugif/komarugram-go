// SPDX-License-Identifier: Unlicense OR MIT

// Command msix-build builds the Windows messenger as an MSIX package and
// signs it, by default with a self-signed certificate it makes and keeps.
// No Windows SDK is needed. See docs/BUILD_MSIX.md.
//
//	go run ./cmd/msix-build
package main

import (
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"komarugram/pkg/program"
)

type options struct {
	out, arch, exe, version  string
	name, publisher, display string
	cert, key, signingDir    string
	unsigned, lowMemory      bool
}

func main() {
	log.SetFlags(0)
	arch := "amd64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	o := options{}
	flag.StringVar(&o.out, "o", "", "output package (default: dist/msix/KomaruGram_<version>_<arch>.msix in the repository)")
	flag.StringVar(&o.arch, "arch", arch, "Windows architecture: amd64 or arm64")
	flag.StringVar(&o.exe, "exe", "", "a KomaruGram.exe built already (default: build it with cmd/windows-build)")
	flag.StringVar(&o.version, "version", "", "package version, four numbers (default: 0.<number of commits>.0.0)")
	flag.StringVar(&o.name, "name", defaultName, "package identity name (the icons need the default: see docs/BUILD_MSIX.md)")
	flag.StringVar(&o.publisher, "publisher", "", "publisher, the certificate's subject (default: the subject of the certificate, CN=KomaruGram for the self-signed one)")
	flag.StringVar(&o.display, "publisher-display-name", "KomaruGram", "publisher's name shown to people")
	flag.StringVar(&o.cert, "cert", "", "PEM file with your own code signing certificate (and its chain) instead of the self-signed one; needs -key")
	flag.StringVar(&o.key, "key", "", "PEM file with the RSA private key of -cert")
	flag.StringVar(&o.signingDir, "signing-dir", "", "where the self-signed certificate and its key are kept (default: dist/msix/signing in the repository)")
	flag.BoolVar(&o.unsigned, "unsigned", false, "do not sign: for the Microsoft Store, or for signing with another tool")
	flag.BoolVar(&o.lowMemory, "low-memory", false, "build the executable with less memory (see windows-build)")
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatal("unexpected arguments; use -h for help")
	}
	if err := build(o); err != nil {
		log.Fatal(err)
	}
}

func build(o options) error {
	arch, err := manifestArch(o.arch)
	if err != nil {
		return err
	}
	if err := identityName(o.name); err != nil {
		return err
	}
	if (o.cert == "") != (o.key == "") {
		return fmt.Errorf("-cert and -key go together")
	}
	if o.unsigned && o.cert != "" {
		return fmt.Errorf("-unsigned and -cert exclude each other")
	}
	repo, err := goOutput("list", "-m", "-f", "{{.Dir}}", "komarugram")
	if err != nil {
		return fmt.Errorf("run from the repository: %w", err)
	}
	if o.version == "" {
		o.version = defaultVersion(repo)
	}
	if err := validVersion(o.version); err != nil {
		return err
	}
	dist := filepath.Join(repo, "dist", "msix")
	if o.out == "" {
		o.out = filepath.Join(dist, fmt.Sprintf("KomaruGram_%s_%s.msix", o.version, o.arch))
	}
	if o.out, err = filepath.Abs(o.out); err != nil {
		return err
	}
	if filepath.Ext(o.out) != ".msix" {
		return fmt.Errorf("output name must end with .msix")
	}
	if o.signingDir == "" {
		o.signingDir = filepath.Join(dist, "signing")
	}

	// The identity first: a wrong certificate should not cost a build.
	var id identity
	var made bool
	switch {
	case o.unsigned:
		if o.publisher == "" {
			o.publisher = defaultPublisher
		}
	case o.cert != "":
		if id, err = loadIdentity(o.cert, o.key); err != nil {
			return err
		}
		if o.publisher == "" {
			o.publisher = id.publisher()
		}
	default:
		if o.publisher == "" {
			o.publisher = defaultPublisher
		}
		if id, made, err = loadOrCreate(o.signingDir, o.publisher, time.Now()); err != nil {
			return err
		}
	}
	if !o.unsigned && id.publisher() != o.publisher {
		return fmt.Errorf("the certificate's subject is %q, but the package would name the publisher %q: they must be the same", id.publisher(), o.publisher)
	}

	if err := os.MkdirAll(filepath.Dir(o.out), 0o755); err != nil {
		return err
	}
	exe, cleanup, err := resolveExe(o, repo)
	if err != nil {
		return err
	}
	defer cleanup()
	exeData, err := os.ReadFile(exe)
	if err != nil {
		return err
	}
	logos, err := logoParts()
	if err != nil {
		return err
	}
	xml, err := manifest(manifestInfo{Name: o.name, Publisher: o.publisher, PublisherDisplay: o.display, Version: o.version, Arch: arch})
	if err != nil {
		return err
	}
	var sign signer
	if !o.unsigned {
		sign = signRSA(id.Chain, id.Key, time.Now())
	}
	payload := append([]part{{"KomaruGram.exe", exeData}}, logos...)
	if o.name == defaultName {
		payload = append(payload, part{"resources.pri", resourcesPRI})
	} else {
		log.Printf("the package name is not %s: without its resources.pri the taskbar draws the icon small, on a plate (docs/BUILD_MSIX.md)", defaultName)
	}
	pkg, err := buildPackage(xml, payload, sign)
	if err != nil {
		return err
	}
	tmp := o.out + ".tmp"
	if err := os.WriteFile(tmp, pkg, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, o.out); err != nil {
		return fmt.Errorf("replace %s: %w", o.out, err)
	}
	log.Printf("built %s", o.out)

	if o.unsigned {
		log.Printf("\nThe package is not signed: it does not install until it is signed (Microsoft Store, signtool, Azure Trusted Signing, SignPath).\nIts publisher is %q: the certificate's subject must be the same.", o.publisher)
		return nil
	}
	cer := filepath.Join(filepath.Dir(o.out), "KomaruGram.cer")
	if err := os.WriteFile(cer, id.Chain[0].Raw, 0o644); err != nil {
		return err
	}
	instructions(o, cer, made, id.Chain[0])
	return nil
}

// instructions tell what to do with the package, in the words of the
// person who has to do it.
func instructions(o options, cer string, made bool, cert *x509.Certificate) {
	if made {
		log.Printf("made a self-signed certificate %q, valid until %s; its private key is in %s: keep it, and do not share it", cert.Subject, cert.NotAfter.Format(time.DateOnly), o.signingDir)
	}
	log.Printf(`
To install the package, on Windows 10 (2004) or 11:
 1. Trust the certificate once (PowerShell as administrator):
      Import-Certificate -FilePath "%s" -CertStoreLocation Cert:\LocalMachine\TrustedPeople
 2. Install the package (ordinary PowerShell):
      Add-AppxPackage "%s"
`, cer, o.out)
	if o.cert == "" {
		log.Printf("A self-signed certificate is for your own machines and for tests: everyone who installs the package must trust it first. For everyone else, sign with a certificate a certification authority issued (-cert and -key), or publish in the Microsoft Store.")
	}
}

func defaultVersion(repo string) string {
	out, err := program.Command("git", "-C", repo, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		log.Printf("the version will not tell the commit: git rev-list: %v", err)
		return "0.0.1.0"
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n > 65535 {
		return "0.0.1.0"
	}
	return fmt.Sprintf("0.%d.0.0", n)
}

// resolveExe returns the executable to pack: -exe, or one built by
// cmd/windows-build into a directory of its own, which cleanup removes.
func resolveExe(o options, repo string) (exe string, cleanup func(), err error) {
	if o.exe != "" {
		abs, err := filepath.Abs(o.exe)
		return abs, func() {}, err
	}
	staging, err := os.MkdirTemp(filepath.Dir(o.out), ".msix-build-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.RemoveAll(staging) }
	exe = filepath.Join(staging, "KomaruGram.exe")
	args := []string{"run", "./cmd/windows-build", "-arch", o.arch, "-o", exe}
	if o.lowMemory {
		args = append(args, "-low-memory")
	}
	cmd := program.Command("go", args...)
	cmd.Dir = repo
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	log.Printf("building the executable with cmd/windows-build")
	if err := cmd.Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("windows-build: %w", err)
	}
	return exe, cleanup, nil
}

func goOutput(args ...string) (string, error) {
	out, err := program.Command("go", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}
