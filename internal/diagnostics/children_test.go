package diagnostics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCaptureRecordsChildren checks a capture writes the profiles of the
// client's own processes beside the process's, and tells one that failed.
func TestCaptureRecordsChildren(t *testing.T) {
	r := Enable()
	defer r.Close()
	var asked []string
	SetChildProfiler("test", func(ctx context.Context, kind string, d time.Duration) []ChildProfile {
		asked = append(asked, kind)
		return []ChildProfile{
			{Name: "decoder", PID: 42, Data: []byte("profile of " + kind)},
			{Name: "broken", PID: 43, Err: errors.New("gone")},
		}
	})
	defer SetChildProfiler("test", nil)
	dir := t.TempDir()
	paths, err := r.Capture(dir, "goroutine")
	if err == nil || !strings.Contains(err.Error(), "broken: gone") {
		t.Fatalf("the failed child not told: %v", err)
	}
	if len(paths) != 2 || !strings.HasSuffix(paths[1], "-goroutine-decoder-42.pprof") || filepath.Dir(paths[1]) != filepath.Dir(paths[0]) {
		t.Fatalf("paths %q", paths)
	}
	if data, err := os.ReadFile(paths[1]); err != nil || string(data) != "profile of goroutine" {
		t.Fatalf("%q, %v", data, err)
	}
	if _, err := r.Capture(dir, "snapshot"); err != nil || len(asked) != 1 {
		t.Fatalf("children asked for a snapshot: %v, %v", asked, err)
	}
}
