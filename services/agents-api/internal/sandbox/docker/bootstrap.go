package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/moby/moby/client"
)

type entry struct {
	name      string
	content   []byte
	directory bool
}

func (p *Provider) bootstrap(ctx context.Context, id string, b sandbox.Bootstrap) error {
	// The existing daemon auth profile is the shared managed/user-managed boundary.
	// This file is injected before the untrusted workspace or harness can run.
	auth, e := json.Marshal(struct {
		ServerURL  string `json:"server_url"`
		RuntimeID  string `json:"runtime_id"`
		Credential string `json:"runner_credential"`
	}{b.CoreURL, b.DeviceID, b.Credential})
	if e != nil {
		return e
	}
	if e = p.copy(ctx, id, "/home", []entry{
		{name: "runtime", directory: true}, {name: "runtime/.parsar", directory: true},
		{name: "runtime/.parsar/parsar-daemon", directory: true},
		{name: "runtime/.parsar/parsar-daemon/default", directory: true},
		{name: "runtime/.parsar/parsar-daemon/default/auth.json", content: auth},
	}); e != nil {
		return e
	}
	return p.copy(ctx, id, "/environment", []entry{{name: "workspace", directory: true}, {name: "staging", directory: true}, {name: "initialization", directory: true}, {name: "packages", directory: true}})
}
func (p *Provider) copy(ctx context.Context, id, path string, entries []entry) error {
	var content bytes.Buffer
	writer := tar.NewWriter(&content)
	for _, file := range entries {
		header := &tar.Header{Name: file.name, Uid: 1000, Gid: 1000, Mode: 0600, Size: int64(len(file.content)), Typeflag: tar.TypeReg}
		if file.directory {
			header.Mode = 0700
			header.Typeflag = tar.TypeDir
		}
		if e := writer.WriteHeader(header); e != nil {
			return e
		}
		if _, e := writer.Write(file.content); e != nil {
			return e
		}
	}
	if e := writer.Close(); e != nil {
		return e
	}
	_, e := p.client.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: path, Content: io.Reader(&content), CopyUIDGID: true})
	return e
}
