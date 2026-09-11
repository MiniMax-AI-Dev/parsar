package dev

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type agentAuthoringService struct {
	store skillUploadStore
	blobs blob.Store
}

// NewAgentAuthoringHandler serves only commands attributed to an authenticated daemon's active run.
func NewAgentAuthoringHandler(s skillUploadStore, blobs blob.Store) func(context.Context, string, proto.AuthoringRequestPayload) (any, error) {
	return (&agentAuthoringService{store: s, blobs: blobs}).handle
}

func (s *agentAuthoringService) handle(ctx context.Context, runID string, request proto.AuthoringRequestPayload) (any, error) {
	if len(request.Content) > proto.AuthoringMaxBytes {
		return nil, errors.New("authoring content exceeds 1 MiB")
	}
	run, err := s.store.GetAgentRunInvocation(ctx, runID)
	if err != nil || run.Status != "running" || run.RequestedByType != "user" || run.RequestedByID == "" {
		return nil, errors.New("workspace commands require an active user-requested run")
	}
	ctx = auth.WithUserID(ctx, run.RequestedByID)
	if err := auth.RequireWorkspaceRole(ctx, s.store, run.WorkspaceID, "owner", "admin", "member", "viewer"); err != nil {
		return nil, err
	}
	agent, err := s.store.GetAgent(ctx, run.AgentID)
	if err != nil || agent.WorkspaceID != run.WorkspaceID {
		return nil, store.ErrUnknownAgent
	}
	write := request.Operation == proto.AuthoringSkillCreate || request.Operation == proto.AuthoringSkillUpdate || request.Operation == proto.AuthoringPromptWrite
	if write {
		if err := auth.RequireWorkspaceRole(ctx, s.store, run.WorkspaceID, "owner", "admin"); err != nil {
			return nil, err
		}
	}
	switch request.Operation {
	case proto.AuthoringContext:
		operations := []string{proto.AuthoringContext, proto.AuthoringSkillList, proto.AuthoringSkillRead, proto.AuthoringPromptRead}
		if auth.RequireWorkspaceRole(ctx, s.store, run.WorkspaceID, "owner", "admin") == nil {
			operations = append(operations, proto.AuthoringSkillCreate, proto.AuthoringSkillUpdate, proto.AuthoringPromptWrite)
		}
		return map[string]any{"workspace_id": run.WorkspaceID, "agent_id": agent.ID, "agent_name": agent.Name, "run_id": runID, "requester_id": run.RequestedByID, "allowed_operations": operations}, nil
	case proto.AuthoringSkillList:
		capabilities, err := s.store.ListCapabilities(ctx, run.WorkspaceID, store.ListCapabilityFilter{Type: "skill"})
		if err != nil {
			return nil, err
		}
		items := make([]map[string]string, 0, len(capabilities))
		for _, capability := range capabilities {
			items = append(items, map[string]string{"id": capability.ID, "name": capability.Name, "description": capability.Description, "version": capability.LatestVersion})
		}
		return items, nil
	case proto.AuthoringSkillRead:
		capability, spec, err := s.readSkill(ctx, run.WorkspaceID, request.CapabilityID)
		if err != nil {
			return nil, err
		}
		files := make([]string, 0, len(spec.Skill.Files))
		for _, file := range spec.Skill.Files {
			files = append(files, file.Path)
		}
		markdown, err := s.readSkillMarkdown(ctx, capability.LatestVersionID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": capability.ID, "name": capability.Name, "version": capability.LatestVersion, "description": spec.Skill.Description, "instructions": spec.Skill.Instruction, "markdown": markdown, "files": files}, nil
	case proto.AuthoringSkillCreate, proto.AuthoringSkillUpdate:
		return s.writeSkill(ctx, run, request)
	case proto.AuthoringPromptRead:
		instructions, _ := agent.Config["system_prompt"].(string)
		return map[string]string{"agent_id": agent.ID, "instructions": instructions}, nil
	case proto.AuthoringPromptWrite:
		instructions := strings.TrimSpace(request.Content)
		_, _, err := s.store.UpdateAgent(ctx, store.UpdateAgentInput{AgentID: agent.ID, ActorID: run.RequestedByID, SystemPrompt: &instructions})
		if err != nil {
			return nil, err
		}
		return map[string]string{"agent_id": agent.ID, "instructions": instructions, "applies": "next turn"}, nil
	default:
		return nil, fmt.Errorf("unsupported workspace operation %q", request.Operation)
	}
}
