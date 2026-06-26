package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFeishuAppRegistrationBeginPostsInitThenBegin(t *testing.T) {
	t.Parallel()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != feishuAppRegistrationPath {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, r.Form.Encode())
		switch r.Form.Get("action") {
		case "init":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "begin":
			if r.Form.Get("archetype") != "PersonalAgent" || r.Form.Get("auth_method") != "client_secret" {
				t.Fatalf("unexpected begin form: %s", r.Form.Encode())
			}
			_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"u c","verification_uri":"https://accounts.feishu.cn/cli","expires_in":240,"interval":7}`))
		default:
			t.Fatalf("unexpected action %q", r.Form.Get("action"))
		}
	}))
	defer srv.Close()
	client, err := NewFeishuAppRegistrationClient(FeishuAppRegistrationClientOptions{
		AccountsBaseURL: srv.URL,
		OpenBaseURL:     "https://open.feishu.cn",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := client.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || !strings.Contains(bodies[0], "action=init") || !strings.Contains(bodies[1], "action=begin") {
		t.Fatalf("unexpected form sequence: %+v", bodies)
	}
	if got.DeviceCode != "dc" || got.UserCode != "u c" || got.Interval != 7 || got.ExpiresIn != 240 {
		t.Fatalf("unexpected begin result: %+v", got)
	}
	if got.VerificationURIComplete != "https://open.feishu.cn/page/cli?user_code=u+c" {
		t.Fatalf("unexpected verification complete URL: %q", got.VerificationURIComplete)
	}
}

func TestFeishuAppRegistrationPollStatusMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		body     map[string]any
		interval int
		want     FeishuAppRegistrationPollResult
	}{
		{
			name:     "pending",
			body:     map[string]any{"error": "authorization_pending"},
			interval: 5,
			want:     FeishuAppRegistrationPollResult{Kind: FeishuAppRegistrationPollPending, NextIntervalSec: 5},
		},
		{
			name:     "slow_down",
			body:     map[string]any{"error": "slow_down"},
			interval: 58,
			want:     FeishuAppRegistrationPollResult{Kind: FeishuAppRegistrationPollPending, NextIntervalSec: 60},
		},
		{
			name: "success",
			body: map[string]any{
				"client_id":     "cli_done",
				"client_secret": "secret",
				"user_info":     map[string]any{"open_id": "ou_admin", "tenant_brand": "feishu"},
			},
			interval: 5,
			want: FeishuAppRegistrationPollResult{
				Kind:         FeishuAppRegistrationPollSuccess,
				ClientID:     "cli_done",
				ClientSecret: "secret",
				AdminOpenID:  "ou_admin",
				TenantBrand:  "feishu",
			},
		},
		{
			name:     "denied",
			body:     map[string]any{"error": "access_denied"},
			interval: 5,
			want:     FeishuAppRegistrationPollResult{Kind: FeishuAppRegistrationPollError, Error: "access_denied", Description: "app registration denied by user"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if r.Form.Get("action") != "poll" || r.Form.Get("device_code") != "dc" {
					t.Fatalf("unexpected poll form: %s", r.Form.Encode())
				}
				_ = json.NewEncoder(w).Encode(tc.body)
			}))
			defer srv.Close()
			client, err := NewFeishuAppRegistrationClient(FeishuAppRegistrationClientOptions{AccountsBaseURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			got, err := client.Poll(context.Background(), "dc", tc.interval, "")
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
