package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

type rawFilesCancellationProof struct {
	Interrupt struct {
		RequestID int             `json:"request_id"`
		Response  json.RawMessage `json:"response"`
	} `json:"interrupt"`
	TurnCompleted    json.RawMessage `json:"turn_completed"`
	CommandStarted   json.RawMessage `json:"command_started"`
	CommandCompleted json.RawMessage `json:"command_completed"`
	BackgroundBefore json.RawMessage `json:"background_before"`
	Termination      *struct {
		RequestID int             `json:"request_id"`
		ProcessID string          `json:"process_id"`
		Response  json.RawMessage `json:"response"`
	} `json:"termination"`
	BackgroundAfter json.RawMessage `json:"background_after"`
	FilesAfter      struct {
		BinaryVerified bool `json:"binary_verified"`
		MarkerVerified bool `json:"marker_verified"`
	} `json:"files_after"`
}

type rawFilesRecoveryProof struct {
	NativeTurns   json.RawMessage `json:"native_turns"`
	FilesVerified bool            `json:"files_verified"`
}

func TestNativeRawEnvironmentFilesCancellation(t *testing.T) {
	testNativeSharedEnvironmentFiles(t, sharedFilesProfile{
		probeVariable: "PARSAR_RAW_FILES_PROBE", status: "raw_native_files_characterized",
		transport: "raw_unix_socket", withCancellation: true,
		limitations: "Private first/cancel/fresh Files composition only. Native typed observations may omit or delay interrupted command results. Observed PID exit and stable heartbeat do not establish all-descendant OS quiescence. Public Files, ownership/admission and unbounded native-client backpressure remain open.",
	})
}

func assertRawFilesCancelActive(t *testing.T, ctx context.Context, container, local string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(local, "cancel.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		t.Fatal("invalid active command PID")
	}
	command := exec.CommandContext(ctx, "docker", "exec", container, "sh", "-c", `kill -0 "$1"`, "--", strconv.Itoa(pid))
	if err := command.Run(); err != nil {
		t.Fatal("owned remote command is not active", err)
	}
	before, err := os.ReadFile(filepath.Join(local, "cancel.heartbeat"))
	if err != nil {
		t.Fatal(err)
	}
	awaitDaemonRemoteCondition(t, ctx, 5*time.Second, "active cancel heartbeat", func() bool {
		after, err := os.ReadFile(filepath.Join(local, "cancel.heartbeat"))
		return err == nil && len(after) > 0 && !bytes.Equal(before, after)
	})
}

func assertRawFilesCancellation(t *testing.T, proof sharedFilesProbeProof, workspace, local string) {
	t.Helper()
	cancel := proof.Cancellation
	if cancel == nil || proof.TurnStartedCount != 1 || proof.TurnCompletedCount != 1 || proof.CommandStartedCount != 1 || proof.CommandCompletedCount < 0 || proof.CommandCompletedCount > 1 {
		t.Fatal("cancel proof lacks actual native lifecycle observations")
	}
	var response map[string]json.RawMessage
	if json.Unmarshal(cancel.Interrupt.Response, &response) != nil || response == nil || len(response) != 0 || cancel.Interrupt.RequestID != 3 {
		t.Fatal("native interrupt acknowledgement is missing")
	}
	var ended struct {
		ThreadID string                      `json:"threadId"`
		Turn     struct{ ID, Status string } `json:"turn"`
	}
	if json.Unmarshal(cancel.TurnCompleted, &ended) != nil || ended.ThreadID != proof.NativeThreadID || ended.Turn.ID != proof.NativeTurnID || ended.Turn.Status != "interrupted" {
		t.Fatal("native cancellation was not independently observed")
	}
	var started, completed struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Item     struct {
			Type, ID, Command, Cwd string
			ProcessID              string `json:"processId"`
		} `json:"item"`
	}
	if json.Unmarshal(cancel.CommandStarted, &started) != nil || started.ThreadID != proof.NativeThreadID || started.TurnID != proof.NativeTurnID || started.Item.Type != "commandExecution" || started.Item.ID == "" || started.Item.ProcessID == "" || started.Item.Cwd != workspace || !strings.Contains(started.Item.Command, "./shared-gate.sh cancel") {
		t.Fatal("cancel target lacks its native command identity")
	}
	if proof.CommandCompletedCount == 0 {
		if string(cancel.CommandCompleted) != "null" {
			t.Fatal("missing interrupted command result was invented")
		}
	} else if json.Unmarshal(cancel.CommandCompleted, &completed) != nil || completed.ThreadID != started.ThreadID || completed.TurnID != started.TurnID || completed.Item.ID != started.Item.ID || (completed.Item.ProcessID != "" && completed.Item.ProcessID != started.Item.ProcessID) {
		t.Fatal("interrupted command result changed native identity")
	}
	var before, after struct {
		Data []struct {
			ItemID, ProcessID, Command, Cwd string
		} `json:"data"`
		NextCursor *string `json:"nextCursor"`
	}
	if json.Unmarshal(cancel.BackgroundBefore, &before) != nil || json.Unmarshal(cancel.BackgroundAfter, &after) != nil || before.NextCursor != nil || after.NextCursor != nil || len(before.Data) > 1 || len(after.Data) != 0 {
		t.Fatal("bounded cancellation terminal inventory is incomplete")
	}
	if len(before.Data) == 0 {
		if cancel.Termination != nil {
			t.Fatal("absent native command was terminated again")
		}
	} else {
		target := before.Data[0]
		var receipt struct{ Terminated bool }
		// Native hook text and rendered argv may differ; the Rust fixture checks
		// both with the existing shell parser. Preserve both original bodies here.
		if target.ItemID != started.Item.ID || target.ProcessID != started.Item.ProcessID || target.Command == "" || target.Cwd != workspace || cancel.Termination == nil || cancel.Termination.RequestID != 5 || cancel.Termination.ProcessID != target.ProcessID || json.Unmarshal(cancel.Termination.Response, &receipt) != nil || !receipt.Terminated {
			t.Fatal("native termination lacks exact ownership and acknowledgement")
		}
	}
	files := proof.Files
	if !cancel.FilesAfter.BinaryVerified || !cancel.FilesAfter.MarkerVerified || !files.ActiveBinaryVerified || files.ActiveHeartbeat == "" || files.BinaryBytes != 128*1024 || files.MetadataSize != 128*1024 || files.BinarySHA256 != sharedFilesHash(t, filepath.Join(local, "shared-binary.bin")) {
		t.Fatal("typed Files were not retained across native cancellation")
	}
	if !slices.Contains(files.ActiveDirectoryNames, "shared-binary.bin") || !slices.Contains(files.DirectoryNames, "cancel-marker.txt") || !slices.Contains(files.DirectoryNames, "shared-binary.bin") {
		t.Fatal("typed directory observations omitted retained files")
	}
	marker, err := os.ReadFile(filepath.Join(local, "cancel-marker.txt"))
	if err != nil || string(marker) != proof.Marker+"\n" {
		t.Fatal("post-cancel typed file differs on the executor", err)
	}
	for _, name := range []string{"cancel.release", "cancel-artifact.txt"} {
		if _, err := os.Stat(filepath.Join(local, name)); !os.IsNotExist(err) {
			t.Fatal("cancelled gate was released or produced a completed artifact", name)
		}
	}
}

func assertRawFilesRecovery(t *testing.T, proof, cancelled sharedFilesProbeProof, local string) {
	t.Helper()
	if proof.CancellationRecovery == nil || !proof.CancellationRecovery.FilesVerified {
		t.Fatal("cold native cancellation recovery was not verified")
	}
	var page struct {
		Data       []struct{ ID, Status string } `json:"data"`
		NextCursor *string                       `json:"nextCursor"`
	}
	if json.Unmarshal(proof.CancellationRecovery.NativeTurns, &page) != nil || page.NextCursor != nil || len(page.Data) != 2 {
		t.Fatal("cold native history omitted a prior Turn")
	}
	found := 0
	for _, turn := range page.Data {
		if turn.ID == cancelled.NativeTurnID && turn.Status == "interrupted" {
			found++
		}
	}
	marker, err := os.ReadFile(filepath.Join(local, "cancel-marker.txt"))
	if found != 1 || err != nil || string(marker) != cancelled.Marker+"\n" {
		t.Fatal("cold native history or post-cancel file changed")
	}
}
