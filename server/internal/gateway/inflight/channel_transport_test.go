package inflight

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
)

// TestOutboundTransport_DefaultSharedBot verifies the production Transport
// binding resolves the default shared bot's credentials and returns a usable
// CardSender (the cached tenant client) from the bot's app_id alone, without
// touching the store.
func TestOutboundTransport_DefaultSharedBot(t *testing.T) {
	w, err := NewWorker(Options{
		Store:            newFakeStore(),
		Secrets:          fakeDecrypter{},
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_default", AppSecret: "secret_default"},
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	sender, creds, err := w.OutboundTransport().OutboundSender(context.Background(), "cli_default")
	if err != nil {
		t.Fatalf("OutboundSender: %v", err)
	}
	if creds.AppID != "cli_default" || creds.AppSecret != "secret_default" {
		t.Fatalf("creds = %+v, want default shared bot", creds)
	}
	if sender == nil {
		t.Fatal("sender must be non-nil")
	}
}

// TestOutboundTransport_CachesClient confirms repeat calls reuse the same
// cached tenant client — the binding must not duplicate the worker's pool.
func TestOutboundTransport_CachesClient(t *testing.T) {
	w, err := NewWorker(Options{
		Store:            newFakeStore(),
		Secrets:          fakeDecrypter{},
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_default", AppSecret: "secret_default"},
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	tr := w.OutboundTransport()
	first, _, err := tr.OutboundSender(context.Background(), "cli_default")
	if err != nil {
		t.Fatalf("OutboundSender #1: %v", err)
	}
	second, _, err := tr.OutboundSender(context.Background(), "cli_default")
	if err != nil {
		t.Fatalf("OutboundSender #2: %v", err)
	}
	if first != second {
		t.Fatal("repeat OutboundSender must return the same cached client")
	}
}

// TestOutboundTransport_UnknownApp propagates resolveCredentials' dead-letter
// signal when the app_id maps to no live agent.
func TestOutboundTransport_UnknownApp(t *testing.T) {
	w, err := NewWorker(Options{Store: newFakeStore(), Secrets: fakeDecrypter{}})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	_, _, err = w.OutboundTransport().OutboundSender(context.Background(), "cli_missing")
	if err == nil {
		t.Fatal("OutboundSender must error for an unknown app_id")
	}
	if !errors.Is(err, gateway.ErrUnresolvableOutbound) {
		t.Fatalf("err = %v, want ErrUnresolvableOutbound", err)
	}
}
