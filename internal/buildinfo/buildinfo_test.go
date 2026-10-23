package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestFromSettings(t *testing.T) {
	b := fromSettings([]debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: "9bff187bac4f9f4ac595ac16784b221381ff1b9c"},
		{Key: "vcs.modified", Value: "true"},
	})
	if b.Commit != "9bff187bac4f9f4ac595ac16784b221381ff1b9c" || !b.Modified {
		t.Fatalf("got %+v", b)
	}
	if got := b.Short(); got != "9bff187bac4f+" {
		t.Errorf("Short is %q", got)
	}
	if got := fromSettings(nil).Short(); got != "" {
		t.Errorf("Short without a commit is %q", got)
	}
}
