# Building an MSIX package (Windows 10 2004+ and Windows 11)

From the repository, run:

```sh
go run ./cmd/msix-build
```

It builds the client with [`cmd/windows-build`](BUILD_WINDOWS.md) (the first
build needs about 4.5 GB; `-low-memory` reduces it), packs it as an MSIX and
signs it. No Windows SDK is needed: the package is written and signed by Go
code alone (`cmd/msix-build`), so the command also runs on Linux and macOS.

The result is in `dist/msix` in the repository (ignored by git):

| File | What |
|---|---|
| `KomaruGram_<version>_<arch>.msix` | the package |
| `KomaruGram.cer` | the public certificate that signed it |
| `signing/` | the self-signed certificate and its **private key**; made on the first run and kept for the next ones. Do not share the key |

The command ends with what to do next. For a self-signed package:

1. Trust the certificate, once per machine. PowerShell **as administrator**:

   ```powershell
   Import-Certificate -FilePath ".\dist\msix\KomaruGram.cer" -CertStoreLocation Cert:\LocalMachine\TrustedPeople
   ```

2. Install, in an ordinary PowerShell:

   ```powershell
   Add-AppxPackage ".\dist\msix\KomaruGram_<version>_amd64.msix"
   ```

KomaruGram appears in the Start menu. To update, build a newer package (the
version grows with the commits) and run `Add-AppxPackage` again; to remove,
uninstall it in Settings, or `Get-AppxPackage KomaruGram | Remove-AppxPackage`.
Remove the certificate with `Get-ChildItem Cert:\LocalMachine\TrustedPeople |
Where Subject -eq 'CN=KomaruGram' | Remove-Item`.

A self-signed certificate is free, and good for your own machines and for
tests: everyone who installs the package must trust it first, and Windows
shows the app as made by an unknown publisher. For other people, sign with a
certificate a certification authority issued, or publish in the Microsoft
Store (see below).

## Your data stays where it is

A packaged desktop program normally writes to a copy of `%AppData%` that
belongs to the package and is deleted with it. The manifest opts out
(`unvirtualizedResources`, `FileSystemWriteVirtualization` and
`RegistryWriteVirtualization` disabled), so the packaged client keeps its
accounts, sessions and caches in the real `%AppData%\komarugram-go` and
`%LocalAppData%\komarugram-go`, the ones the unpackaged `KomaruGram.exe`
uses, and uninstalling the package leaves them. This is why the package needs
Windows 10 version 2004 (build 19041) or later. Windows 7 and older builds
stay with `cmd/windows-build`.

The program does not offer to install itself (shortcuts, the list of
programs) when it runs from a package: the package is installed and removed
by the system.

## Options

| Flag | What |
|---|---|
| `-o file.msix` | the package; its `.cer` is beside it |
| `-arch amd64\|arm64` | default: the build host's |
| `-exe KomaruGram.exe` | pack an executable built already, skip the build |
| `-low-memory` | passed to `cmd/windows-build` |
| `-version 1.2.3.0` | four numbers; default `0.<number of commits>.0.0` |
| `-name`, `-publisher-display-name` | identity name (3-50 letters, digits, `.`, `-`), the publisher's name shown to people |
| `-signing-dir dir` | where the self-signed certificate and key are kept |
| `-cert file.pem -key file.pem` | your own certificate (with its chain) and its unencrypted RSA key, PEM |
| `-publisher "CN=…"` | the publisher; default: the certificate's subject (`CN=KomaruGram` for the self-signed one) |
| `-unsigned` | do not sign |

The manifest's `Publisher` must be the certificate's subject, character for
character: `msix-build` refuses a pair that differs. A certificate kept in
`signing/` is used until it has less than a month left; then the command asks
to remove it, and a new certificate has to be trusted again. Delete the files
too when you want another publisher name.

## The package's icons

The taskbar, the Start menu and the app list take the icon from the package,
and pick the picture by the size they need and by the scale of the screen.
That choice is made by `resources.pri`, the package's resource index: without
it the system takes the one file the manifest names, scales it and draws the
accent color's plate behind it (a small, blurred icon on a blue square). So
the package holds the icon in the sizes of `logoNames` (`manifest.go`; with
and without the plate, "unplated") and `cmd/msix-build/resources.pri`, which
tells the system which is which.

The index is made by Microsoft's `makepri`, which is not part of the build:
the file is kept in the repository, and `go test` checks that it names every
picture. It holds the names only, not the pictures, so a new icon needs no
new index; a change of `logoNames`, or a new identity name, does. Then:

```sh
MSIX_PRI_DIR=$PWD/dist/pri-work/root go test ./cmd/msix-build -run TestWritePRISources
makepri createconfig /cf priconfig.xml /dq en-US /pv 10.0.0
# remove the <packaging> element from priconfig.xml: one index for all scales
makepri new /pr dist/pri-work/root /cf priconfig.xml /mn dist/pri-work/root/AppxManifest.xml /of cmd/msix-build/resources.pri /o
```

(`makepri` comes with the Windows SDK, or with the NuGet package
`Microsoft.Windows.SDK.BuildTools`; the manifest the first step writes has
the name `KomaruGram`.) The index is for the identity name `KomaruGram`: with
another `-name` the package is built without it, and the icon is drawn the
way described above.

## With a real certificate or a store

The self-signed certificate is only the default. The package itself does not
change:

- **A code signing certificate** from a certification authority (OV or EV,
  roughly 200–700 USD a year; most issue the key on hardware, which cannot
  be exported): when you have the key as a PEM file, `-cert chain.pem -key
  key.pem`. When the key stays in a token or a cloud service, build with
  `-unsigned -publisher "<the certificate's subject>"` and sign the result
  with `signtool sign /fd SHA256 /a /f …` (or `/sha1 <thumbprint>`, or Azure
  Trusted Signing's dlib) from the Windows SDK. People who install need no
  `Import-Certificate` then.
- **Microsoft Store**: reserve the name in Partner Center (a one-time fee for
  a person), build with `-unsigned -name <reserved name> -publisher
  "<the publisher it gives>"`, and upload the package; Microsoft signs it.
  The Store may question the `unvirtualizedResources` capability: it is
  restricted, and has to be justified in the submission.
- **SignPath Foundation** signs open source projects for free in its own CI;
  whether it signs MSIX packages has to be checked with them first. Use
  `-unsigned` and let it sign.

## What was checked, and what was not

Checked on Windows 10 LTSC 2021 (19044), amd64: the signature and hashes of a
built package are accepted by the system's own verifier (`WinVerifyTrust`
answers only that the self-signed root is not trusted), and the unit tests of
`cmd/msix-build` (manifest, block map, ZIP layout, signature, certificate).
Installing and running the package, the data staying in the real profile
after uninstalling, the tray, notifications and starting other programs
(FFmpeg, a player, a browser) have to be tried on a machine; arm64 packages
are built but were not run, and Windows 11 and macOS/Linux hosts were not
tried.
