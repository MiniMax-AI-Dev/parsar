package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTP client for the agent-runtime tree (server/internal/api/specmem).
// All endpoints live under /api/v1/agent-runtime and authenticate via
// Config.RunnerToken.

type client struct {
	cfg  Config
	http *http.Client
}

func newClient(cfg Config) *client {
	return &client{
		cfg:  cfg,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// DTOs mirror server/internal/api/specmem handler.go but only the
// fields the CLI prints. The contract is the JSON keys — we
// deliberately don't import the server package so parsar ships as a tiny
// static binary.

type Fragment struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	Tags        []string  `json:"tags"`
	Source      string    `json:"source"`
	CreatedBy   string    `json:"created_by,omitempty"`
	AgentActor  string    `json:"agent_actor,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Memory struct {
	ID             string    `json:"id"`
	Scope          string    `json:"scope"`
	UserID         string    `json:"user_id"`
	WorkspaceID    string    `json:"workspace_id,omitempty"`
	MemoryType     string    `json:"memory_type"`
	Title          string    `json:"title,omitempty"`
	Body           string    `json:"body"`
	Why            string    `json:"why,omitempty"`
	Tags           []string  `json:"tags"`
	Source         string    `json:"source"`
	AgentActor     string    `json:"agent_actor,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Injection struct {
	SpecBlock         string `json:"spec_block"`
	MemoryBlock       string `json:"memory_block"`
	MemoryWriteGuide  string `json:"memory_write_guide"`
	IncrementalMemory string `json:"incremental_memory"`
}

// apiError carries the server's writeError(...) JSON shape.
type apiError struct {
	Status  int
	Code    string `json:"error"`
	Message string `json:"message"`
}

func (e *apiError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("server returned %d (%s): %s", e.Status, e.Code, e.Message)
	}
	if e.Code != "" {
		return fmt.Sprintf("server returned %d (%s)", e.Status, e.Code)
	}
	return fmt.Sprintf("server returned %d", e.Status)
}

// ----- transport ------------------------------------------------------------

// do dispatches a JSON request and unmarshals the response. body and
// out are both optional.
func (c *client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	endpoint, err := c.endpointURL(path, query)
	if err != nil {
		return err
	}
	var buf io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		buf = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, buf)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.RunnerToken)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		apiErr := &apiError{Status: resp.StatusCode}
		// Fall back to raw body when the response isn't structured JSON
		// (e.g. proxy 502) so the user sees something actionable.
		if err := json.Unmarshal(raw, apiErr); err != nil || apiErr.Code == "" {
			apiErr.Message = strings.TrimSpace(string(raw))
		}
		return apiErr
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *client) endpointURL(path string, query url.Values) (string, error) {
	base, err := url.Parse(c.cfg.ServerURL)
	if err != nil {
		return "", fmt.Errorf("server url: %w", err)
	}
	rel, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("api path %q: %w", path, err)
	}
	out := base.ResolveReference(rel)
	if len(query) > 0 {
		out.RawQuery = query.Encode()
	}
	return out.String(), nil
}

// ----- spec_fragments -------------------------------------------------------

type listFragmentsResponse struct {
	Fragments []Fragment `json:"fragments"`
}

func (c *client) ListFragments(ctx context.Context, tags []string, source string, limit int) ([]Fragment, error) {
	q := url.Values{}
	if len(tags) > 0 {
		q.Set("tag", strings.Join(tags, ","))
	}
	if source != "" {
		q.Set("source", source)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	var resp listFragmentsResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/agent-runtime/spec/fragments", q, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Fragments, nil
}

type createFragmentRequest struct {
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Tags  []string `json:"tags,omitempty"`
}

func (c *client) CreateFragment(ctx context.Context, title, body string, tags []string) (Fragment, error) {
	var out Fragment
	in := createFragmentRequest{Title: title, Body: body, Tags: tags}
	if err := c.do(ctx, http.MethodPost, "/api/v1/agent-runtime/spec/fragments", nil, in, &out); err != nil {
		return Fragment{}, err
	}
	return out, nil
}

type updateFragmentRequest struct {
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Tags  []string `json:"tags"`
}

func (c *client) UpdateFragment(ctx context.Context, id, title, body string, tags []string) (Fragment, error) {
	var out Fragment
	in := updateFragmentRequest{Title: title, Body: body, Tags: tags}
	path := "/api/v1/agent-runtime/spec/fragments/" + url.PathEscape(id)
	if err := c.do(ctx, http.MethodPatch, path, nil, in, &out); err != nil {
		return Fragment{}, err
	}
	return out, nil
}

func (c *client) DeleteFragment(ctx context.Context, id string) error {
	path := "/api/v1/agent-runtime/spec/fragments/" + url.PathEscape(id)
	return c.do(ctx, http.MethodDelete, path, nil, nil, nil)
}

// ----- memories -------------------------------------------------------------

type listMemoriesResponse struct {
	Memories []Memory `json:"memories"`
}

func (c *client) ListMemories(ctx context.Context, scope, memoryType string, tags []string, limit int) ([]Memory, error) {
	q := url.Values{}
	q.Set("scope", scope)
	if memoryType != "" {
		q.Set("memory_type", memoryType)
	}
	if len(tags) > 0 {
		q.Set("tag", strings.Join(tags, ","))
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	var resp listMemoriesResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/agent-runtime/memories", q, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Memories, nil
}

type createMemoryRequest struct {
	Scope      string   `json:"scope,omitempty"`
	MemoryType string   `json:"memory_type"`
	Title      string   `json:"title,omitempty"`
	Body       string   `json:"body"`
	Why        string   `json:"why,omitempty"`
	Tags       []string `json:"tags,omitempty"`
}

func (c *client) CreateMemory(ctx context.Context, scope, memoryType, title, body, why string, tags []string) (Memory, error) {
	in := createMemoryRequest{
		Scope:      scope,
		MemoryType: memoryType,
		Title:      title,
		Body:       body,
		Why:        why,
		Tags:       tags,
	}
	var out Memory
	if err := c.do(ctx, http.MethodPost, "/api/v1/agent-runtime/memories", nil, in, &out); err != nil {
		return Memory{}, err
	}
	return out, nil
}

type updateMemoryRequest struct {
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Why   string   `json:"why"`
	Tags  []string `json:"tags"`
}

func (c *client) UpdateMemory(ctx context.Context, id, title, body, why string, tags []string) (Memory, error) {
	in := updateMemoryRequest{Title: title, Body: body, Why: why, Tags: tags}
	var out Memory
	path := "/api/v1/agent-runtime/memories/" + url.PathEscape(id)
	if err := c.do(ctx, http.MethodPatch, path, nil, in, &out); err != nil {
		return Memory{}, err
	}
	return out, nil
}

func (c *client) DeleteMemory(ctx context.Context, id string) error {
	path := "/api/v1/agent-runtime/memories/" + url.PathEscape(id)
	return c.do(ctx, http.MethodDelete, path, nil, nil, nil)
}

// ----- injection ------------------------------------------------------------

func (c *client) Snapshot(ctx context.Context) (Injection, error) {
	var out Injection
	if err := c.do(ctx, http.MethodGet, "/api/v1/agent-runtime/injection/snapshot", nil, nil, &out); err != nil {
		return Injection{}, err
	}
	return out, nil
}

func (c *client) Incremental(ctx context.Context, since time.Time) (Injection, error) {
	q := url.Values{}
	q.Set("since", since.UTC().Format(time.RFC3339))
	var out Injection
	if err := c.do(ctx, http.MethodGet, "/api/v1/agent-runtime/injection/incremental", q, nil, &out); err != nil {
		return Injection{}, err
	}
	return out, nil
}

// IsNotFound reports whether err is a 404 from the agent-runtime tree.
func IsNotFound(err error) bool {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.Status == http.StatusNotFound
	}
	return false
}
