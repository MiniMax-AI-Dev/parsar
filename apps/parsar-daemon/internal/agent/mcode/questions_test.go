package mcode

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestQuestionnaireOtherUsesOneQuestion(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		property := formProperty{Type: "string", Title: "Region?", Options: []formOption{{Value: "eu", Title: "Europe"}}}
		if multiple {
			property.Type = "array"
			property.Items.Options = property.Options
			property.Options = nil
		}
		properties := map[string]formProperty{"region": property, "region__other": {Type: "string", Title: "Region? — Other"}}
		params, _ := json.Marshal(map[string]any{"sessionId": "native-1", "mode": "form", "requestedSchema": map[string]any{"type": "object", "properties": properties}})
		out := make(chan proto.Envelope, 1)
		session := &Session{ctx: t.Context(), sessionID: "native-1", out: out, questions: map[string]pendingQuestion{}}
		if err := session.askQuestion(rpcFrame{ID: json.RawMessage(`1`), Params: params}); err != nil {
			t.Fatal(err)
		}
		var request proto.PromptForUserChoicePayload
		_ = json.Unmarshal((<-out).Payload, &request)
		if len(request.Questions) != 1 || !request.Questions[0].IsOther || request.Questions[0].MultiSelect != multiple {
			t.Fatalf("unexpected questions: %#v", request.Questions)
		}
		pending := session.questions[request.AskID]
		for _, custom := range []bool{false, true} {
			answer := "Europe"
			want := map[string]any{"region": "eu"}
			if multiple {
				want["region"] = []string{"eu"}
			}
			if custom {
				answer = "Asia"
				want = map[string]any{"region__other": "Asia"}
			}
			got, err := questionContent(pending, proto.PromptForUserChoiceDecisionPayload{Answers: []string{answer}})
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("multiple=%t custom=%t: got=%v err=%v", multiple, custom, got, err)
			}
		}
		if multiple {
			got, err := questionContent(pending, proto.PromptForUserChoiceDecisionPayload{Answers: []string{"Europe", "Asia"}})
			want := map[string]any{"region": []string{"eu"}, "region__other": "Asia"}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("combined choice and custom: got=%v err=%v", got, err)
			}
		}
	}
}
