package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestWorkspaceDirectorySharesReadCorrelationAndFrameBound(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
	defer s.Close("test")
	request := proto.WorkspaceReadPayload{Handle: "prepared", EnvironmentID: "environment", MaxEntries: proto.WorkspaceDirectoryMaxEntries}
	done := make(chan error, 1)
	go func() {
		_, err := s.ListWorkspaceDirectory(t.Context(), request)
		done <- err
	}()
	message := <-s.sendCh
	var sent proto.WorkspaceReadPayload
	if message.Type != proto.TypeWorkspaceRead || message.DecodePayload(&sent) != nil || sent.Operation != "directory" || sent.MaxBytes != 0 || sent.MaxEntries != request.MaxEntries {
		t.Fatal("directory request changed")
	}
	directory := &proto.WorkspaceDirectoryResult{Entries: make([]proto.WorkspaceDirectoryEntry, request.MaxEntries), Truncated: true}
	for i := range directory.Entries {
		directory.Entries[i] = proto.WorkspaceDirectoryEntry{Name: strings.Repeat("\x01", 250) + fmt.Sprintf("%04d", i), Kind: "directory"}
	}
	result := proto.WorkspaceReadResultPayload{Outcome: "completed", CloseAcknowledged: true, Directory: directory}
	reply, _ := proto.NewEnvelope(proto.TypeWorkspaceReadResult, message.ID, result)
	encoded, err := json.Marshal(reply)
	if err != nil || int64(len(encoded)) >= ReadLimit {
		t.Fatal("directory result exceeds frame", len(encoded), err)
	}
	s.dispatch(reply)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceDirectoryRejectsContradictoryAndUnboundedMetadata(t *testing.T) {
	request := proto.WorkspaceReadPayload{Operation: "directory", MaxEntries: 2}
	size := int64(0)
	valid := proto.WorkspaceDirectoryEntry{Name: "file", Kind: "file", SizeBytes: &size}
	for _, entries := range [][]proto.WorkspaceDirectoryEntry{
		nil, {valid, valid}, {{Name: "../other", Kind: "directory"}},
		{{Name: "file", Kind: "file"}}, {{Name: "dir", Kind: "directory", SizeBytes: &size}},
		{{Name: strings.Repeat("x", 256), Kind: "directory"}},
	} {
		result := proto.WorkspaceReadResultPayload{Outcome: "completed", CloseAcknowledged: true, Directory: &proto.WorkspaceDirectoryResult{Entries: entries}}
		if validWorkspaceOperationResult(result, request) {
			t.Fatal("invalid directory metadata accepted")
		}
	}
	result := proto.WorkspaceReadResultPayload{Outcome: "completed", CloseAcknowledged: true, Directory: &proto.WorkspaceDirectoryResult{Entries: []proto.WorkspaceDirectoryEntry{valid}}}
	if !validWorkspaceOperationResult(result, request) || validWorkspaceReadResult(result, 1024) {
		t.Fatal("byte and directory result contracts mixed")
	}
	result.Data = []byte("unexpected bytes")
	if validWorkspaceOperationResult(result, request) {
		t.Fatal("contradictory result accepted")
	}
}

func TestWorkspaceDirectoryRequiresExplicitWireTruncation(t *testing.T) {
	for _, tc := range []struct {
		name, directory string
		valid           bool
	}{
		{"omitted", `{"entries":[]}`, false},
		{"null", `{"entries":[],"truncated":null}`, false},
		{"wrong_type", `{"entries":[],"truncated":"false"}`, false},
		{"complete", `{"entries":[],"truncated":false}`, true},
		{"truncated", `{"entries":[{"name":"dir","kind":"directory","size_bytes":null}],"truncated":true}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
			defer s.Close("test")
			done := make(chan error, 1)
			go func() {
				_, err := s.ListWorkspaceDirectory(t.Context(), proto.WorkspaceReadPayload{Handle: "prepared", EnvironmentID: "environment", MaxEntries: 1})
				done <- err
			}()
			request := <-s.sendCh
			reply := proto.Envelope{Type: proto.TypeWorkspaceReadResult, ID: request.ID,
				Payload: json.RawMessage(`{"outcome":"completed","close_acknowledged":true,"directory":` + tc.directory + `}`)}
			s.dispatch(reply)
			if err := <-done; (err == nil) != tc.valid {
				t.Fatal("directory wire validation differs", err)
			}
		})
	}
}
