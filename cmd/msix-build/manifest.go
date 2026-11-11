// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"komarugram/internal/appicon"
)

// manifestInfo is what the package's AppxManifest.xml tells.
type manifestInfo struct {
	// Name is the package's identity name; Publisher is the certificate's
	// subject, the same text.
	Name, Publisher string
	// PublisherDisplay is the publisher's name for people.
	PublisherDisplay string
	// Version has four numbers.
	Version string
	// Arch is the manifest's name of the architecture: x64 or arm64.
	Arch string
}

var versionPattern = regexp.MustCompile(`^(\d{1,5})\.(\d{1,5})\.(\d{1,5})\.(\d{1,5})$`)

func validVersion(v string) error {
	m := versionPattern.FindStringSubmatch(v)
	if m == nil {
		return fmt.Errorf("version %q: four numbers like 1.2.3.0 are needed", v)
	}
	for _, n := range m[1:] {
		if len(n) == 5 && n > "65535" {
			return fmt.Errorf("version %q: a number is over 65535", v)
		}
	}
	return nil
}

// manifestArch maps Go's architecture to the manifest's.
func manifestArch(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "arm64", nil
	}
	return "", fmt.Errorf("unsupported Windows architecture %q; use amd64 or arm64", goarch)
}

// The data of the client is in the user's profile (%AppData%\komarugram-go)
// where the unpackaged client keeps it too. A packaged desktop app writes
// to a copy of it that goes with the package unless it opts out; the
// restricted capability unvirtualizedResources and the two properties do
// (Windows 10, version 1903 and later).
var manifestTemplate = template.Must(template.New("manifest").Funcs(template.FuncMap{"esc": xmlEscape}).Parse(`<?xml version="1.0" encoding="utf-8"?>
<Package
  xmlns="http://schemas.microsoft.com/appx/manifest/foundation/windows10"
  xmlns:uap="http://schemas.microsoft.com/appx/manifest/uap/windows10"
  xmlns:rescap="http://schemas.microsoft.com/appx/manifest/foundation/windows10/restrictedcapabilities"
  xmlns:desktop6="http://schemas.microsoft.com/appx/manifest/desktop/windows10/6"
  IgnorableNamespaces="uap rescap desktop6">
  <Identity Name="{{esc .Name}}" Publisher="{{esc .Publisher}}" Version="{{.Version}}" ProcessorArchitecture="{{.Arch}}" />
  <Properties>
    <DisplayName>KomaruGram</DisplayName>
    <PublisherDisplayName>{{esc .PublisherDisplay}}</PublisherDisplayName>
    <Logo>Assets\StoreLogo.png</Logo>
    <desktop6:FileSystemWriteVirtualization>disabled</desktop6:FileSystemWriteVirtualization>
    <desktop6:RegistryWriteVirtualization>disabled</desktop6:RegistryWriteVirtualization>
  </Properties>
  <Dependencies>
    <TargetDeviceFamily Name="Windows.Desktop" MinVersion="10.0.19041.0" MaxVersionTested="10.0.22631.0" />
  </Dependencies>
  <Resources>
    <Resource Language="en-us" />
  </Resources>
  <Applications>
    <Application Id="KomaruGram" Executable="KomaruGram.exe" EntryPoint="Windows.FullTrustApplication">
      <uap:VisualElements
        DisplayName="KomaruGram"
        Description="Telegram desktop client"
        BackgroundColor="transparent"
        Square150x150Logo="Assets\Square150x150Logo.png"
        Square44x44Logo="Assets\Square44x44Logo.png" />
    </Application>
  </Applications>
  <Capabilities>
    <Capability Name="internetClient" />
    <rescap:Capability Name="runFullTrust" />
    <rescap:Capability Name="unvirtualizedResources" />
  </Capabilities>
</Package>
`))

func manifest(m manifestInfo) ([]byte, error) {
	var b bytes.Buffer
	if err := manifestTemplate.Execute(&b, m); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// logoNames are the pictures the manifest names and the system picks from by
// its qualifiers (resources.pri tells how): the tile and the Start menu's
// by scale, the app list and the taskbar by pixel size, with and without the
// plate of the accent color behind (the unplated ones are drawn on their
// own), and the store's. Each is of the size given.
func logoNames() map[string]int {
	logos := map[string]int{
		"Square44x44Logo.png":   44,
		"Square150x150Logo.png": 150,
		"StoreLogo.png":         50,
	}
	for _, scale := range []int{100, 125, 150, 200, 400} {
		logos[fmt.Sprintf("Square44x44Logo.scale-%d.png", scale)] = 44 * scale / 100
		logos[fmt.Sprintf("Square150x150Logo.scale-%d.png", scale)] = 150 * scale / 100
		logos[fmt.Sprintf("StoreLogo.scale-%d.png", scale)] = 50 * scale / 100
	}
	for _, size := range []int{16, 20, 24, 30, 32, 36, 40, 48, 60, 64, 72, 80, 96, 256} {
		logos[fmt.Sprintf("Square44x44Logo.targetsize-%d.png", size)] = size
		logos[fmt.Sprintf("Square44x44Logo.targetsize-%d_altform-unplated.png", size)] = size
	}
	return logos
}

// logoParts are the pictures of logoNames, made from the application's icon.
func logoParts() ([]part, error) {
	var out []part
	for name, size := range logoNames() {
		var b bytes.Buffer
		var im image.Image = appicon.Image(size)
		if err := png.Encode(&b, im); err != nil {
			return nil, err
		}
		out = append(out, part{"Assets/" + name, b.Bytes()})
	}
	// The same bytes for the same package.
	slices.SortFunc(out, func(a, b part) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// identityName checks the package name: letters, digits, dots and dashes.
func identityName(name string) error {
	if len(name) < 3 || len(name) > 50 || strings.Trim(name, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789.-") != "" {
		return fmt.Errorf("package name %q: 3 to 50 letters, digits, dots and dashes are needed", name)
	}
	return nil
}
