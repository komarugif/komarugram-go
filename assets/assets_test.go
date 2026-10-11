// SPDX-License-Identifier: Unlicense OR MIT

package assets

import (
	"os"
	"strings"
	"testing"
)

// dependencies.txt names the versions go.mod requires.
func TestDependenciesFollowGoMod(t *testing.T) {
	gomod, err := os.ReadFile("../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]string{}
	for _, line := range strings.Split(string(gomod), "\n") {
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "require "))
		if len(fields) >= 2 && strings.HasPrefix(fields[1], "v") {
			required[fields[0]] = fields[1]
		}
	}
	deps := Dependencies()
	if len(deps) == 0 {
		t.Fatal("no dependencies listed")
	}
	for _, d := range deps {
		if v, ok := required[d.Module]; !ok {
			t.Errorf("%s: go.mod does not require %s", d.Name, d.Module)
		} else if v != d.Version {
			t.Errorf("%s: dependencies.txt has %s, go.mod requires %s", d.Name, d.Version, v)
		}
	}
}
