package e2b

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

const apiURL = "https://api.e2b.app"
const envdURL = "https://sandbox.e2b.app"
const metadataPrefix = "io.parsar.agents-api."

type allocation struct {
	ID          string            `json:"sandboxID"`
	State       string            `json:"state"`
	AccessToken string            `json:"envdAccessToken"`
	Metadata    map[string]string `json:"metadata"`
}

func (p *Provider) metadata(r sandbox.Reference) map[string]string {
	return map[string]string{metadataPrefix + "installation": p.config.InstallationID, metadataPrefix + "tenant": r.TenantID, metadataPrefix + "environment": r.EnvironmentID, metadataPrefix + "allocation": r.AllocationID}
}
func (p *Provider) owns(a allocation, r sandbox.Reference) bool {
	for k, v := range p.metadata(r) {
		if a.Metadata[k] != v {
			return false
		}
	}
	return a.ID != ""
}

// The caller owns retries. In particular, never replay a lost Create response.
// Provider responses and transport errors can contain secrets; return safe status only.
func (p *Provider) request(ctx context.Context, method, path string, body, out any) (http.Header, error) {
	var input io.Reader
	if body != nil {
		raw, e := json.Marshal(body)
		if e != nil {
			return nil, sandbox.ErrInvalid
		}
		input = bytes.NewReader(raw)
	}
	req, e := http.NewRequestWithContext(ctx, method, apiURL+path, input)
	if e != nil {
		return nil, sandbox.ErrInvalid
	}
	req.Header.Set("X-API-Key", p.config.APIKey)
	req.Header.Set("Content-Type", "application/json")
	response, e := p.client.Do(req)
	if e != nil {
		return nil, errors.Join(errors.New("E2B control request failed"), ctx.Err())
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return response.Header, sandbox.ErrNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.Header, fmt.Errorf("E2B control request returned HTTP %d", response.StatusCode)
	}
	if out != nil {
		raw, e := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
		if e != nil || len(raw) > 1024*1024 || json.Unmarshal(raw, out) != nil {
			return nil, errors.New("invalid E2B control response")
		}
	}
	return response.Header, nil
}

func (p *Provider) allocations(ctx context.Context, r sandbox.Reference) ([]allocation, error) {
	if !validReference(r) {
		return nil, sandbox.ErrInvalid
	}
	metadata := url.Values{}
	for k, v := range p.metadata(r) {
		metadata.Set(k, v)
	}
	query := url.Values{"metadata": {metadata.Encode()}, "limit": {"100"}, "state": {"running,paused"}}
	var result []allocation
	seen := map[string]bool{}
	for {
		var page []allocation
		headers, e := p.request(ctx, http.MethodGet, "/v2/sandboxes?"+query.Encode(), nil, &page)
		if e != nil {
			return nil, e
		}
		for _, a := range page {
			if !p.owns(a, r) {
				return nil, sandbox.ErrOwnership
			}
			result = append(result, a)
		}
		token := headers.Get("X-Next-Token")
		if token == "" {
			return result, nil
		}
		if seen[token] {
			return nil, errors.New("invalid E2B pagination")
		}
		seen[token] = true
		query.Set("nextToken", token)
	}
}
func (p *Provider) inspectID(ctx context.Context, id string, r sandbox.Reference) (allocation, error) {
	var a allocation
	_, e := p.request(ctx, http.MethodGet, "/sandboxes/"+url.PathEscape(id), nil, &a)
	if e == nil && (a.ID != id || !p.owns(a, r)) {
		e = sandbox.ErrOwnership
	}
	return a, e
}
func (p *Provider) inspect(ctx context.Context, r sandbox.Reference) (allocation, error) {
	all, e := p.allocations(ctx, r)
	if e != nil {
		return allocation{}, e
	}
	if len(all) == 0 {
		return allocation{}, sandbox.ErrNotFound
	}
	if len(all) != 1 {
		return allocation{}, sandbox.ErrOwnership
	}
	return p.inspectID(ctx, all[0].ID, r)
}
