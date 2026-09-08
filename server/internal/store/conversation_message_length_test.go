package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestConversationUserMessageUnicodeLength(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := New(db)
	ids := mustSeedDevFixture(t, ctx, s)
	accepted := 0
	for _, tc := range []struct {
		name, content string
		valid         bool
	}{
		{"ASCII limit", strings.Repeat("a", 32000), true},
		{"reported Chinese message", strings.Repeat("中", 11000), true},
		{"Chinese limit", " \n" + strings.Repeat("中", 32000) + "\t", true},
		{"emoji limit", strings.Repeat("😀", 32000), true},
		{"ASCII over limit", strings.Repeat("a", 32001), false},
		{"Chinese over limit", strings.Repeat("中", 32001), false},
		{"blank", " \n\t", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := s.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
				ConversationID: ids.ConversationID, UserID: ids.UserID, Content: tc.content,
			})
			if !tc.valid {
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("error = %v, want ErrInvalidInput", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Message.Content != strings.TrimSpace(tc.content) || len(result.RunIDs) != 0 {
				t.Fatal("accepted message changed or unexpectedly dispatched an Agent")
			}
			accepted++
		})
	}
	var persisted int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM messages WHERE conversation_id = $1", ids.ConversationID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != accepted {
		t.Fatalf("persisted %d messages, want %d", persisted, accepted)
	}
}
