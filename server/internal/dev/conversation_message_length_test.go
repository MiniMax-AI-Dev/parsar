package dev

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

func TestConversationUserMessageUnicodeLength(t *testing.T) {
	router := chi.NewRouter()
	RegisterRoutesWithStore(router, stubRuntimeStore{})
	for _, tc := range []struct {
		name, content string
		status        int
	}{
		{"ASCII limit", strings.Repeat("a", 32000), http.StatusCreated},
		{"reported Chinese message", strings.Repeat("中", 11000), http.StatusCreated},
		{"Chinese limit", " \n" + strings.Repeat("中", 32000) + "\t", http.StatusCreated},
		{"emoji limit", strings.Repeat("😀", 32000), http.StatusCreated},
		{"ASCII over limit", strings.Repeat("a", 32001), http.StatusUnprocessableEntity},
		{"Chinese over limit", strings.Repeat("中", 32001), http.StatusUnprocessableEntity},
		{"blank", " \n\t", http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(createConversationUserMessageBody{Content: tc.content})
			if err != nil {
				t.Fatal(err)
			}
			res := serveDevRoute(t, router, http.MethodPost, "/api/v1/conversations/"+testConversationID+"/messages", string(body))
			if res.Code != tc.status {
				t.Fatalf("status = %d, want %d", res.Code, tc.status)
			}
			if tc.status == http.StatusCreated {
				var result struct {
					Message store.MessageRead `json:"message"`
				}
				if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Message.Content != strings.TrimSpace(tc.content) {
					t.Fatal("accepted message content changed")
				}
			}
		})
	}
}
