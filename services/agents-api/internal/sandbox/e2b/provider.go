// Package e2b implements compute lifecycle for the colocated Runtime. It does not
// implement model execution, public Files, or a second Runtime transport.
package e2b

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
)

type Config struct {
	InstallationID, APIKey, Template string
	// LeaseSeconds must exceed Core's one-hour disconnect grace. Renewal changes
	// the expiry of the original running VM; it never resumes or replaces it.
	LeaseSeconds int
}
type Provider struct {
	config Config
	client *http.Client
}

var _ sandbox.Provider = (*Provider)(nil)

func validID(v string) bool {
	u, e := uuid.Parse(v)
	return e == nil && u != uuid.Nil && u.String() == v
}
func validReference(r sandbox.Reference) bool {
	return validID(r.TenantID) && validID(r.EnvironmentID) && validID(r.AllocationID)
}

func New(config Config) (*Provider, error) {
	parts := strings.Split(config.Template, ":")
	if !validID(config.InstallationID) || strings.TrimSpace(config.APIKey) == "" || len(parts) != 2 || !validID(parts[1]) || parts[0] == "" || strings.Trim(parts[0], "abcdefghijklmnopqrstuvwxyz0123456789") != "" || config.LeaseSeconds < 7200 || config.LeaseSeconds > 86400 {
		return nil, sandbox.ErrInvalid
	}
	return &Provider{config: config, client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("E2B redirects are not allowed") }}}, nil
}
func (p *Provider) Create(ctx context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	info := sandbox.Info{Reference: b.Reference}
	u, e := url.Parse(b.CoreURL)
	if !validReference(b.Reference) || !validID(b.SessionID) || !validID(b.DeviceID) || e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(b.Credential) == "" || (b.NetworkAccess != "enabled" && b.NetworkAccess != "disabled" && b.NetworkAccess != "") {
		return info, sandbox.ErrInvalid
	}
	existing, e := p.allocations(ctx, b.Reference)
	if e != nil {
		return info, e
	}
	if len(existing) != 0 {
		return info, sandbox.ErrExists
	}
	var a allocation
	metadata := p.metadata(b.Reference)
	metadata[metadataPrefix+"session"] = b.SessionID
	metadata[metadataPrefix+"device"] = b.DeviceID
	_, e = p.request(ctx, http.MethodPost, "/sandboxes", map[string]any{"templateID": p.config.Template, "timeout": p.config.LeaseSeconds, "secure": true, "autoPause": false, "allowInternetAccess": true, "metadata": metadata}, &a)
	if e != nil {
		return info, e
	}
	info.ProviderID = a.ID
	// Re-read authenticated identity and envd authority. Running compute is not
	// evidence that credential injection and daemon launch have completed.
	a, e = p.inspectID(ctx, a.ID, b.Reference)
	if e != nil {
		return info, e
	}
	if e = p.bootstrap(ctx, a, b); e != nil {
		return info, e
	}
	return p.GetInfo(ctx, b.Reference)
}
func (p *Provider) GetInfo(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	info := sandbox.Info{Reference: r}
	a, e := p.inspect(ctx, r)
	if e != nil {
		return info, e
	}
	info.ProviderID, info.State = a.ID, a.State
	if a.State == "running" {
		info.BootstrapComplete, e = p.completed(ctx, a, r)
	}
	return info, e
}
func (p *Provider) Renew(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	info := sandbox.Info{Reference: r}
	a, e := p.inspect(ctx, r)
	if e != nil {
		return info, e
	}
	info.ProviderID, info.State = a.ID, a.State
	if a.State != "running" {
		return info, errors.New("E2B allocation is not running")
	}
	_, e = p.request(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(a.ID)+"/timeout", map[string]int{"timeout": p.config.LeaseSeconds}, nil)
	if e != nil {
		return info, e
	}
	return p.GetInfo(ctx, r)
}
func (p *Provider) Kill(ctx context.Context, r sandbox.Reference) error {
	all, e := p.allocations(ctx, r)
	if e != nil {
		return e
	}
	// An anomalous duplicate may be reclaimed only after all exact owners are
	// checked. No execution operation chooses an arbitrary duplicate.
	for _, a := range all {
		if _, e = p.inspectID(ctx, a.ID, r); e != nil && !errors.Is(e, sandbox.ErrNotFound) {
			return e
		}
	}
	for _, a := range all {
		if _, e = p.request(ctx, http.MethodDelete, "/sandboxes/"+url.PathEscape(a.ID), nil, nil); e != nil && !errors.Is(e, sandbox.ErrNotFound) {
			return e
		}
	}
	remaining, e := p.allocations(ctx, r)
	if e != nil {
		return e
	}
	if len(remaining) != 0 {
		return errors.New("E2B removal unconfirmed")
	}
	return nil
}
