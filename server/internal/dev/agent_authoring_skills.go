package dev

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/parser"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func (s *agentAuthoringService) readSkill(ctx context.Context, workspaceID, id string) (store.CapabilityRead, canonical.Spec, error) {
	if !isUUID(id) {
		return store.CapabilityRead{}, canonical.Spec{}, store.ErrUnknownCapability
	}
	capability, err := s.store.GetCapability(ctx, id)
	if err != nil || capability.WorkspaceID != workspaceID || capability.Type != "skill" || capability.DeletedAt != nil {
		return store.CapabilityRead{}, canonical.Spec{}, store.ErrUnknownCapability
	}
	if capability.LatestVersionID == "" {
		return capability, canonical.Spec{}, errors.New("Skill has no readable version")
	}
	version, err := s.store.GetCapabilityVersion(ctx, capability.LatestVersionID)
	if err != nil {
		return capability, canonical.Spec{}, err
	}
	var spec canonical.Spec
	if version.CapabilityID != capability.ID || json.Unmarshal(version.CanonicalSpec, &spec) != nil || spec.Kind != canonical.KindSkill || spec.Validate() != nil {
		return capability, spec, errors.New("Skill version has invalid content")
	}
	return capability, spec, nil
}

func (s *agentAuthoringService) writeSkill(ctx context.Context, run store.AgentRunInvocation, request proto.AuthoringRequestPayload) (any, error) {
	if request.Operation == proto.AuthoringSkillUpdate {
		_, current, err := s.readSkill(ctx, run.WorkspaceID, request.CapabilityID)
		if err != nil {
			return nil, err
		}
		if len(current.Skill.Files) != 0 {
			return nil, errors.New("this Skill has supporting files; use a ZIP version upload to preserve them")
		}
	} else if request.CapabilityID != "" {
		return nil, errors.New("skill.create does not accept a capability ID")
	}
	parsed, err := parser.ParseSkill(request.Content, parser.SourceFormatMarkdown)
	if err != nil {
		return nil, err
	}
	ref, digest, httpErr := ensureSkillImportArchive(ctx, run.WorkspaceID, parsed.Spec, "", "", s.blobs)
	if httpErr != nil {
		return nil, errors.New(httpErr.message)
	}
	source, _ := json.Marshal(map[string]string{"format": "markdown", "body": request.Content, "run_id": run.RunID})
	var result store.ImportCapabilityResult
	if request.Operation == proto.AuthoringSkillUpdate {
		result, err = s.store.ImportCapabilityVersion(ctx, store.ImportCapabilityVersionInput{
			WorkspaceID: run.WorkspaceID, CapabilityID: request.CapabilityID, CreatorID: run.RequestedByID,
			Spec: parsed.Spec, SourcePayload: source, OssKey: ref, SHA256: digest,
		})
	} else {
		result, err = s.store.ImportCapability(ctx, store.ImportCapabilityInput{
			WorkspaceID: run.WorkspaceID, CreatorID: run.RequestedByID, Name: parsed.SuggestedName,
			Description: parsed.Spec.Skill.Description, Visibility: "workspace", Type: "skill", Version: "1.0.0",
			Spec: parsed.Spec, SourcePayload: source, OssKey: ref, SHA256: digest,
		})
	}
	if err != nil {
		return nil, err
	}
	return map[string]string{"id": result.Capability.ID, "name": result.Capability.Name, "version": result.CapabilityVersion.Version, "version_id": result.CapabilityVersion.ID, "visibility": "workspace"}, nil
}
