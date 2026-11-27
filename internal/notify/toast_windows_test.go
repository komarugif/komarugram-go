// SPDX-License-Identifier: Unlicense OR MIT

//go:build windows

package notify

import (
	"strings"
	"testing"
)

func TestBuildToastXML(t *testing.T) {
	got := buildToastXML("Сватош", "о зашло & <ok>", `C:\cache\avatar.png`, false)
	for _, want := range []string{
		`template="ToastGeneric"`,
		`placement="appLogoOverride"`,
		`hint-crop="circle"`,
		`src="file:///C:/cache/avatar.png"`,
		`<text>Сватош</text>`,
		`<text>о зашло &amp; &lt;ok&gt;</text>`,
		`<audio silent="true"/>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("XML missing %q:\n%s", want, got)
		}
	}
	if tag := toastTag("acc/1234567890123456789"); len(tag) != 16 {
		t.Errorf("toastTag length = %d (%q), want 16", len(tag), tag)
	}
}
