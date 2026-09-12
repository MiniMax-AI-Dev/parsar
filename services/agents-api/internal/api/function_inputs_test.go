package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func submitResultRequest(t *testing.T, body string, failure error) (*httptest.ResponseRecorder, *inputRecorder) {
	t.Helper()
	recorder := &inputRecorder{err: failure}
	h, _, _ := testHandler(t, WithExecution(recorder))
	r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions/session/events", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer test-api-key")
	r.Header.Set("OpenAI-Beta", "agents=v1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w, recorder
}

func TestPublicFunctionResultsPreserveOptionalValues(t *testing.T) {
	for _, fields := range []string{
		`"success":false`, `"success":true,"error":null,"output":null`,
		`"success":false,"error":"","output":""`, `"success":true,"output":[]`,
		`"success":false,"error":"failure","output":[{"type":"input_text","text":""},{"type":"input_image","image_url":"data:image/png;base64,AA=="},{"type":"input_text","text":"last"}]`,
	} {
		body := `{"events":[{"type":"agent.session.input.tool_result","turn_id":"turn","call_id":"call",` + fields + `}]}`
		w, recorder := submitResultRequest(t, body, nil)
		if w.Code != 204 || w.Body.Len() != 0 || len(recorder.inputs) != 1 {
			t.Fatal(w.Code, w.Body, recorder.inputs)
		}
		var input store.FunctionResultInput
		if json.Unmarshal(recorder.inputs[0].Payload, &input) != nil || input.TurnID != "turn" || input.CallID != "call" {
			t.Fatal(input)
		}
		var actual, expected map[string]any
		_ = json.Unmarshal(input.Result, &actual)
		_ = json.Unmarshal([]byte(`{`+fields+`}`), &expected)
		a, _ := json.Marshal(actual)
		b, _ := json.Marshal(expected)
		if string(a) != string(b) {
			t.Fatalf("got %s want %s", a, b)
		}
	}
}

func TestPublicFunctionResultsRejectMalformedVariantsBeforeAdmission(t *testing.T) {
	prefix := `"type":"agent.session.input.tool_result","turn_id":"turn","call_id":"call"`
	for _, event := range []string{
		`{` + prefix + `}`, `{` + prefix + `,"success":null}`, `{` + prefix + `,"success":"true"}`,
		`{` + prefix + `,"success":true,"input":null}`, `{` + prefix + `,"success":true,"error":{}}`,
		`{` + prefix + `,"success":true,"output":{}}`, `{` + prefix + `,"success":true,"output":false}`,
		`{` + prefix + `,"success":true,"output":[null]}`, `{` + prefix + `,"success":true,"output":[{"type":"input_text"}]}`,
		`{` + prefix + `,"success":true,"output":[{"type":"input_text","text":null}]}`,
		`{` + prefix + `,"success":true,"output":[{"type":"input_text","text":"x","image_url":null}]}`,
		`{` + prefix + `,"success":true,"output":[{"type":"input_image"}]}`,
		`{` + prefix + `,"success":true,"output":[{"type":"input_image","image_url":null}]}`,
		`{` + prefix + `,"success":true,"output":[{"type":"input_image","image_url":"url","text":"x"}]}`,
		`{"type":"agent.session.input.tool_result","turn_id":"turn","success":true}`,
		`{"type":"agent.session.input.tool_result","call_id":"call","success":true}`,
		`{"type":"agent.session.input.cancel","success":null}`, `{"type":"agent.session.input.cancel","input":null}`,
		`{"type":"agent.session.input.message","input":[],"output":null}`,
	} {
		w, recorder := submitResultRequest(t, `{"events":[{"type":"agent.session.input.cancel"},`+event+`]}`, nil)
		if w.Code != 400 || recorder.inputs != nil {
			t.Fatal(event, w.Code, w.Body, recorder.inputs)
		}
	}
}

func TestPublicFunctionTurnConflictIsNotServerFailure(t *testing.T) {
	w, _ := submitResultRequest(t, `{"events":[{"type":"agent.session.input.tool_result","turn_id":"turn","call_id":"call","success":true}]}`, store.ErrTurnConflict)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"turn_conflict"`) {
		t.Fatal(w.Code, w.Body)
	}
}
