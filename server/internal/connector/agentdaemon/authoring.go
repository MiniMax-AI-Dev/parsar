package agentdaemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/gateway"
)

type AuthoringHandler func(context.Context, string, proto.AuthoringRequestPayload) (any, error)

const authoringInstructions = `## Working with this Parsar workspace

Use the parsar CLI to read and author workspace Skills and your own instructions:
- parsar workspace context: current workspace, Agent, requester and allowed operations.
- parsar skill list: workspace Skill IDs, names and current versions.
- parsar skill get <id>: read a Skill's instructions and metadata.
- parsar skill create --file /absolute/path/SKILL.md: create a workspace Skill.
- parsar skill update --file /absolute/path/SKILL.md <id>: add its next version.
- parsar agent instructions get: read your currently saved system prompt.
- parsar agent instructions set --file /absolute/path/instructions.md: replace it for future turns.

Skill Markdown needs YAML frontmatter with name and description, followed by its
instructions. This path supports single-file Skills. Keep supporting files local
and report when they require a separate ZIP upload. Creating a Skill does not
publish it or bind it to an Agent. Only change saved instructions when the user
asks you to. Read the existing instructions first and preserve unrelated content.
The CLI uses this active run's daemon connection; do not discover the server URL,
request account credentials, or use another run's socket. Permissions match the
requesting user's current workspace role; writes require owner/admin. If a write
has an uncertain outcome, check the resource before retrying. Never claim a save
succeeded until the CLI confirms it. The default sandbox includes parsar; custom
runtimes must install its companion CLI alongside parsar-daemon.`

func applyAuthoringInstructions(opts map[string]any) {
	key := "system_prompt"
	if stringFromMap(opts, "override_system_prompt") != "" {
		key = "override_system_prompt"
	}
	base := stringFromMap(opts, key)
	if base != "" {
		base += "\n\n"
	}
	opts[key] = base + authoringInstructions
}

// Bound concurrent commands without blocking lifecycle events on their I/O.
func (c *Connector) dispatchAuthoringRequest(ctx context.Context, session *gateway.Session, runID string, env proto.Envelope, slots chan struct{}) {
	select {
	case slots <- struct{}{}:
		go func() {
			defer func() { <-slots }()
			c.handleAuthoringRequest(ctx, session, runID, env, "")
		}()
	default:
		// An overloaded command must not hold up run completion either.
		replyCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
		defer cancel()
		c.handleAuthoringRequest(replyCtx, session, runID, env, "too many workspace commands; try again after a command finishes")
	}
}

func (c *Connector) handleAuthoringRequest(ctx context.Context, session *gateway.Session, runID string, env proto.Envelope, rejection string) {
	if ctx.Err() != nil {
		return
	}
	var request proto.AuthoringRequestPayload
	if env.DecodePayload(&request) != nil || request.RequestID == "" {
		return
	}
	response := proto.AuthoringResponsePayload{RequestID: request.RequestID, Error: rejection}
	if len(env.Payload) > proto.AuthoringMaxBytes || env.ID != runID || c.authoring == nil {
		response.Error = "workspace authoring is unavailable for this request"
	} else if response.Error == "" {
		workCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		data, err := c.authoring(workCtx, runID, request)
		cancel()
		if err == nil {
			response.Data, err = json.Marshal(data)
		}
		if err != nil {
			response.Error = err.Error()
		}
	}
	reply, err := proto.NewEnvelope(proto.TypeAuthoringResponse, runID, response)
	if err == nil {
		_ = session.Send(ctx, reply)
	}
}
