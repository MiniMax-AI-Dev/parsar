package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Validate against the binary's active catalog and use that same snapshot for
// execution. A CLI override alone is silently ignored for unsupported models.
func prepareModelVerbosity(ctx context.Context, binary string, plan *SessionPlan) error {
	args := []string{}
	for _, kv := range plan.ExtraConfig {
		args = append(args, "-c", kv[0]+"="+kv[1])
	}
	args = append(args, "debug", "models")
	ctx, cancel := context.WithTimeout(ctx, rpcDefaultRequestTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = plan.Cwd
	cmd.Env = append(os.Environ(), plan.Env...)
	catalog, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("codex: cannot verify model verbosity support: %w", err)
	}
	supported, err := catalogSupportsVerbosity(catalog, plan.Model)
	if err != nil {
		return fmt.Errorf("codex: cannot read model verbosity support: %w", err)
	}
	if !supported {
		return fmt.Errorf("codex: model %q does not declare text verbosity support", plan.Model)
	}
	codexHome := ""
	for _, entry := range plan.Env {
		if value, ok := strings.CutPrefix(entry, "CODEX_HOME="); ok {
			codexHome = value
		}
	}
	if !filepath.IsAbs(codexHome) {
		return fmt.Errorf("codex: missing managed home for model catalog")
	}
	file, err := os.CreateTemp(codexHome, "model-catalog-*.json")
	if err != nil {
		return err
	}
	_, writeErr := file.Write(catalog)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(file.Name())
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	cleanup := plan.Cleanup
	plan.Cleanup = func() { _ = os.Remove(file.Name()); cleanup() }
	plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"model_catalog_json", strconv(file.Name())})
	return nil
}

func catalogSupportsVerbosity(raw []byte, model string) (bool, error) {
	var catalog struct {
		Models []struct {
			Slug             string `json:"slug"`
			SupportVerbosity bool   `json:"support_verbosity"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return false, err
	}
	// Match Codex models-manager's longest-prefix lookup, then its single,
	// simple provider namespace fallback. Do not infer support from a model name.
	match := func(name string) (bool, bool) {
		longest, supported := 0, false
		for _, candidate := range catalog.Models {
			if len(candidate.Slug) > longest && strings.HasPrefix(name, candidate.Slug) {
				longest, supported = len(candidate.Slug), candidate.SupportVerbosity
			}
		}
		return supported, longest > 0
	}
	if supported, found := match(model); found {
		return supported, nil
	}
	namespace, suffix, found := strings.Cut(model, "/")
	if !found || namespace == "" || strings.Contains(suffix, "/") {
		return false, nil
	}
	for _, c := range namespace {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false, nil
		}
	}
	supported, _ := match(suffix)
	return supported, nil
}
