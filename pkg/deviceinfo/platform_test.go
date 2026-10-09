// SPDX-License-Identifier: Unlicense OR MIT

package deviceinfo

import "testing"

func TestOSReleaseName(t *testing.T) {
	for data, want := range map[string]string{
		"NAME=\"Ubuntu\"\nVERSION=\"24.04.1 LTS (Noble Numbat)\"\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\n": "Ubuntu 24.04.1 LTS",
		"NAME=Alpine\nVERSION=3.20\n":              "Alpine 3.20",
		"# comment\nPRETTY_NAME='Arch Linux'\n":    "Arch Linux",
		"PRETTY_NAME=\"Say \\\"hi\\\" \\$HOME\"\n": `Say "hi" $HOME`,
		"": "",
	} {
		if got := osReleaseName(data); got != want {
			t.Errorf("osReleaseName(%q) = %q, want %q", data, got, want)
		}
	}
}

func TestWindowsName(t *testing.T) {
	for _, c := range []struct {
		product, sp string
		build       uint32
		want        string
	}{
		{"Windows 7 Ultimate", "Service Pack 1", 7601, "Windows 7 Ultimate Service Pack 1"},
		{"Windows 10 Pro", "", 19045, "Windows 10 Pro"},
		// Windows 11's registry still says Windows 10.
		{"Windows 10 Pro", "", 22631, "Windows 11 Pro"},
		{"", "", 9600, "Windows"},
	} {
		if got := windowsName(c.product, c.sp, c.build); got != c.want {
			t.Errorf("windowsName(%q, %q, %d) = %q, want %q", c.product, c.sp, c.build, got, c.want)
		}
	}
}

func TestPlatformText(t *testing.T) {
	for _, c := range [][3]string{
		{"Ubuntu 24.04 LTS", "Linux 6.8.0", "Ubuntu 24.04 LTS (Linux\u00a06.8.0)"},
		{"Haiku", "", "Haiku"},
		{"", "FreeBSD 14.1", "FreeBSD 14.1"},
	} {
		if got := platformText(c[0], c[1]); got != c[2] {
			t.Errorf("platformText(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
	if Platform() == "" {
		t.Error("no platform here")
	}
	t.Log(Platform())
}
