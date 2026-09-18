package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

func SupportsNativeSessionRecovery(version string) bool {
	return strings.TrimSpace(version) == "codex-cli 0.153.4"
}

func (s *Session) recoverRoot(plan SessionPlan) (string, error) {
	var home string
	for _, value := range plan.Env {
		if strings.HasPrefix(value, "CODEX_HOME=") {
			home = strings.TrimPrefix(value, "CODEX_HOME=")
		}
	}
	if !filepath.IsAbs(home) || !filepath.IsAbs(plan.Cwd) {
		return "", errors.New("codex: recovery requires private native history")
	}
	var root string
	for _, archived := range []bool{false, true} {
		params := struct {
			Limit          int      `json:"limit"`
			ModelProviders []string `json:"modelProviders"`
			SourceKinds    []string `json:"sourceKinds"`
			Archived       bool     `json:"archived"`
			UseStateDBOnly bool     `json:"useStateDbOnly"`
		}{2, []string{}, []string{}, archived, false}
		raw, err := s.rpc.Request(s.cancelCtx, "thread/list", params)
		if err != nil {
			return "", errors.New("codex: native history lookup unavailable")
		}
		var page struct {
			Data []struct {
				ID        string          `json:"id"`
				Parent    json.RawMessage `json:"parentThreadId"`
				Fork      json.RawMessage `json:"forkedFromId"`
				Ephemeral *bool           `json:"ephemeral"`
				Source    string          `json:"source"`
				Cwd       string          `json:"cwd"`
				Path      string          `json:"path"`
			} `json:"data"`
			NextCursor json.RawMessage `json:"nextCursor"`
		}
		if json.Unmarshal(raw, &page) != nil || page.Data == nil || !bytes.Equal(bytes.TrimSpace(page.NextCursor), []byte("null")) {
			return "", errors.New("codex: native history lookup incomplete")
		}
		if len(page.Data) > 1 || (archived && len(page.Data) != 0) {
			return "", errors.New("codex: native history is ambiguous or archived")
		}
		for _, row := range page.Data {
			rel, err := filepath.Rel(filepath.Join(home, "sessions"), row.Path)
			if strings.TrimSpace(row.ID) == "" || row.Ephemeral == nil || *row.Ephemeral ||
				!bytes.Equal(bytes.TrimSpace(row.Parent), []byte("null")) || !bytes.Equal(bytes.TrimSpace(row.Fork), []byte("null")) ||
				(row.Source != "vscode" && row.Source != "cli" && row.Source != "appServer") ||
				row.Cwd != plan.Cwd || !filepath.IsAbs(row.Path) || err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return "", errors.New("codex: native history ownership is unverified")
			}
			root = row.ID
		}
	}
	if root == "" {
		return "", errors.New("codex: required native history is missing")
	}
	return root, nil
}
