package store_test

import (
	"archive/tar"
	"bytes"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func completeLocalArtifactExport(t *testing.T, h *dispatchHarness, environment store.Environment) {
	t.Helper()
	prepared := h.read(proto.TypeExecutionPrepare)
	var request proto.ExecutionPreparePayload
	if prepared.DecodePayload(&request) != nil || !proto.ValidWorkspaceReadPreparation(request.Configuration) || request.Configuration.LocalEnvironment == nil || request.Configuration.LocalEnvironment.ID != environment.ID {
		t.Fatal("export did not reuse the bound read-only preparation")
	}
	handle := acknowledgePreparation(h, prepared.ID)
	h.write(prepared.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	begin := h.read(proto.TypeWorkspaceExport)
	var export proto.WorkspaceExportPayload
	if begin.DecodePayload(&export) != nil || export.Step != "begin" || export.Handle != handle || export.EnvironmentID != environment.ID {
		t.Fatal("export lost preparation authority")
	}
	var data bytes.Buffer
	w := tar.NewWriter(&data)
	if err := w.WriteHeader(&tar.Header{Name: "outputs/result.bin", Size: 3, Mode: 0600, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte{0, 255, 1}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	h.write(begin.ID, proto.TypeWorkspaceExportResult, proto.WorkspaceExportResultPayload{Outcome: "chunk", Data: data.Bytes()})
	next := h.read(proto.TypeWorkspaceExport)
	if next.DecodePayload(&export) != nil || export.Step != "next" || export.Offset != int64(data.Len()) {
		t.Fatal("export did not await native completion")
	}
	page, err := h.s.ListSessionArtifacts(t.Context(), h.tenant, h.session.ID, "", "", 20, false)
	if err != nil || len(page.Artifacts) != 0 {
		t.Fatal("capture published before native completion", err)
	}
	h.write(begin.ID, proto.TypeWorkspaceExportResult, proto.WorkspaceExportResultPayload{Outcome: "completed", Offset: export.Offset})
	release := h.read(proto.TypeExecutionRelease)
	var close proto.ExecutionReleasePayload
	if release.DecodePayload(&close) != nil || release.ID != prepared.ID || close.Handle != handle {
		t.Fatal("export did not release its own read preparation")
	}
	h.write(prepared.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "released"})
}
