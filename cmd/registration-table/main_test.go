// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

// TestReadsTheClientsFile reads a file of points as the client sends it:
// the time it was collected at is a comment, and each point keeps its
// second.
func TestReadsTheClientsFile(t *testing.T) {
	at := time.Date(2026, 10, 20, 21, 11, 33, 0, time.UTC)
	joined := time.Date(2025, 8, 12, 10, 30, 15, 0, time.UTC)
	name := filepath.Join(t.TempDir(), model.RegistrationPointsName(at))
	if filepath.Base(name) != "registration-points-2026-10-20.txt" {
		t.Errorf("named %s", filepath.Base(name))
	}
	var b strings.Builder
	if err := model.WriteRegistrationPoints(&b, []model.RegistrationPoint{{User: 8262622416, Date: joined}, {User: 5000000001, Date: joined}}, at); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "# Collected at 2026-10-20T21:11:33Z.") {
		t.Errorf("no time of collection in\n%s", b.String())
	}
	if err := os.WriteFile(name, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	points, err := read(name)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 || points[0].id != 5000000001 || points[1].id != 8262622416 {
		t.Fatalf("read %+v", points)
	}
	if got := time.Unix(int64(points[1].days*day+0.5), 0).UTC(); !got.Equal(joined) {
		t.Errorf("read %v, want %v", got, joined)
	}
}
