// Package docker is a thin SDK adapter for the dedicated, colocated Runtime.
package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/containerd/errdefs"
	"github.com/google/uuid"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
)

const labelPrefix = "io.parsar.agents-api."

// Config is trusted operator configuration, never public Session input. The
// immutable image contains the qualified native profile and all Runtime binaries.
// Seccomp is JSON content, not a path on the Docker host. Network must provide
// trusted daemon/model connectivity; native tool network policy is in the image.
type Config struct {
	InstallationID, Image, Network, Seccomp string
	ExtraHosts                              []string
}
type Provider struct {
	client *client.Client
	config Config
}

var _ sandbox.Provider = (*Provider)(nil)

func New(c *client.Client, config Config) (*Provider, error) {
	if c == nil || !validID(config.InstallationID) || (!strings.HasPrefix(config.Image, "sha256:") && !strings.Contains(config.Image, "@sha256:")) || config.Seccomp == "" || config.Network == "" || config.Network == "host" || strings.HasPrefix(config.Network, "container:") {
		return nil, sandbox.ErrInvalid
	}
	return &Provider{client: c, config: config}, nil
}
func validID(v string) bool {
	u, e := uuid.Parse(v)
	return e == nil && u != uuid.Nil && u.String() == v
}
func validReference(r sandbox.Reference) bool {
	return validID(r.TenantID) && validID(r.EnvironmentID) && validID(r.AllocationID)
}
func (p *Provider) name(r sandbox.Reference) string {
	h := sha256.Sum256([]byte(p.config.InstallationID + ":" + r.TenantID + ":" + r.EnvironmentID + ":" + r.AllocationID))
	return "agents-runtime-" + hex.EncodeToString(h[:16])
}
func (p *Provider) labels(r sandbox.Reference) map[string]string {
	return map[string]string{
		labelPrefix + "installation": p.config.InstallationID, labelPrefix + "tenant": r.TenantID, labelPrefix + "environment": r.EnvironmentID, labelPrefix + "allocation": r.AllocationID,
	}
}
func (p *Provider) owns(labels map[string]string, r sandbox.Reference) bool {
	for k, v := range p.labels(r) {
		if labels[k] != v {
			return false
		}
	}
	return true
}
func (p *Provider) inspect(ctx context.Context, r sandbox.Reference) (client.ContainerInspectResult, error) {
	if !validReference(r) {
		return client.ContainerInspectResult{}, sandbox.ErrInvalid
	}
	v, e := p.client.ContainerInspect(ctx, p.name(r), client.ContainerInspectOptions{})
	if errdefs.IsNotFound(e) {
		return v, sandbox.ErrNotFound
	}
	if e != nil {
		return v, e
	}
	if v.Container.Config == nil || !p.owns(v.Container.Config.Labels, r) {
		return v, sandbox.ErrOwnership
	}
	return v, nil
}
func (p *Provider) GetInfo(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	info := sandbox.Info{Reference: r}
	v, e := p.inspect(ctx, r)
	if e != nil {
		return info, e
	}
	info.ProviderID = v.Container.ID
	info.State = string(v.Container.State.Status)
	return info, nil
}

// Renew observes Docker state. Docker has no lease; service-owned expiry must be
// managed durably by Core. Never synthesize an expiry or revive a stopped Runtime.
func (p *Provider) Renew(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	return p.GetInfo(ctx, r)
}

func (p *Provider) Create(ctx context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	info := sandbox.Info{Reference: b.Reference}
	u, e := url.Parse(b.CoreURL)
	if !validReference(b.Reference) || !validID(b.SessionID) || !validID(b.DeviceID) || e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(b.Credential) == "" {
		return info, sandbox.ErrInvalid
	}
	if existing, e := p.GetInfo(ctx, b.Reference); e == nil {
		return existing, sandbox.ErrExists
	} else if !errors.Is(e, sandbox.ErrNotFound) {
		return info, e
	}
	name := p.name(b.Reference)
	// Retained volumes without a container are partial or lost state, not an
	// invitation to overwrite native history with a new bootstrap identity.
	for _, suffix := range []string{"-home", "-environment"} {
		v, err := p.client.VolumeInspect(ctx, name+suffix, client.VolumeInspectOptions{})
		if err == nil {
			if !p.owns(v.Volume.Labels, b.Reference) {
				return info, sandbox.ErrOwnership
			}
			return info, sandbox.ErrExists
		}
		if !errdefs.IsNotFound(err) {
			return info, err
		}
	}
	for _, suffix := range []string{"-home", "-environment"} {
		v, e := p.client.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name + suffix, Labels: p.labels(b.Reference)})
		if e != nil {
			return info, e
		}
		if !p.owns(v.Volume.Labels, b.Reference) {
			return info, sandbox.ErrOwnership
		}
	}
	limit := int64(128)
	v, e := p.client.ContainerCreate(ctx, client.ContainerCreateOptions{Name: name, Image: p.config.Image,
		Config: &container.Config{User: "1000:1000", WorkingDir: "/environment/workspace", Labels: p.labels(b.Reference), Env: []string{"PARSAR_RUNTIME_ENVIRONMENT_ID=" + b.EnvironmentID, "PARSAR_RUNTIME_SESSION_ID=" + b.SessionID}},
		HostConfig: &container.HostConfig{ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges", "seccomp=" + p.config.Seccomp, "apparmor=unconfined"}, NetworkMode: container.NetworkMode(p.config.Network), ExtraHosts: p.config.ExtraHosts,
			Resources: container.Resources{PidsLimit: &limit, Memory: 2 * 1024 * 1024 * 1024, NanoCPUs: 2 * 1000000000}, Tmpfs: map[string]string{"/tmp": "rw,nosuid,nodev,size=128m"},
			Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: name + "-home", Target: "/home"}, {Type: mount.TypeVolume, Source: name + "-environment", Target: "/environment"}},
		},
	})
	if errdefs.IsConflict(e) {
		return info, sandbox.ErrExists
	}
	if e != nil {
		return info, e
	}
	info.ProviderID = v.ID
	// Any failure returns the retained allocation reference. The caller must Kill
	// it, including on a lost acknowledgement. Never erase uncertain owner state.
	if e = p.bootstrap(ctx, v.ID, b); e != nil {
		return info, fmt.Errorf("runtime bootstrap: %w", e)
	}
	if _, e = p.client.ContainerStart(ctx, v.ID, client.ContainerStartOptions{}); e != nil {
		return info, e
	}
	return p.GetInfo(ctx, b.Reference)
}

// Kill is idempotent only for absence, not for errors or foreign ownership. It
// checks all resources before removing any and confirms removal of named volumes.
func (p *Provider) Kill(ctx context.Context, r sandbox.Reference) error {
	if !validReference(r) {
		return sandbox.ErrInvalid
	}
	c, e := p.inspect(ctx, r)
	if e != nil && !errors.Is(e, sandbox.ErrNotFound) {
		return e
	}
	present := e == nil
	name := p.name(r)
	volumes := []string{}
	for _, suffix := range []string{"-home", "-environment"} {
		v, e := p.client.VolumeInspect(ctx, name+suffix, client.VolumeInspectOptions{})
		if errdefs.IsNotFound(e) {
			continue
		}
		if e != nil {
			return e
		}
		if !p.owns(v.Volume.Labels, r) {
			return sandbox.ErrOwnership
		}
		volumes = append(volumes, name+suffix)
	}
	if present {
		if _, e = p.client.ContainerRemove(ctx, c.Container.ID, client.ContainerRemoveOptions{Force: true}); e != nil && !errdefs.IsNotFound(e) {
			return e
		}
	}
	for _, v := range volumes {
		if _, e = p.client.VolumeRemove(ctx, v, client.VolumeRemoveOptions{}); e != nil && !errdefs.IsNotFound(e) {
			return e
		}
	}
	if _, e = p.inspect(ctx, r); !errors.Is(e, sandbox.ErrNotFound) {
		if e == nil {
			return errors.New("container removal unconfirmed")
		}
		return e
	}
	for _, v := range volumes {
		if _, e = p.client.VolumeInspect(ctx, v, client.VolumeInspectOptions{}); !errdefs.IsNotFound(e) {
			if e == nil {
				return errors.New("volume removal unconfirmed")
			}
			return e
		}
	}
	return nil
}
