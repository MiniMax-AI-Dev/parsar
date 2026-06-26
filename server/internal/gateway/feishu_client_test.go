package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFeishuClientSendDelivery(t *testing.T) {
	var gotAuth string
	var gotPath string
	var gotReceiveIDType string
	var gotPayload FeishuSendMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotReceiveIDType = r.URL.Query().Get("receive_id_type")
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"message_id":"om_feishu_1"}}`))
	}))
	defer server.Close()

	client, err := NewFeishuClient(FeishuClientOptions{BaseURL: server.URL, Token: "tenant-token"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	response, err := client.SendDelivery(context.Background(), Delivery{ExternalChatID: "oc_demo", Text: "agent output"})
	if err != nil {
		t.Fatalf("send delivery: %v", err)
	}
	if response.MessageID != "om_feishu_1" {
		t.Fatalf("unexpected response: %+v", response)
	}
	if gotAuth != "Bearer tenant-token" {
		t.Fatalf("unexpected auth header: %s", gotAuth)
	}
	if gotPath != "/open-apis/im/v1/messages" || gotReceiveIDType != "chat_id" {
		t.Fatalf("unexpected endpoint: path=%s receive_id_type=%s", gotPath, gotReceiveIDType)
	}
	if gotPayload.ReceiveIDType != "chat_id" || gotPayload.ReceiveID != "oc_demo" || gotPayload.MsgType != "text" || gotPayload.Content != `{"text":"agent output"}` {
		t.Fatalf("unexpected payload: %+v", gotPayload)
	}
}

func TestFeishuClientSendDeliveryErrors(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		wantErr error
	}{
		{
			name: "non 2xx",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "bad gateway", http.StatusBadGateway)
			},
			wantErr: ErrFeishuNon2xx,
		},
		{
			name: "invalid json",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`not json`))
			},
			wantErr: ErrFeishuInvalidResponse,
		},
		{
			name: "missing message id",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{}}`))
			},
			wantErr: ErrFeishuInvalidResponse,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()
			client, err := NewFeishuClient(FeishuClientOptions{BaseURL: server.URL, Token: "tenant-token"})
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			_, err = client.SendDelivery(context.Background(), Delivery{ExternalChatID: "oc_demo", Text: "agent output"})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestFeishuClientNon2xxDoesNotLeakTokenFromResponseBody(t *testing.T) {
	secretToken := "secret-token-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "reflected Authorization: Bearer "+secretToken, http.StatusBadGateway)
	}))
	defer server.Close()

	client, err := NewFeishuClient(FeishuClientOptions{BaseURL: server.URL, Token: secretToken})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = client.SendDelivery(context.Background(), Delivery{ExternalChatID: "oc_demo", Text: "agent output"})
	if !errors.Is(err, ErrFeishuNon2xx) {
		t.Fatalf("expected non-2xx error, got %v", err)
	}
	errText := err.Error()
	if strings.Contains(errText, secretToken) || strings.Contains(errText, "Bearer") || strings.Contains(errText, "Authorization") {
		t.Fatalf("error leaked sensitive response body: %s", errText)
	}
}
