package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

type managedEnrollResponse struct {
	ServerURL    string `json:"server_url"`
	PairingToken string `json:"pairing_token"`
}

func runManagedConnect(ctx *runContext, args []string) error {
	fs := newFlagSet("managed-connect")
	profile := fs.String("profile", "managed-local", "managed runtime profile")
	serverURL := fs.String("url", "http://127.0.0.1:8080", "local Parsar server base URL")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("managed-connect: parse flags: %w", err)
	}
	if err := paths.ValidateProfile(*profile); err != nil {
		return fmt.Errorf("managed-connect: %w", err)
	}
	_ = os.Setenv("PARSAR_DAEMON_ALLOW_EMPTY_AGENT_CLIS", "true")
	_ = os.Setenv("PARSAR_DAEMON_WS_URL_OVERRIDE", "ws://127.0.0.1:8080/agent-daemon/ws")
	_ = os.Setenv("PARSAR_DAEMON_LAZY_CODEX", "true")
	if root, err := paths.Root(); err == nil {
		codexBinDir := filepath.Join(root, "tools", "codex", "bin")
		_ = os.Setenv("PATH", codexBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	for {
		if _, err := auth.Load(*profile); err == nil {
			if err := runConnect(ctx, []string{"--profile", *profile}); err == nil {
				return nil
			}
			if err := auth.Delete(*profile); err != nil {
				return fmt.Errorf("managed-connect: reset stale profile: %w", err)
			}
		}
		enroll, retry, err := requestManagedEnrollment(*serverURL)
		if retry {
			fmt.Fprintln(ctx.stdout, "managed-connect: waiting for first workspace")
			time.Sleep(2 * time.Second)
			continue
		}
		if err == nil {
			return runConnect(ctx, []string{
				"--profile", *profile,
				"--url", enroll.ServerURL,
				"--token", enroll.PairingToken,
				"--device-name", "Default Docker Runtime",
			})
		}
		return err
	}
}

func requestManagedEnrollment(serverURL string) (managedEnrollResponse, bool, error) {
	base := strings.TrimRight(strings.TrimSpace(serverURL), "/")
	requestCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, base+"/internal/managed-daemon/enroll", bytes.NewReader(nil))
	if err != nil {
		return managedEnrollResponse{}, false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return managedEnrollResponse{}, true, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusConflict && bytes.Contains(body, []byte("bootstrap_pending")) {
		return managedEnrollResponse{}, true, nil
	}
	if resp.StatusCode/100 != 2 {
		return managedEnrollResponse{}, false, fmt.Errorf("managed-connect: enroll failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out managedEnrollResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return managedEnrollResponse{}, false, fmt.Errorf("managed-connect: decode enrollment: %w", err)
	}
	return out, false, nil
}
