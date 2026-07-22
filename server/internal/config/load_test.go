package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// envMap is the stub Load tests use in place of os.Getenv.
type envMap map[string]string

func (e envMap) get(k string) string { return e[k] }

// fakeFiles is the in-memory FileReader used by file-loading tests.
type fakeFiles map[string][]byte

func (f fakeFiles) read(p string) ([]byte, error) {
	data, ok := f[p]
	if !ok {
		return nil, fmt.Errorf("no such file: %s", p)
	}
	return data, nil
}

func TestLoadDefaultsThenEnvOverride(t *testing.T) {
	env := envMap{
		EnvDatabaseURL: "postgres://postgres@localhost:5432/parsar",
		EnvDevAuth:     "true",
	}
	res, err := Load(env.get, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := res.Config.Database.URL; got != "postgres://postgres@localhost:5432/parsar" {
		t.Fatalf("database url = %q", got)
	}
	if !res.Config.Auth.DevAuth {
		t.Fatalf("dev_auth should be true")
	}
	if got := res.Config.Server.Addr; got != ":8080" {
		t.Fatalf("default addr lost: %q", got)
	}
	if !strings.HasSuffix(res.Config.Server.DataDir, "/.parsar") {
		t.Fatalf("default data_dir lost: %q (expected absolute path ending in /.parsar)", res.Config.Server.DataDir)
	}
	if res.Source != SourceEnv {
		t.Fatalf("source = %s, want env", res.Source)
	}
	if res.Config.Profile() != ProfileDev {
		t.Fatalf("dev_auth=true should force dev profile, got %s", res.Config.Profile())
	}
}

func TestLoadFileThenEnvWins(t *testing.T) {
	path := "/abs/etc/parsar/config.yaml"
	files := fakeFiles{
		path: []byte(`server:
  addr: ":9090"
  public_url: "https://from-file.example.com"
database:
  url: "postgres://file-host/parsar"
auth:
  dev_auth: false
secret:
  master_key: "file-master-key"
gateway:
  feishu:
    mock: false
    app_id: "cli_file"
sandbox:
  runner: "local"
`),
	}
	env := envMap{
		EnvConfigFile: path,
		EnvAddr:       ":7070",
		EnvFeishuMock: "true",
		// secret master key intentionally NOT set — file value
		// must survive.
	}
	res, err := Load(env.get, files.read)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := res.Config.Server.Addr; got != ":7070" {
		t.Fatalf("env should override file addr, got %q", got)
	}
	if got := res.Config.Server.PublicURL; got != "https://from-file.example.com" {
		t.Fatalf("file public_url lost, got %q", got)
	}
	if got := res.Config.Secret.MasterKey; got != "file-master-key" {
		t.Fatalf("file master key lost, got %q", got)
	}
	if !res.Config.Gateway.Feishu.Mock {
		t.Fatalf("env feishu mock=true should override file false")
	}
	if res.FilePath != path {
		t.Fatalf("file path = %q", res.FilePath)
	}
	if res.Source != SourceFileAndEnv {
		t.Fatalf("source = %s, want file+env", res.Source)
	}
}

func TestLoadConfigFilePathMustBeAbsolute(t *testing.T) {
	env := envMap{
		EnvConfigFile:  "./config.yaml",
		EnvDatabaseURL: "postgres://localhost/parsar",
	}
	_, err := Load(env.get, func(string) ([]byte, error) { return nil, nil })
	if err == nil {
		t.Fatalf("expected error for relative config path")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error should wrap ErrInvalidConfig: %v", err)
	}
}

func TestLoadConfigFileTildeIsExpanded(t *testing.T) {
	// We can't easily fake os.UserHomeDir without touching real env;
	// instead, assert the file reader receives an absolute path.
	var seen string
	files := func(p string) ([]byte, error) {
		seen = p
		return []byte(`database:
  url: "postgres://localhost/parsar"
auth:
  dev_auth: true
`), nil
	}
	env := envMap{
		EnvConfigFile: "~/parsar.yaml",
	}
	res, err := Load(env.get, files)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !filepath.IsAbs(seen) {
		t.Fatalf("file reader received non-absolute path: %q", seen)
	}
	if !strings.HasSuffix(seen, "parsar.yaml") {
		t.Fatalf("file reader path lost trailing segment: %q", seen)
	}
	if res.FilePath != seen {
		t.Fatalf("LoadResult.FilePath = %q, want reader-received %q", res.FilePath, seen)
	}
}

func TestLoadUnknownKeyRejected(t *testing.T) {
	path := "/abs/config.yaml"
	files := fakeFiles{
		path: []byte(`serverr:
  addr: ":8080"
database:
  url: "postgres://localhost/parsar"
`),
	}
	env := envMap{EnvConfigFile: path}
	_, err := Load(env.get, files.read)
	if err == nil {
		t.Fatalf("expected error for unknown YAML key")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error should wrap ErrInvalidConfig: %v", err)
	}
	if !strings.Contains(err.Error(), "serverr") {
		t.Fatalf("error should name the offending key: %v", err)
	}
}

func TestLoadEmptyDatabaseURLFailsInProd(t *testing.T) {
	// Prod (no dev knob) without DATABASE_URL must refuse to
	// start. Dev profile treats empty as a degraded-mode warning.
	env := envMap{
		EnvMasterKey: "abc",
		EnvPublicURL: "https://parsar.example.com",
	}
	_, err := Load(env.get, nil)
	if err == nil {
		t.Fatalf("expected error for missing DATABASE_URL in prod")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error should wrap ErrInvalidConfig: %v", err)
	}
	if !strings.Contains(err.Error(), "database.url") {
		t.Fatalf("error should mention database.url: %v", err)
	}
}

func TestLoadEmptyDatabaseURLPermittedInDev(t *testing.T) {
	// Dev profile permits empty DATABASE_URL (health-only degraded
	// path for `make server`).
	env := envMap{EnvDevAuth: "true"}
	res, err := Load(env.get, nil)
	if err != nil {
		t.Fatalf("dev profile should permit empty DATABASE_URL, got: %v", err)
	}
	if res.Config.Database.URL != "" {
		t.Fatalf("URL should remain empty, got %q", res.Config.Database.URL)
	}
}

func TestBuildPublicURL(t *testing.T) {
	cfg := Default()
	cfg.Server.PublicURL = " https://parsar.example.com/base/ "
	if got := cfg.BuildPublicURL("api/v1/auth/feishu/callback"); got != "https://parsar.example.com/base/api/v1/auth/feishu/callback" {
		t.Fatalf("BuildPublicURL with configured public_url = %q", got)
	}

	cfg = Default()
	cfg.Auth.DevAuth = true
	if got := cfg.BuildPublicURL("/api/v1/auth/feishu/callback"); got != "http://127.0.0.1:18081/api/v1/auth/feishu/callback" {
		t.Fatalf("BuildPublicURL dev fallback = %q", got)
	}

	cfg = Default()
	cfg.Database.URL = "postgres://localhost/parsar"
	cfg.Secret.MasterKey = "abc"
	cfg.Auth.Cookie.Secure = true
	defer func() {
		if recover() == nil {
			t.Fatal("expected production BuildPublicURL without public_url to panic")
		}
	}()
	_ = cfg.BuildPublicURL("/api/v1/auth/feishu/callback")
}

func TestValidateProductionRequiresMasterKey(t *testing.T) {
	cfg := Default()
	cfg.Database.URL = "postgres://localhost/parsar"
	cfg.Auth.Cookie.Secure = true
	cfg.Server.PublicURL = "https://parsar.example.com"
	err := cfg.Validate()
	if err == nil {
		t.Fatalf("expected error for missing master key in prod")
	}
	if !strings.Contains(err.Error(), "master_key") {
		t.Fatalf("error should mention master_key: %v", err)
	}
}

func TestValidateProductionRejectsDevAuth(t *testing.T) {
	cfg := Default()
	cfg.Database.URL = "postgres://localhost/parsar"
	cfg.Auth.Cookie.Secure = true
	cfg.Secret.MasterKey = "abc"
	cfg.Server.PublicURL = "https://parsar.example.com"
	cfg.Auth.DevAuth = true
	if cfg.Profile() != ProfileDev {
		t.Fatalf("dev_auth=true should force dev profile, got %s", cfg.Profile())
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("dev profile with dev_auth=true should validate: %v", err)
	}
}

func TestLoadProductionNormalizesCookieSecure(t *testing.T) {
	env := envMap{
		EnvDatabaseURL: "postgres://localhost/parsar",
		EnvMasterKey:   "abc",
		EnvPublicURL:   "https://parsar.example.com",
	}
	res, err := Load(env.get, nil)
	if err != nil {
		t.Fatalf("production load should succeed, got error: %v", err)
	}
	if !res.Config.Auth.Cookie.Secure {
		t.Fatalf("production load should derive cookie secure true, got false")
	}
}

func TestValidateRejectsBadSandboxRunner(t *testing.T) {
	cfg := Default()
	cfg.Database.URL = "postgres://localhost/parsar"
	cfg.Secret.MasterKey = "abc"
	cfg.Auth.Cookie.Secure = true
	cfg.Server.PublicURL = "https://parsar.example.com"
	cfg.Sandbox.Runner = "ec2"
	err := cfg.Validate()
	if err == nil {
		t.Fatalf("expected error for bad sandbox.runner")
	}
	if !strings.Contains(err.Error(), "sandbox.runner") {
		t.Fatalf("error should mention sandbox.runner: %v", err)
	}
}

func TestRedactedHidesSecrets(t *testing.T) {
	cfg := Default()
	cfg.Database.URL = "postgres://user:supersecret@host:5432/parsar?sslmode=disable"
	cfg.Secret.MasterKey = "MASTERKEY-32-chars-long-secret-x"
	cfg.Gateway.Feishu.AppSecret = "feishu-secret"
	cfg.Gateway.Feishu.VerificationToken = "verify-token"
	cfg.Gateway.Feishu.EncryptKey = "encrypt-key"

	r := cfg.Redacted()
	for label, v := range map[string]string{
		"Secret.MasterKey":                 r.Secret.MasterKey,
		"Gateway.Feishu.AppSecret":         r.Gateway.Feishu.AppSecret,
		"Gateway.Feishu.VerificationToken": r.Gateway.Feishu.VerificationToken,
		"Gateway.Feishu.EncryptKey":        r.Gateway.Feishu.EncryptKey,
	} {
		if strings.Contains(v, "secret") || strings.Contains(v, "feishu-secret") || strings.Contains(v, "verify-token") || strings.Contains(v, "encrypt-key") {
			t.Fatalf("%s leaked secret in redacted form: %q", label, v)
		}
	}
	if strings.Contains(r.Database.URL, "supersecret") {
		t.Fatalf("database URL leaks password in redacted form: %q", r.Database.URL)
	}
	if !strings.Contains(r.Database.URL, "host:5432") {
		t.Fatalf("redacted database URL lost host info: %q", r.Database.URL)
	}
}

func TestRedactedOnEmptyConfig(t *testing.T) {
	// Empty fields should redact to empty strings — no
	// "<redacted len=0>" artifacts polluting the output.
	r := Default().Redacted()
	if r.Secret.MasterKey != "" {
		t.Fatalf("empty master key should stay empty in redacted form: %q", r.Secret.MasterKey)
	}
}

func TestParseBool(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
		ok   bool
	}{
		{"true", true, true},
		{"True", true, true},
		{"YES", true, true},
		{"1", true, true},
		{"on", true, true},
		{"false", false, true},
		{"NO", false, true},
		{"0", false, true},
		{"off", false, true},
		{"", false, false},
		{"banana", false, false},
	} {
		got, ok := parseBool(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("parseBool(%q) = (%v, %v); want (%v, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestApplyEnvEmptyDoesNotClearFile(t *testing.T) {
	// Regression: empty env vars must not zero file values.
	path := "/abs/config.yaml"
	files := fakeFiles{
		path: []byte(`server:
  public_url: "https://kept.example.com"
database:
  url: "postgres://localhost/parsar"
auth:
  dev_auth: true
`),
	}
	env := envMap{
		EnvConfigFile: path,
		EnvPublicURL:  "", // explicit empty should NOT override
	}
	res, err := Load(env.get, files.read)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.Config.Server.PublicURL != "https://kept.example.com" {
		t.Fatalf("file public_url cleared by empty env: %q", res.Config.Server.PublicURL)
	}
}

// TestAuditOTLPDefaultsToDisabled guards against a regression that
// would silently open :4318 on every Parsar process.
func TestAuditOTLPDefaultsToDisabled(t *testing.T) {
	cfg := Default()
	if cfg.Audit.OTLP.Enabled {
		t.Fatalf("audit.otlp.enabled should default to false; got true")
	}
	if cfg.Audit.OTLP.Addr != ":4318" {
		t.Fatalf("audit.otlp.addr default = %q, want :4318", cfg.Audit.OTLP.Addr)
	}
}

// TestAuditOTLPEnvOverridesYAML asserts standard env-over-YAML
// precedence for audit.otlp.* keys.
func TestAuditOTLPEnvOverridesYAML(t *testing.T) {
	path := "/abs/config.yaml"
	files := fakeFiles{
		path: []byte(`database:
  url: "postgres://localhost/parsar"
auth:
  dev_auth: true
audit:
  otlp:
    enabled: false
    addr: ":4318"
`),
	}
	env := envMap{
		EnvConfigFile:       path,
		EnvAuditOTLPEnabled: "true",
		EnvAuditOTLPAddr:    "127.0.0.1:14318",
	}
	res, err := Load(env.get, files.read)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !res.Config.Audit.OTLP.Enabled {
		t.Fatalf("env should flip otlp.enabled to true")
	}
	if got := res.Config.Audit.OTLP.Addr; got != "127.0.0.1:14318" {
		t.Fatalf("env should override otlp.addr, got %q", got)
	}
}

// TestValidateRejectsEnabledOTLPWithBlankAddr asserts operators
// who clear the addr after flipping enabled=true get an immediate
// startup failure rather than a confusing net.Listen error.
func TestValidateRejectsEnabledOTLPWithBlankAddr(t *testing.T) {
	cfg := Default()
	cfg.Database.URL = "postgres://localhost/parsar"
	cfg.Secret.MasterKey = "abc"
	cfg.Auth.Cookie.Secure = true
	cfg.Server.PublicURL = "https://parsar.example.com"
	cfg.Audit.OTLP.Enabled = true
	cfg.Audit.OTLP.Addr = ""
	cfg.Audit.OTLP.SigningKey = "k"
	err := cfg.Validate()
	if err == nil {
		t.Fatalf("expected error when otlp enabled with blank addr")
	}
	if !strings.Contains(err.Error(), "audit.otlp.addr") {
		t.Fatalf("error should mention audit.otlp.addr: %v", err)
	}
}

// TestValidateRejectsEnabledOTLPWithBlankSigningKeyInProd asserts
// production needs a signing key when the receiver is enabled. Dev
// skips because cmd/server substitutes a stable dev constant.
func TestValidateRejectsEnabledOTLPWithBlankSigningKeyInProd(t *testing.T) {
	cfg := Default()
	cfg.Database.URL = "postgres://localhost/parsar"
	cfg.Secret.MasterKey = "abc"
	cfg.Auth.Cookie.Secure = true
	cfg.Server.PublicURL = "https://parsar.example.com"
	cfg.Audit.OTLP.Enabled = true
	cfg.Audit.OTLP.Addr = ":4318"
	cfg.Audit.OTLP.SigningKey = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatalf("expected production error when otlp enabled without signing key")
	}
	if !strings.Contains(err.Error(), "audit.otlp.signing_key") {
		t.Fatalf("error should mention audit.otlp.signing_key: %v", err)
	}
}

// TestValidateAllowsBlankSigningKeyInDev mirrors the master-key dev
// fallback — cmd/server substitutes a stable dev constant before
// NewSigner runs.
func TestValidateAllowsBlankSigningKeyInDev(t *testing.T) {
	cfg := Default()
	cfg.Database.URL = "postgres://localhost/parsar"
	cfg.Auth.DevAuth = true
	cfg.Audit.OTLP.Enabled = true
	cfg.Audit.OTLP.Addr = ":4318"
	cfg.Audit.OTLP.SigningKey = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("dev profile with blank signing key should validate: %v", err)
	}
}

// TestRedactedHidesOTLPSigningKey covers the new key so a stray
// log of Config.Redacted() does not leak it.
func TestRedactedHidesOTLPSigningKey(t *testing.T) {
	cfg := Default()
	cfg.Audit.OTLP.SigningKey = "supersecret-otlp-key"
	r := cfg.Redacted()
	if strings.Contains(r.Audit.OTLP.SigningKey, "supersecret") {
		t.Fatalf("redacted otlp signing key leaks original: %q", r.Audit.OTLP.SigningKey)
	}
	if r.Audit.OTLP.SigningKey == "" {
		t.Errorf("non-empty key should redact to a placeholder, not empty")
	}
}

// TestAuditOTLPExternalEndpointEnvOverride asserts env overrides
// YAML for spawn-time OTel env injection.
func TestAuditOTLPExternalEndpointEnvOverride(t *testing.T) {
	path := "/abs/config.yaml"
	files := fakeFiles{
		path: []byte(`database:
  url: "postgres://localhost/parsar"
auth:
  dev_auth: true
audit:
  otlp:
    enabled: true
    addr: ":4318"
    external_endpoint: "https://from-file.example.com:4318"
`),
	}
	env := envMap{
		EnvConfigFile:                path,
		EnvAuditOTLPExternalEndpoint: "https://from-env.example.com:4318",
	}
	res, err := Load(env.get, files.read)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := res.Config.Audit.OTLP.ExternalEndpoint; got != "https://from-env.example.com:4318" {
		t.Fatalf("env should override file external_endpoint; got %q", got)
	}
}

// TestAuditOTLPExternalEndpointEmptyByDefault confirms spawn-time
// env injection stays opt-in.
func TestAuditOTLPExternalEndpointEmptyByDefault(t *testing.T) {
	cfg := Default()
	if cfg.Audit.OTLP.ExternalEndpoint != "" {
		t.Errorf("audit.otlp.external_endpoint should default to empty; got %q",
			cfg.Audit.OTLP.ExternalEndpoint)
	}
}

// TestAuditOTLPFanoutEndpointEmptyByDefault: with no customer
// collector endpoint, only the canonical PostgresSink fires.
func TestAuditOTLPFanoutEndpointEmptyByDefault(t *testing.T) {
	cfg := Default()
	if cfg.Audit.OTLP.FanoutEndpoint != "" {
		t.Errorf("audit.otlp.fanout_endpoint should default to empty; got %q",
			cfg.Audit.OTLP.FanoutEndpoint)
	}
}

// TestAuditOTLPFanoutEndpointEnvOverride asserts standard env-over-YAML
// precedence for fanout_endpoint.
func TestAuditOTLPFanoutEndpointEnvOverride(t *testing.T) {
	path := "/abs/config.yaml"
	files := fakeFiles{
		path: []byte(`database:
  url: "postgres://localhost/parsar"
auth:
  dev_auth: true
audit:
  otlp:
    fanout_endpoint: "https://from-file.example.com:4318"
`),
	}
	env := envMap{
		EnvConfigFile:              path,
		EnvAuditOTLPFanoutEndpoint: "https://from-env.example.com:4318",
	}
	res, err := Load(env.get, files.read)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := res.Config.Audit.OTLP.FanoutEndpoint; got != "https://from-env.example.com:4318" {
		t.Fatalf("env should override file fanout_endpoint; got %q", got)
	}
}

func TestPlatformAdminUserIDsEnvParsed(t *testing.T) {
	env := envMap{
		EnvDatabaseURL:          "postgres://localhost/parsar",
		EnvDevAuth:              "true",
		EnvPlatformAdminUserIDs: " 00000000-0000-0000-0000-0000000000ad ,,  00000000-0000-0000-0000-0000000000aa ",
	}
	res, err := Load(env.get, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := res.Config.Auth.PlatformAdminUserIDs
	want := []string{"00000000-0000-0000-0000-0000000000ad", "00000000-0000-0000-0000-0000000000aa"}
	if len(got) != len(want) {
		t.Fatalf("platform admin ids length = %d (%v), want %d", len(got), got, len(want))
	}
	for i, id := range want {
		if got[i] != id {
			t.Fatalf("platform admin id[%d] = %q, want %q", i, got[i], id)
		}
	}
}

func TestPlatformAdminUserIDsEnvEmpty(t *testing.T) {
	env := envMap{
		EnvDatabaseURL: "postgres://localhost/parsar",
		EnvDevAuth:     "true",
	}
	res, err := Load(env.get, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(res.Config.Auth.PlatformAdminUserIDs) != 0 {
		t.Fatalf("unset env should leave allowlist empty; got %v", res.Config.Auth.PlatformAdminUserIDs)
	}
}

func TestProfileLoopbackPublicURL(t *testing.T) {
	cases := []struct {
		name      string
		publicURL string
		want      Profile
	}{
		{"http loopback ipv4", "http://127.0.0.1:18088", ProfileDev},
		{"https loopback localhost", "https://localhost", ProfileDev},
		{"http loopback 127.x non-.1", "http://127.0.0.2:8080", ProfileDev},
		{"http loopback ipv6", "http://[::1]:8080", ProfileDev},
		{"https loopback ipv4", "https://127.0.0.1", ProfileDev},
		{"real domain", "https://parsar.example.com", ProfileProd},
		{"empty", "", ProfileProd},
		{"unparseable", "://bad", ProfileProd},
		{"non-loopback ip", "http://10.0.0.5:8080", ProfileProd},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.Server.PublicURL = tc.publicURL
			if got := cfg.Profile(); got != tc.want {
				t.Fatalf("Profile() with public_url=%q = %s, want %s", tc.publicURL, got, tc.want)
			}
		})
	}
}

// Real-Feishu (Mock=false, DevAuth unset) on a loopback PublicURL must
// load cleanly WITHOUT a master key or secure cookie — that is the whole
// point of the loopback dev signal. A non-loopback PublicURL with the
// same (absent) secrets must still be rejected, proving the relaxation
// is scoped to loopback.
func TestLoadRealFeishuLoopbackIsDev(t *testing.T) {
	env := envMap{
		EnvDatabaseURL:             "postgres://localhost/parsar",
		EnvPublicURL:               "http://127.0.0.1:18088",
		EnvFeishuMock:              "false",
		EnvFeishuAppID:             "cli_test",
		EnvFeishuAppSecret:         "secret_test",
		EnvFeishuVerificationToken: "tok",
	}
	res, err := Load(env.get, nil)
	if err != nil {
		t.Fatalf("loopback real-Feishu should load without master key / secure cookie: %v", err)
	}
	if res.Config.Profile() != ProfileDev {
		t.Fatalf("loopback real-Feishu Profile = %s, want dev", res.Config.Profile())
	}

	env[EnvPublicURL] = "https://parsar.example.com"
	if _, err := Load(env.get, nil); err == nil {
		t.Fatal("non-loopback real-Feishu without master key / secure cookie should be rejected")
	}
}

func TestBlobBackendDefaultsToPG(t *testing.T) {
	res, err := Load(envMap{EnvMasterKey: "abc", EnvDevAuth: "true"}.get, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := res.Config.Storage.BlobBackend; got != "pg" {
		t.Fatalf("default blob backend: got %q want pg", got)
	}
}

func TestBlobBackendEnvOverride(t *testing.T) {
	res, err := Load(envMap{
		EnvMasterKey:   "abc",
		EnvDevAuth:     "true",
		EnvBlobBackend: "oss",
	}.get, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := res.Config.Storage.BlobBackend; got != "oss" {
		t.Fatalf("env override: got %q want oss", got)
	}
}
