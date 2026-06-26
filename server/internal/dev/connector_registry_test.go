package dev

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
)

func testConnectorRegistry(t *testing.T, conn connector.AgentConnector) *connector.Registry {
	t.Helper()
	reg := connector.NewRegistry()
	if err := reg.Register(conn); err != nil {
		t.Fatal(err)
	}
	return reg
}
