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
