// SPDX-License-Identifier: Unlicense OR MIT

package historycache

import (
	"context"
	"path/filepath"
	"testing"

	"komarugram/internal/messenger/model"
)

// A sender's message in a group is found past the batches of rows read,
// the newest of them; one in a private chat, or of no one cached, is not.
func TestSenderMessage(t *testing.T) {
	ctx := context.Background()
	c, err := Open(filepath.Join(t.TempDir(), "h"), "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	msg := func(chat int64, id int, sender int64) model.Message {
		return model.Message{Key: model.MessageKey{AccountID: "a", ChatID: chat, MessageID: model.MessageID(id)}, SenderID: sender, Text: "x"}
	}
	// The author wrote in a private chat and, twice, in a group, before
	// many messages of others.
	batch := []model.Message{msg(42, 1, 7), msg(-1000000000005, 1, 7), msg(-1000000000005, 2, 7)}
	for i := 3; i < 3*senderBatch; i++ {
		batch = append(batch, msg(-1000000000005, i, 8))
	}
	if err := c.SaveMessages(ctx, batch); err != nil {
		t.Fatal(err)
	}
	key, ok, err := c.SenderMessage(ctx, 7)
	if err != nil || !ok || key.ChatID != -1000000000005 || key.MessageID != 2 {
		t.Fatalf("%+v, %v, %v", key, ok, err)
	}
	if _, ok, err := c.SenderMessage(ctx, 9); ok || err != nil {
		t.Fatalf("a sender never cached found: %v, %v", ok, err)
	}
}
