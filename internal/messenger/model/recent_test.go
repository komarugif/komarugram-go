package model

import (
	"testing"
	"time"
)

func TestBumpChat(t *testing.T) {
	list := []Chat{{ID: 1}, {ID: 2}, {ID: 3}}
	got := BumpChat(list, Chat{ID: 3, Title: "c", Last: Preview{Text: "hi"}, Unread: 5, LastTime: time.Now()}, 3)
	if len(got) != 3 || got[0].ID != 3 || got[1].ID != 1 || got[2].ID != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Last != (Preview{}) || got[0].Unread != 0 || !got[0].LastTime.IsZero() || got[0].Title != "c" {
		t.Errorf("kept %+v", got[0])
	}
	if got := BumpChat(list, Chat{ID: 9}, 3); len(got) != 3 || got[0].ID != 9 || got[2].ID != 2 {
		t.Errorf("over the limit: %+v", got)
	}
	if list[0].ID != 1 {
		t.Error("the list given was changed")
	}
}
