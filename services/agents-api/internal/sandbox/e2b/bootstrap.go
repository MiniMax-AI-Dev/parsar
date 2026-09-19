package e2b

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

const bootstrapInput = "/root/.parsar/e2b/bootstrap.json"
const bootstrapReceipt = "/root/.parsar/e2b/ready.json"

func basicUser(user string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"))
}

func (p *Provider) file(ctx context.Context, a allocation, name string, data []byte) ([]byte, error) {
	if a.AccessToken == "" {
		return nil, sandbox.ErrOwnership
	}
	method := http.MethodGet
	var body bytes.Buffer
	var contentType string
	if data != nil {
		method = http.MethodPost
		writer := multipart.NewWriter(&body)
		part, e := writer.CreateFormFile("file", "bootstrap.json")
		if e != nil {
			return nil, e
		}
		if _, e = part.Write(data); e != nil {
			return nil, e
		}
		if e = writer.Close(); e != nil {
			return nil, e
		}
		contentType = writer.FormDataContentType()
	}
	req, e := http.NewRequestWithContext(ctx, method, envdURL+"/files?"+url.Values{"path": {name}, "username": {"root"}}.Encode(), &body)
	if e != nil {
		return nil, sandbox.ErrInvalid
	}
	req.Header.Set("X-Access-Token", a.AccessToken)
	req.Header.Set("E2b-Sandbox-Id", a.ID)
	req.Header.Set("E2b-Sandbox-Port", "49983")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	response, e := p.client.Do(req)
	if e != nil {
		return nil, errors.Join(errors.New("E2B initialization file request failed"), ctx.Err())
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		return nil, sandbox.ErrNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("E2B initialization file request returned HTTP %d", response.StatusCode)
	}
	output, e := io.ReadAll(io.LimitReader(response.Body, 65537))
	if e != nil || len(output) > 65536 {
		return nil, errors.New("invalid E2B initialization file response")
	}
	return output, nil
}
func (p *Provider) bootstrap(ctx context.Context, a allocation, b sandbox.Bootstrap) error {
	raw, e := json.Marshal(struct {
		sandbox.Reference
		SessionID     string `json:"session_id"`
		DeviceID      string `json:"runtime_id"`
		CoreURL       string `json:"server_url"`
		Credential    string `json:"runner_credential"`
		NetworkAccess string `json:"network_access"`
	}{b.Reference, b.SessionID, b.DeviceID, b.CoreURL, b.Credential, b.NetworkAccess})
	if e != nil {
		return e
	}
	// E2B reinitializes /run when booting a template. /root remains root-private
	// on persistent disk, including while envd creates the input's parent directory.
	if _, e = p.file(ctx, a, bootstrapInput, raw); e != nil {
		return e
	}
	result, e := p.run(ctx, a, "root", sandbox.Command{Args: []string{"/usr/bin/python3", "/opt/parsar-e2b/init.py"}, Directory: "/root"})
	if e != nil {
		return e
	}
	if result.ExitCode != 0 {
		return errors.New("E2B Runtime initialization failed")
	}
	return nil
}
func (p *Provider) completed(ctx context.Context, a allocation, r sandbox.Reference) (bool, error) {
	raw, e := p.file(ctx, a, bootstrapReceipt, nil)
	if errors.Is(e, sandbox.ErrNotFound) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	var receipt struct {
		sandbox.Reference
		SessionID string `json:"session_id"`
		DeviceID  string `json:"runtime_id"`
	}
	if json.Unmarshal(raw, &receipt) != nil || receipt.Reference != r || !validID(receipt.SessionID) || !validID(receipt.DeviceID) || receipt.SessionID != a.Metadata[metadataPrefix+"session"] || receipt.DeviceID != a.Metadata[metadataPrefix+"device"] {
		return false, sandbox.ErrOwnership
	}
	return true, nil
}
