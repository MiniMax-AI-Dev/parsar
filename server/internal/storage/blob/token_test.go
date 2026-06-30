package blob

import (
	"testing"
	"time"
)

func TestProxyTokenSignVerifyRoundTrip(t *testing.T) {
	s := NewProxySigner("master-key-123")
	tok, err := s.Sign(ProxyClaims{Ref: "pg:abc", WorkspaceID: "ws-1", Method: "PUT"}, time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	claims, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Ref != "pg:abc" || claims.WorkspaceID != "ws-1" || claims.Method != "PUT" {
		t.Fatalf("claims round-trip mismatch: %+v", claims)
	}
}

func TestProxyTokenRejectsTamper(t *testing.T) {
	s := NewProxySigner("master-key-123")
	tok, _ := s.Sign(ProxyClaims{Ref: "pg:abc", WorkspaceID: "ws-1", Method: "GET"}, time.Minute)
	if _, err := s.Verify(tok + "x"); err == nil {
		t.Fatal("expected signature error on tampered token")
	}
}

func TestProxyTokenRejectsExpired(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	s := NewProxySigner("master-key-123")
	s.now = func() time.Time { return base }
	tok, _ := s.Sign(ProxyClaims{Ref: "pg:abc", WorkspaceID: "ws-1", Method: "GET"}, time.Minute)
	s.now = func() time.Time { return base.Add(2 * time.Minute) }
	if _, err := s.Verify(tok); err != ErrTokenExpired {
		t.Fatalf("want ErrTokenExpired, got %v", err)
	}
}

func TestProxyTokenWrongKeyFails(t *testing.T) {
	tok, _ := NewProxySigner("key-a").Sign(ProxyClaims{Ref: "pg:abc", WorkspaceID: "ws-1", Method: "GET"}, time.Minute)
	if _, err := NewProxySigner("key-b").Verify(tok); err == nil {
		t.Fatal("token signed with key-a must not verify under key-b")
	}
}

func TestProxySignerEmptyKeyDisabled(t *testing.T) {
	s := NewProxySigner("")
	if s.Enabled() {
		t.Fatal("empty key signer must report disabled")
	}
	if _, err := s.Sign(ProxyClaims{Ref: "pg:abc", WorkspaceID: "ws-1", Method: "GET"}, time.Minute); err != ErrSignerNotConfigured {
		t.Fatalf("want ErrSignerNotConfigured, got %v", err)
	}
}
