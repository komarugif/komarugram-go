// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"bytes"
	"testing"
)

func TestNoticeFilterPreservesDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"complete", "Success\n\n" + appImageHubNotice + "done\n", "Success\n\ndone\n"},
		{"repeated", appImageHubNotice + "warning\n" + appImageHubNotice, "warning\n"},
		{"progress", "\r20%\r100%\nWARNING: compression failed\n", "\r20%\r100%\nWARNING: compression failed\n"},
		{"partial", "error\n" + appImageHubNotice[:90], "error\n" + appImageHubNotice[:90]},
		{"changed", "Please consider submitting your AppImage to another service\n", "Please consider submitting your AppImage to another service\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, chunk := range []int{1, 7, len(appImageHubNotice) - 1, len(tc.input) + 1} {
				var out bytes.Buffer
				filter := &noticeFilter{out: &out}
				for start := 0; start < len(tc.input); start += chunk {
					part := []byte(tc.input[start:min(start+chunk, len(tc.input))])
					if n, err := filter.Write(part); err != nil || n != len(part) {
						t.Fatalf("chunk %d: Write = %d, %v", chunk, n, err)
					}
				}
				if err := filter.Flush(); err != nil {
					t.Fatal(err)
				}
				if got := out.String(); got != tc.want {
					t.Fatalf("chunk %d: output %q, want %q", chunk, got, tc.want)
				}
			}
		})
	}
}
