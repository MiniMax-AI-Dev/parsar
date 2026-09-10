package httprunner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendRejectsRedirectsAndInvalidResponses(t *testing.T) {
	var followed bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed = true }))
	defer target.Close()
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"redirect", "", 302, ErrNon2xx},
		{"status", "sensitive provider details", 401, ErrNon2xx},
		{"invalid", "not json", 200, ErrInvalidJSON},
		{"empty", `{"content":" "}`, 200, ErrInvalidJSON},
		{"oversized", strings.Repeat("x", (4<<20)+1), 200, ErrInvalidJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", target.URL)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer endpoint.Close()
			_, err := Send(context.Background(), nil, endpoint.URL, map[string]string{"Authorization": "Bearer synthetic"}, AgentRequest{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), endpoint.URL) {
				t.Fatal("error exposed provider details")
			}
		})
	}
	if followed {
		t.Fatal("followed credential-bearing redirect")
	}
}
