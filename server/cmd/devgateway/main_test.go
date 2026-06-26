package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDrainOutboundFeishuSuccessAcksAfterSend(t *testing.T) {
	var sent bool
	var acked bool
	var ackDeliveryID string
	parsarServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/dev/gateway/outbound":
			writeTestJSON(t, w, map[string]any{
				"gateway": "feishu",
				"deliveries": []map[string]any{{
					"message_id":       "00000000-0000-0000-0000-000000000202",
					"gateway":          "feishu",
					"external_chat_id": "oc_demo",
					"text":             "agent output",
					"delivery_key":     "00000000-0000-0000-0000-000000000202",
				}},
				"messages": []map[string]any{},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/dev/gateway/outbound/00000000-0000-0000-0000-000000000202/delivered":
			acked = true
			var body struct {
				DeliveryID string `json:"delivery_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode ack: %v", err)
			}
			ackDeliveryID = body.DeliveryID
			writeTestJSON(t, w, map[string]any{"ok": true})
		default:
			t.Fatalf("unexpected Parsar request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer parsarServer.Close()

	feishuServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/open-apis/im/v1/messages" {
			t.Fatalf("unexpected Feishu request: %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("unexpected authorization header: %s", r.Header.Get("Authorization"))
		}
		var payload struct {
			ReceiveID string `json:"receive_id"`
			Content   string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode feishu payload: %v", err)
		}
		if payload.ReceiveID != "oc_demo" || payload.Content != `{"text":"agent output"}` {
			t.Fatalf("unexpected feishu payload: %+v", payload)
		}
		sent = true
		writeTestJSON(t, w, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"message_id": "om_feishu_1"}})
	}))
	defer feishuServer.Close()

	err := drainOutbound(context.Background(), []string{"--api-url", parsarServer.URL, "--gateway", "feishu", "--mode", "feishu", "--feishu-base-url", feishuServer.URL, "--feishu-token", "test-token"})
	if err != nil {
		t.Fatalf("drain outbound: %v", err)
	}
	if !sent || !acked {
		t.Fatalf("expected sent and acked, sent=%v acked=%v", sent, acked)
	}
	if ackDeliveryID != "om_feishu_1" {
		t.Fatalf("expected Feishu message id as ack delivery id, got %s", ackDeliveryID)
	}
}

func TestDrainOutboundFeishuNon2xxDoesNotAck(t *testing.T) {
	var acked bool
	parsarServer := gatewayTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		acked = true
		writeTestJSON(t, w, map[string]any{"ok": true})
	})
	defer parsarServer.Close()
	feishuServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fail", http.StatusBadGateway)
	}))
	defer feishuServer.Close()

	err := drainOutbound(context.Background(), []string{"--api-url", parsarServer.URL, "--gateway", "feishu", "--mode", "feishu", "--feishu-base-url", feishuServer.URL, "--feishu-token", "test-token"})
	if err == nil || !strings.Contains(err.Error(), "non-2xx") {
		t.Fatalf("expected non-2xx error, got %v", err)
	}
	if acked {
		t.Fatal("did not expect ack after Feishu non-2xx")
	}
}

func TestDrainOutboundFeishuNon2xxDoesNotLeakTokenOrAck(t *testing.T) {
	secretToken := "secret-token-value"
	var acked bool
	parsarServer := gatewayTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		acked = true
		writeTestJSON(t, w, map[string]any{"ok": true})
	})
	defer parsarServer.Close()
	feishuServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "reflected Authorization: Bearer "+secretToken, http.StatusBadGateway)
	}))
	defer feishuServer.Close()

	err := drainOutbound(context.Background(), []string{"--api-url", parsarServer.URL, "--gateway", "feishu", "--mode", "feishu", "--feishu-base-url", feishuServer.URL, "--feishu-token", secretToken})
	if err == nil || !strings.Contains(err.Error(), "non-2xx") {
		t.Fatalf("expected non-2xx error, got %v", err)
	}
	errText := err.Error()
	if strings.Contains(errText, secretToken) || strings.Contains(errText, "Bearer") || strings.Contains(errText, "Authorization") {
		t.Fatalf("error leaked sensitive response body: %s", errText)
	}
	if acked {
		t.Fatal("did not expect ack after Feishu non-2xx")
	}
}

func TestDrainOutboundFeishuInvalidResponseDoesNotAck(t *testing.T) {
	var acked bool
	parsarServer := gatewayTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		acked = true
		writeTestJSON(t, w, map[string]any{"ok": true})
	})
	defer parsarServer.Close()
	feishuServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{}}`))
	}))
	defer feishuServer.Close()

	err := drainOutbound(context.Background(), []string{"--api-url", parsarServer.URL, "--gateway", "feishu", "--mode", "feishu", "--feishu-base-url", feishuServer.URL, "--feishu-token", "test-token"})
	if err == nil || !strings.Contains(err.Error(), "invalid response") {
		t.Fatalf("expected invalid response error, got %v", err)
	}
	if acked {
		t.Fatal("did not expect ack after invalid Feishu response")
	}
}

func gatewayTestServer(t *testing.T, ackHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/dev/gateway/outbound":
			writeTestJSON(t, w, map[string]any{
				"gateway": "feishu",
				"deliveries": []map[string]any{{
					"message_id":       "00000000-0000-0000-0000-000000000202",
					"gateway":          "feishu",
					"external_chat_id": "oc_demo",
					"text":             "agent output",
					"delivery_key":     "00000000-0000-0000-0000-000000000202",
				}},
				"messages": []map[string]any{},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/delivered"):
			ackHandler(w, r)
		default:
			t.Fatalf("unexpected Parsar request: %s %s", r.Method, r.URL.String())
		}
	}))
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write json: %v", err)
	}
}
