package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestOutputSchemaSurvivesPreparationAndColdResume(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, supplied := range []bool{false, true} {
			t.Run(map[bool]string{true: "resume", false: "new"}[resume]+map[bool]string{true: "/schema", false: "/ordinary"}[supplied], func(t *testing.T) {
				req, cfg, root := preparationFixture(t)
				if resume {
					req.AgentSessionID = "fixture-native-thread"
				}
				var want json.RawMessage
				if supplied {
					want = json.RawMessage(`{"const":9007199254740993,"multipleOf":0.00000000000000000001}`)
					req.OutputSchema = bytes.Clone(want)
				}
				p, err := newPreparation(t.Context(), req, cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer p.Close()
				assertPreparationOnly(t, root)
				for i := range req.OutputSchema {
					req.OutputSchema[i] = ' '
				}
				out := make(chan proto.Envelope, 8)
				s, err := p.start(t.Context(), "schema-run", "hello", out)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Cancel(context.Background())
				for _, frame := range waitPreparationMethod(t, root, "turn/start") {
					var params map[string]json.RawMessage
					if err := json.Unmarshal(frame.Params, &params); err != nil {
						t.Fatal(err)
					}
					if frame.Method == "turn/start" {
						if !bytes.Equal(params["outputSchema"], want) {
							t.Fatal("prepared schema lost, mutated or numerically converted")
						}
					} else if _, exists := params["outputSchema"]; exists {
						t.Fatal("schema leaked into a non-Turn request")
					}
				}
			})
		}
	}
}

func TestOutputSchemaInvalidShapeRejectedBeforeNativeLaunch(t *testing.T) {
	for _, value := range []string{"null", "[]", "false", `"schema"`, "{"} {
		req, cfg, root := preparationFixture(t)
		req.OutputSchema = json.RawMessage(value)
		if p, err := newPreparation(t.Context(), req, cfg); err == nil {
			_ = p.Close()
			t.Fatal("invalid output schema accepted")
		}
		if len(preparationFrames(t, root)) != 0 {
			t.Fatal("invalid schema launched native work")
		}
	}
}

func TestOutputSchemaNativeErrorsNeverRetryWithoutConstraint(t *testing.T) {
	for _, mode := range []string{"rpc", "provider"} {
		t.Run(mode, func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			t.Setenv("PARSAR_PREPARATION_SCHEMA_ERROR", mode)
			req.OutputSchema = json.RawMessage(`{"unsupported_schema_keyword":true}`)
			req.RunID, req.Prompt = "schema-error", "hello"
			out := make(chan proto.Envelope, 8)
			s, err := newSession(t.Context(), req, out, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Cancel(context.Background())
			failed, done := false, false
			deadline := time.After(4 * time.Second)
			for closed := false; !closed; {
				select {
				case env, ok := <-out:
					closed = !ok
					if env.Type == proto.TypeError {
						failed = bytes.Contains(env.Payload, []byte("schema rejected"))
					}
					if env.Type == proto.TypeDone {
						done = true
					}
				case <-deadline:
					t.Fatal("schema failure did not settle")
				}
			}
			// The router normally releases the completed native Session.
			if err := s.Cancel(context.Background()); err != nil {
				t.Fatal(err)
			}
			starts := 0
			for _, frame := range preparationFrames(t, root) {
				if frame.Method == "turn/start" {
					starts++
					if !strings.Contains(string(frame.Params), `"outputSchema":{"unsupported_schema_keyword":true}`) {
						t.Fatal("native failure removed the output constraint")
					}
				}
			}
			if !failed || !done || starts != 1 {
				t.Fatal("schema failure retried or reported success", failed, done, starts)
			}
		})
	}
}
