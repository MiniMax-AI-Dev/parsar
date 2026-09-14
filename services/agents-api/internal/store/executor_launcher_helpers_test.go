package store_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

const launcherTestHost = "agents-executor.test"

func launcherTestCertificate(t *testing.T, root string) tls.Certificate {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: launcherTestHost},
		DNSNames: []string{launcherTestHost}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	leafPublic, leafKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: launcherTestHost},
		DNSNames: []string{launcherTestHost}, NotBefore: template.NotBefore, NotAfter: template.NotAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, template, leafPublic, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{leafDER, der}, PrivateKey: leafKey}
}

func launcherContainerArgs(t *testing.T, binary string) ([]string, string) {
	t.Helper()
	if filepath.Base(binary) != "codex" || filepath.Base(filepath.Dir(binary)) != "bin" {
		t.Fatal("fixture requires the native Codex platform installation's bin/codex")
	}
	name := "parsar-executor-client-" + uuid.NewString()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
	})
	return []string{"run", "--name", name, "--network", "host",
		"--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()),
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--security-opt", "seccomp=unconfined", "--security-opt", "apparmor=unconfined",
		"--add-host", launcherTestHost + ":127.0.0.1", "--add-host", "wrong." + launcherTestHost + ":127.0.0.1",
		"--mount", "type=bind,src=" + filepath.Dir(filepath.Dir(binary)) + ",dst=/opt/codex,readonly",
		"--env", "NO_PROXY=*", "--env", "no_proxy=*",
		"--env", "HTTP_PROXY=", "--env", "HTTPS_PROXY=", "--env", "ALL_PROXY=",
		"--env", "http_proxy=", "--env", "https_proxy=", "--env", "all_proxy="}, name
}

func startLauncherContainer(t *testing.T, ctx context.Context, root, image, binary, launcher, remote, environment string, trusted bool) string {
	t.Helper()
	args, name := launcherContainerArgs(t, binary)
	args = append(args, "--detach", "--workdir", root,
		"--mount", "type=bind,src="+root+",dst="+root,
		"--env", "HOME="+filepath.Join(root, "executor"),
		"--mount", "type=bind,src="+launcher+",dst=/usr/local/bin/agents-api-codex-executor,readonly")
	if trusted {
		args = append(args, "--env", "SSL_CERT_FILE="+filepath.Join(root, "ca.pem"))
	}
	args = append(args, "--entrypoint", "/usr/local/bin/agents-api-codex-executor", image,
		"--remote", remote, "--environment-id", environment, "--credentials", filepath.Join(root, "executor", "credential.json"),
		"--codex-bin", "/opt/codex/bin/codex")
	if err := exec.CommandContext(ctx, "docker", args...).Run(); err != nil {
		t.Fatal("launcher container failed", err)
	}
	return name
}

func stopLauncherContainer(t *testing.T, ctx context.Context, name string, success bool) {
	t.Helper()
	if err := exec.CommandContext(ctx, "docker", "stop", "--time", "20", name).Run(); err != nil {
		t.Fatal(err)
	}
	exit, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.ExitCode}}", name).Output()
	code := strings.TrimSpace(string(exit))
	if err != nil || (code != "0" && (success || code != "1")) {
		t.Fatal("launcher did not stop gracefully", err)
	}
}

func startLauncherProbe(t *testing.T, ctx context.Context, root, image, binary, probe, remote, environment, token, phase string) *relayProcess {
	t.Helper()
	args, _ := launcherContainerArgs(t, binary)
	args = append(args, "--rm", "--workdir", root,
		"--mount", "type=bind,src="+root+",dst="+root,
		"--env", "HOME="+filepath.Join(root, "harness"),
		"--env", "SSL_CERT_FILE="+filepath.Join(root, "ca.pem"),
		"--env", "PARSAR_NATIVE_ENV_PROOF="+root,
		"--env", "CODEX_EXEC_SERVER_NOISE_REGISTRY_URL="+remote,
		"--env", "CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID="+environment,
		"--env", "CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN",
		"--mount", "type=bind,src="+probe+",dst=/usr/local/bin/probe,readonly",
		"--entrypoint", "/usr/local/bin/probe", image, phase)
	return startRelayProcess(t, ctx, root, append(os.Environ(), "CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN="+token), "docker", args...)
}

func writeLauncherCredential(t *testing.T, path string, credential store.IssuedExecutorCredential) {
	t.Helper()
	data, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func startDaemonLauncherExecutor(t *testing.T, ctx context.Context, root, local, remote, binary, image, registryURL, environment string, credential store.IssuedExecutorCredential, launcher string) string {
	t.Helper()
	writeLauncherCredential(t, filepath.Join(root, "executor", "credential.json"), credential)
	args, name := launcherContainerArgs(t, binary)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		logs, _ := exec.CommandContext(ctx, "docker", "logs", name).CombinedOutput()
		text := string(logs)
		if strings.Contains(text, credential.Token) {
			t.Error("executor credential appeared in launcher diagnostics")
			text = strings.ReplaceAll(text, credential.Token, "[redacted]")
		}
		_ = os.WriteFile(filepath.Join(root, "launcher.stderr"), []byte(text), 0600)
	})
	args = append(args, "--detach", "--workdir", remote,
		"--mount", "type=bind,src="+filepath.Join(root, "executor")+",dst="+filepath.Join(root, "executor"),
		"--env", "HOME="+filepath.Join(root, "executor"),
		"--mount", "type=bind,src="+launcher+",dst=/usr/local/bin/agents-api-codex-executor,readonly",
		"--mount", "type=bind,src="+local+",dst="+remote,
		"--entrypoint", "/usr/local/bin/agents-api-codex-executor", image,
		"--remote", registryURL, "--environment-id", environment, "--credentials", filepath.Join(root, "executor", "credential.json"),
		"--codex-bin", "/opt/codex/bin/codex")
	if err := exec.CommandContext(ctx, "docker", args...).Run(); err != nil {
		t.Fatal("daemon test launcher container failed", err)
	}
	for _, private := range []string{"harness", "parsar-daemon"} {
		if err := exec.CommandContext(ctx, "docker", "exec", name, "test", "!", "-e", filepath.Join(root, private)).Run(); err != nil {
			t.Fatal("private harness/daemon state is visible inside executor")
		}
	}
	return name
}
