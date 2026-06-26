package oss

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

func TestConfigEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"empty", Config{}, false},
		{"missing bucket", Config{Region: "cn-hangzhou", AccessKeyID: "ak", AccessKeySecret: "sk"}, false},
		{"missing ak", Config{Region: "cn-hangzhou", Bucket: "b", AccessKeySecret: "sk"}, false},
		{"complete", Config{Region: "cn-hangzhou", Bucket: "b", AccessKeyID: "ak", AccessKeySecret: "sk"}, true},
		{"whitespace region", Config{Region: "   ", Bucket: "b", AccessKeyID: "ak", AccessKeySecret: "sk"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.cfg.Enabled(); got != tc.want {
				t.Fatalf("Enabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cfg       Config
		wantErr   bool
		wantParts []string // substrings expected in the joined message
	}{
		{
			"complete ok",
			Config{Region: "cn-hangzhou", Bucket: "b", AccessKeyID: "ak", AccessKeySecret: "sk"},
			false, nil,
		},
		{
			"reports every missing field",
			Config{},
			true,
			[]string{"region is required", "bucket is required", "access_key_id is required", "access_key_secret is required"},
		},
		{
			"reject non-http base url",
			Config{Region: "cn", Bucket: "b", AccessKeyID: "a", AccessKeySecret: "s", BaseURL: "example.com"},
			true,
			[]string{"base_url must start with http"},
		},
		{
			"accept https base url",
			Config{Region: "cn", Bucket: "b", AccessKeyID: "a", AccessKeySecret: "s", BaseURL: "https://b.oss-cn.aliyuncs.com"},
			false, nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
			if !tc.wantErr {
				return
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Validate() error does not wrap ErrInvalidConfig: %v", err)
			}
			msg := err.Error()
			for _, want := range tc.wantParts {
				if !strings.Contains(msg, want) {
					t.Fatalf("Validate() error %q missing substring %q", msg, want)
				}
			}
		})
	}
}

func TestNormalizeTTL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   time.Duration
		want time.Duration
	}{
		{0, DefaultPresignTTL},
		{-1 * time.Hour, DefaultPresignTTL},
		{30 * time.Second, time.Minute},
		{15 * time.Minute, 15 * time.Minute},
		{MaxPresignTTL, MaxPresignTTL},
		{MaxPresignTTL + time.Hour, MaxPresignTTL},
	}
	for _, tc := range tests {
		t.Run(tc.in.String(), func(t *testing.T) {
			t.Parallel()
			if got := normalizeTTL(tc.in); got != tc.want {
				t.Fatalf("normalizeTTL(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestNewPluginObjectKey(t *testing.T) {
	t.Parallel()

	const wid = "ws-1"
	key := NewPluginObjectKey(wid, "my-plugin.zip")
	if !strings.HasPrefix(key, PluginObjectPrefix+"/"+wid+"/") {
		t.Fatalf("key missing workspace-scoped prefix: %q", key)
	}
	if !strings.HasSuffix(key, "/my-plugin.zip") {
		t.Fatalf("key missing filename suffix: %q", key)
	}

	// Two consecutive calls must produce different keys.
	first := NewPluginObjectKey(wid, "x.zip")
	second := NewPluginObjectKey(wid, "x.zip")
	if first == second {
		t.Fatal("expected distinct keys across calls")
	}

	if !strings.HasSuffix(NewPluginObjectKey(wid, ""), "/plugin.zip") {
		t.Fatal("empty filename should default to plugin.zip")
	}

	got := NewPluginObjectKey(wid, "../../etc/passwd")
	if !strings.HasSuffix(got, "/passwd") {
		t.Fatalf("path-traversal filename not basenamed: %q", got)
	}
	// ".." filename must NOT collapse via path.Clean back to the
	// prefix dir. The sanitizer substitutes the default.
	traversal := NewPluginObjectKey(wid, "..")
	if !strings.HasSuffix(traversal, "/plugin.zip") {
		t.Fatalf("filename=\"..\" did not fall back to plugin.zip: %q", traversal)
	}
	parts := strings.Split(got, "/")
	if parts[len(parts)-1] != "passwd" {
		t.Fatalf("residual path segments survived: %q", got)
	}
}

func TestKeyBelongsToWorkspace(t *testing.T) {
	t.Parallel()
	mine := NewPluginObjectKey("ws-A", "x.zip")
	if !KeyBelongsToWorkspace(mine, "ws-A") {
		t.Fatalf("key=%q should belong to ws-A", mine)
	}
	if KeyBelongsToWorkspace(mine, "ws-B") {
		t.Fatalf("key=%q must NOT belong to ws-B", mine)
	}
	if KeyBelongsToWorkspace("", "ws-A") {
		t.Fatal("empty key should never belong")
	}
	if KeyBelongsToWorkspace(mine, "") {
		t.Fatal("empty workspace should never claim")
	}
	// Forged keys that look like the prefix but escape via .. must NOT claim.
	for _, bad := range []string{
		"capabilities/plugins/ws-B/uuid/x.zip",
		"capabilities/plugins/ws-A/../ws-B/uuid/x.zip",
		"../capabilities/plugins/ws-A/uuid/x.zip",
		"capabilities/plugins/ws-A",
		"capabilities/plugins/ws-A-suffix/", // prefix-substring attack
	} {
		if KeyBelongsToWorkspace(bad, "ws-A") {
			t.Fatalf("forged key %q must NOT belong to ws-A", bad)
		}
	}
}

func TestJoinKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   []string
		want string
	}{
		{[]string{"a", "b", "c"}, "a/b/c"},
		{[]string{"a/", "/b/", "/c"}, "a/b/c"},
		{[]string{"", "a", " ", "b"}, "a/b"},
		{nil, ""},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.in, "|"), func(t *testing.T) {
			t.Parallel()
			if got := JoinKey(tc.in...); got != tc.want {
				t.Fatalf("JoinKey(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	t.Parallel()

	_, err := New(Config{})
	if err == nil {
		t.Fatal("expected error from New(empty), got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestNilClientMethodsReturnNotConfigured(t *testing.T) {
	t.Parallel()

	var c *Client
	ctx := context.Background()
	if _, _, err := c.PresignPut(ctx, "x", time.Minute); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("PresignPut: %v, want ErrNotConfigured", err)
	}
	if _, _, err := c.PresignGet(ctx, "x", time.Minute); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("PresignGet: %v, want ErrNotConfigured", err)
	}
	if _, err := c.Download(ctx, "x"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Download: %v, want ErrNotConfigured", err)
	}
	if got := c.ObjectURL("x"); got != "" {
		t.Fatalf("ObjectURL on nil client: %q, want \"\"", got)
	}
	if got := c.Bucket(); got != "" {
		t.Fatalf("Bucket on nil client: %q, want \"\"", got)
	}
	if err := c.HealthCheck(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("HealthCheck on nil: %v, want ErrNotConfigured", err)
	}
}

func TestClientObjectURL(t *testing.T) {
	t.Parallel()

	t.Run("with base url", func(t *testing.T) {
		t.Parallel()
		c, err := New(Config{
			Region: "cn-hangzhou", Bucket: "test-bucket",
			AccessKeyID: "ak", AccessKeySecret: "sk",
			BaseURL: "https://test-bucket.oss-cn-hangzhou.aliyuncs.com/",
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		got := c.ObjectURL("/capabilities/plugins/uuid/x.zip")
		want := "https://test-bucket.oss-cn-hangzhou.aliyuncs.com/capabilities/plugins/uuid/x.zip"
		if got != want {
			t.Fatalf("ObjectURL = %q, want %q", got, want)
		}
	})

	t.Run("without base url derives from region", func(t *testing.T) {
		t.Parallel()
		c, err := New(Config{
			Region: "cn-hangzhou", Bucket: "test-bucket",
			AccessKeyID: "ak", AccessKeySecret: "sk",
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		got := c.ObjectURL("k")
		want := "https://test-bucket.oss-cn-hangzhou.aliyuncs.com/k"
		if got != want {
			t.Fatalf("ObjectURL = %q, want %q", got, want)
		}
	})
}

func TestClientPresignReturnsURLAndExpiration(t *testing.T) {
	t.Parallel()
	// Presign uses a nopHttpClient (SDK client_presign.go), so this
	// issues no network traffic — it just exercises the signer.

	c, err := New(Config{
		Region: "cn-hangzhou", Bucket: "test-bucket",
		AccessKeyID: "ak", AccessKeySecret: "sk",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	url, exp, err := c.PresignPut(testCtx(t), "k", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	if url == "" {
		t.Fatal("PresignPut returned empty URL")
	}
	if !strings.Contains(url, "k") {
		t.Fatalf("PresignPut URL missing key segment: %q", url)
	}
	if exp.IsZero() || exp.Before(time.Now()) {
		t.Fatalf("PresignPut returned bad expiration: %v", exp)
	}
}
