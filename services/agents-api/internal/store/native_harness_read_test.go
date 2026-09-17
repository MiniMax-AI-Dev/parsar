package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

const nativeHarnessReadMaximum = 8 * 1024 * 1024

func nativeHarnessReadBinary() []byte {
	data := make([]byte, 2*1024*1024+37)
	for index := range data {
		data[index] = byte((index*31 + index/251) % 256)
	}
	return data
}

func seedNativeHarnessReadFiles(t *testing.T, local string) {
	t.Helper()
	for path, data := range map[string][]byte{
		"bounded-read.bin":  nativeHarnessReadBinary(),
		"bounded-empty.bin": {},
	} {
		if err := os.WriteFile(filepath.Join(local, path), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func (a *nativeHarnessArtifact) observeReads(t *testing.T, ctx context.Context, owner nativeHarnessOwner, phase, local string, retained bool) {
	t.Helper()
	if len(a.readyOwners) == 0 || owner != a.readyOwners[len(a.readyOwners)-1] {
		t.Fatal("native reads did not use the current prepared owner", phase)
	}
	binary := nativeHarnessReadBinary()
	type readCase struct {
		name     string
		path     string
		maxBytes int
		data     []byte
	}
	cases := []readCase{
		{"full", "bounded-read.bin", nativeHarnessReadMaximum, binary},
		{"exact_bound", "bounded-read.bin", len(binary), binary},
		{"truncated", "bounded-read.bin", 1024*1024 + 17, binary},
		{"minimum_bound", "bounded-read.bin", 1, binary},
		{"empty", "bounded-empty.bin", 1, []byte{}},
	}
	if retained {
		cases = append(cases, readCase{"native_retained", "retained.txt", 1024, []byte("remote-file-content\n")})
	}
	for _, check := range cases {
		localData, err := os.ReadFile(filepath.Join(local, check.path))
		if err != nil || !bytes.Equal(localData, check.data) {
			t.Fatal("native read backing file differs from expected bytes", phase, check.name)
		}
		response := a.request(t, ctx, owner, map[string]any{
			"environment_id": a.environment, "path": check.path, "operation": "read", "max_bytes": check.maxBytes,
		}, int64(base64.StdEncoding.EncodedLen(check.maxBytes)+1024))
		var read struct {
			DataBase64        *string `json:"data_base64"`
			Truncated         *bool   `json:"truncated"`
			CloseAcknowledged bool    `json:"close_acknowledged"`
		}
		if response.Error != "" || len(response.Metadata) != 0 || json.Unmarshal(response.Read, &read) != nil || read.DataBase64 == nil || read.Truncated == nil || !read.CloseAcknowledged {
			t.Fatal("private native read omitted a valid result or acknowledged close", phase, check.name, response.Error)
		}
		actual, err := base64.StdEncoding.Strict().DecodeString(*read.DataBase64)
		expected := check.data[:min(len(check.data), check.maxBytes)]
		truncated := len(check.data) > check.maxBytes
		if err != nil || !bytes.Equal(actual, expected) || *read.Truncated != truncated {
			t.Fatal("private native read bytes or truncation differ", phase, check.name)
		}
		digest, sourceDigest := sha256.Sum256(actual), sha256.Sum256(check.data)
		observations := a.proof["read_observations"].([]map[string]any)
		a.proof["read_observations"] = append(observations, map[string]any{
			"phase": phase, "case": check.name, "path": check.path, "owner": owner,
			"source_size": len(check.data), "source_sha256": hex.EncodeToString(sourceDigest[:]),
			"max_bytes": check.maxBytes, "bytes_read": len(actual), "sha256": hex.EncodeToString(digest[:]),
			"truncated": *read.Truncated, "close_acknowledged": read.CloseAcknowledged,
		})
	}
	a.observeReadErrors(t, ctx, owner, phase)
	a.observeDaemonReads(t, ctx, owner, phase, retained)
	if a.current(t) != owner {
		t.Fatal("native read phase replaced its prepared execution owner", phase)
	}
}

func (a *nativeHarnessArtifact) observeReadErrors(t *testing.T, ctx context.Context, owner nativeHarnessOwner, phase string) {
	t.Helper()
	cases := []struct {
		name        string
		environment string
		path        string
		bound       any
		omitBound   bool
		expected    string
	}{
		{"wrong_identity", uuid.NewString(), "bounded-read.bin", 1, false, "wrong_environment"},
		{"missing_file", a.environment, "missing-" + uuid.NewString(), 1, false, "not_found"},
		{"zero_bound", a.environment, "bounded-read.bin", 0, false, "invalid_request"},
		{"negative_bound", a.environment, "bounded-read.bin", -1, false, "invalid_request"},
		{"oversized_bound", a.environment, "bounded-read.bin", nativeHarnessReadMaximum + 1, false, "invalid_request"},
		{"missing_bound", a.environment, "bounded-read.bin", nil, true, "invalid_request"},
		{"null_bound", a.environment, "bounded-read.bin", nil, false, "invalid_request"},
		{"fractional_bound", a.environment, "bounded-read.bin", 1.5, false, "invalid_request"},
	}
	for _, check := range cases {
		request := map[string]any{"environment_id": check.environment, "path": check.path, "operation": "read"}
		if !check.omitBound {
			request["max_bytes"] = check.bound
		}
		response := a.request(t, ctx, owner, request, 16*1024)
		if response.Error != check.expected || len(response.Metadata) != 0 || len(response.Read) != 0 {
			t.Fatal("private native read error differs", phase, check.name, response.Error)
		}
		observations := a.proof["read_error_observations"].([]map[string]any)
		a.proof["read_error_observations"] = append(observations, map[string]any{
			"phase": phase, "case": check.name, "owner": owner, "error": response.Error,
		})
	}
}
