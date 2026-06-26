package agentdaemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/binding"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// DeviceOwnerResolver is the read side of the agent_daemon owner table.
// Production wiring passes *store.Store; tests can omit and keep the
// single-pod in-memory Registry behavior.
type DeviceOwnerResolver interface {
	GetAgentDaemonDeviceOwner(ctx context.Context, deviceID string) (store.AgentDaemonDeviceOwnerRead, bool, error)
}

// RemoteStreamer forwards a prompt to the pod that currently owns the
// device WebSocket and streams PromptEvents back.
type RemoteStreamer interface {
	StreamPromptRemote(ctx context.Context, owner store.AgentDaemonDeviceOwnerRead, in connector.PromptInput) (<-chan connector.PromptEvent, error)
}

// RemoteSubmitter forwards Submit-style decisions (no stream) to the
// pod that currently owns the device WebSocket. Permission and
// PromptForUserChoice both flow through here so a feishu webhook that
// lands on a non-owner pod can still deliver the verdict.
type RemoteSubmitter interface {
	SubmitPermissionRemote(ctx context.Context, owner store.AgentDaemonDeviceOwnerRead, decision connector.PermissionDecision) error
	SubmitPromptForUserChoiceRemote(ctx context.Context, owner store.AgentDaemonDeviceOwnerRead, decision connector.PromptForUserChoiceDecision) error
}

// SubmitSlotResolver is the narrow read surface SubmitPermission /
// SubmitPromptForUserChoice need to translate a request id back to the
// owning device — the wire-level decision does NOT carry device_id, so
// the non-owner pod that receives the feishu webhook has to look it up
// before it can owner-route. *store.Store satisfies this via the
// existing FindConversationBy* methods reading the inflight slot stamped
// by the outbound driver.
type SubmitSlotResolver interface {
	DeviceIDForPermissionRequest(ctx context.Context, requestID string) (string, error)
	DeviceIDForPromptForUserChoiceRequest(ctx context.Context, requestID string) (string, error)
}

// submitOwnerOutcome captures whether SubmitPermission /
// SubmitPromptForUserChoice should run locally on this pod or forward
// to a remote owner. The handful of named fields keep the call sites
// readable; the alternative was a triple return that the caller would
// have to remember by position.
type submitOwnerOutcome struct {
	// Remote is non-nil when the decision must be forwarded to another
	// pod. Local is true when the caller should run the in-process
	// registry lookup path.
	Remote *store.AgentDaemonDeviceOwnerRead
	Local  bool
}

// resolveOwnerForSubmit decides where a Submit-style RPC should be
// handled given a device id. Three return shapes:
//
//   - Local: run the existing in-process registry lookup. Either there
//     is no owner row, the lease is expired, or this pod is the owner.
//   - Remote != nil: forward to owner.OwnerURL via a RemoteSubmitter.
//   - error: lookup itself failed (DB transient).
//
// Mirrors routeRemoteIfNeeded but stays decision-only — no channels,
// no `allowRemote` knob, no protocol coupling. Callers do the dispatch.
func (c *Connector) resolveOwnerForSubmit(ctx context.Context, requestID, deviceID, kind string) (submitOwnerOutcome, error) {
	if c.ownerResolver == nil || strings.TrimSpace(deviceID) == "" {
		c.log.Info("agent_daemon: submit owner routing skipped (no resolver or empty device_id)",
			"kind", kind, "request_id", requestID, "device_id", deviceID, "has_resolver", c.ownerResolver != nil)
		return submitOwnerOutcome{Local: true}, nil
	}
	owner, ok, err := c.ownerResolver.GetAgentDaemonDeviceOwner(ctx, deviceID)
	if err != nil {
		c.log.Error("agent_daemon: submit owner resolve failed",
			"kind", kind, "request_id", requestID, "device_id", deviceID, "err", err.Error())
		return submitOwnerOutcome{}, fmt.Errorf("agent_daemon: resolve device owner: %w", err)
	}
	if !ok || owner.Status != store.AgentDaemonOwnerStatusConnected || !owner.LeaseExpiresAt.After(time.Now().UTC()) {
		// No live lease — fall through to local registry lookup. The
		// local LookupPermission/LookupPromptForUserChoice will return
		// the appropriate NotRegistered error if we are not the owner.
		c.log.Warn("agent_daemon: no live owner lease found for device — will try local registry",
			"kind", kind,
			"request_id", requestID,
			"device_id", deviceID,
			"row_found", ok,
			"status", string(owner.Status),
			"lease_expires_at", owner.LeaseExpiresAt,
			"this_pod_id", c.ownerPodID)
		return submitOwnerOutcome{Local: true}, nil
	}
	if strings.TrimSpace(owner.OwnerPodID) == "" || owner.OwnerPodID == c.ownerPodID {
		c.log.Info("agent_daemon: submit device owner is THIS pod — handling locally",
			"kind", kind, "request_id", requestID, "device_id", deviceID, "this_pod_id", c.ownerPodID)
		return submitOwnerOutcome{Local: true}, nil
	}
	c.log.Info("agent_daemon: forwarding submit to remote owner pod",
		"kind", kind, "request_id", requestID, "device_id", deviceID,
		"owner_pod_id", owner.OwnerPodID, "owner_url", owner.OwnerURL, "this_pod_id", c.ownerPodID)
	return submitOwnerOutcome{Remote: &owner}, nil
}

func (c *Connector) routeRemoteIfNeeded(ctx context.Context, bind binding.Binding, in connector.PromptInput, allowRemote bool) (<-chan connector.PromptEvent, bool, error) {
	if c.ownerResolver == nil || strings.TrimSpace(bind.DeviceID) == "" {
		c.log.Info("agent_daemon: owner routing skipped (no resolver or empty device_id)",
			"run_id", in.RunID, "device_id", bind.DeviceID, "has_resolver", c.ownerResolver != nil)
		return nil, false, nil
	}
	owner, ok, err := c.ownerResolver.GetAgentDaemonDeviceOwner(ctx, bind.DeviceID)
	if err != nil {
		c.log.Error("agent_daemon: owner resolve failed",
			"run_id", in.RunID, "device_id", bind.DeviceID, "err", err.Error())
		return nil, false, fmt.Errorf("agent_daemon: resolve device owner: %w", err)
	}
	if !ok || owner.Status != store.AgentDaemonOwnerStatusConnected || !owner.LeaseExpiresAt.After(time.Now().UTC()) {
		// No live lease — fall through to local registry lookup.
		c.log.Warn("agent_daemon: no live owner lease found for device — will try local registry",
			"run_id", in.RunID,
			"device_id", bind.DeviceID,
			"row_found", ok,
			"status", string(owner.Status),
			"lease_expires_at", owner.LeaseExpiresAt,
			"this_pod_id", c.ownerPodID)
		return nil, false, nil
	}
	if strings.TrimSpace(owner.OwnerPodID) == "" || owner.OwnerPodID == c.ownerPodID {
		c.log.Info("agent_daemon: device owner is THIS pod — handling locally",
			"run_id", in.RunID, "device_id", bind.DeviceID, "this_pod_id", c.ownerPodID)
		return nil, false, nil
	}
	if !allowRemote {
		c.log.Warn("agent_daemon: device owned by remote pod but allowRemote=false",
			"run_id", in.RunID, "device_id", bind.DeviceID, "owner_pod_id", owner.OwnerPodID, "this_pod_id", c.ownerPodID)
		return errorChannel(in.RunID, fmt.Sprintf("agent_daemon device %s is owned by remote pod %s", bind.DeviceID, owner.OwnerPodID)), true, nil
	}
	if c.remote == nil {
		c.log.Error("agent_daemon: device owned by remote pod but remote streamer is nil",
			"run_id", in.RunID, "device_id", bind.DeviceID, "owner_pod_id", owner.OwnerPodID)
		return errorChannel(in.RunID, fmt.Sprintf("agent_daemon device %s is owned by remote pod %s but remote routing is not configured", bind.DeviceID, owner.OwnerPodID)), true, nil
	}
	c.log.Info("agent_daemon: forwarding prompt to remote owner pod",
		"run_id", in.RunID, "device_id", bind.DeviceID, "owner_pod_id", owner.OwnerPodID, "owner_url", owner.OwnerURL, "this_pod_id", c.ownerPodID)
	ch, err := c.remote.StreamPromptRemote(ctx, owner, in)
	if err != nil {
		c.log.Error("agent_daemon: remote.StreamPromptRemote failed",
			"run_id", in.RunID, "owner_pod_id", owner.OwnerPodID, "owner_url", owner.OwnerURL, "err", err.Error())
		return nil, false, err
	}
	c.log.Info("agent_daemon: remote forward established",
		"run_id", in.RunID, "owner_pod_id", owner.OwnerPodID)
	return ch, true, nil
}
