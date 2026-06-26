package e2b

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	inClusterTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	inClusterCAPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	kubeAPIServer      = "https://kubernetes.default.svc"
)

// PodIPResolver resolves the cluster-internal pod IP from an E2B
// sandbox ID. ACS SandboxSet sandbox IDs follow the format
// "<namespace>--<podname>". Lets envd RunCommand bypass the
// external gateway and hit the pod directly via cluster network.
type PodIPResolver struct {
	apiServer string
	token     string
	client    *http.Client
}

// NewPodIPResolver creates a resolver with an explicit API server URL,
// bearer token, and optional CA PEM bundle. Empty apiServer falls back
// to in-cluster (kubernetes.default.svc). Nil caPEM uses the system
// root pool; insecure=true skips TLS verification.
func NewPodIPResolver(apiServer, token string, caPEM []byte, insecure bool) *PodIPResolver {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	apiServer = strings.TrimRight(strings.TrimSpace(apiServer), "/")
	if apiServer == "" {
		apiServer = kubeAPIServer
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if insecure {
		tlsConfig.InsecureSkipVerify = true
	} else if len(caPEM) > 0 {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caPEM)
		tlsConfig.RootCAs = pool
	}
	return &PodIPResolver{
		apiServer: apiServer,
		token:     token,
		client: &http.Client{
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
		},
	}
}

// NewInClusterPodIPResolver reads the in-cluster service account
// credentials and returns a resolver. Returns nil when not running
// inside Kubernetes (credential files absent).
func NewInClusterPodIPResolver() *PodIPResolver {
	tokenBytes, err := os.ReadFile(inClusterTokenPath)
	if err != nil {
		return nil
	}
	var caPEM []byte
	if ca, err := os.ReadFile(inClusterCAPath); err == nil {
		caPEM = ca
	}
	return NewPodIPResolver("", string(tokenBytes), caPEM, false)
}

// Resolve looks up the pod IP for sandboxID and returns a direct envd
// base URL like "http://10.0.0.1:49983" for RunCommandInput.EnvdURL.
func (r *PodIPResolver) Resolve(ctx context.Context, sandboxID string, port int) (string, error) {
	ns, podName, err := ParseSandboxID(sandboxID)
	if err != nil {
		return "", err
	}
	if port <= 0 {
		port = DefaultEnvdPort
	}

	url := fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s", r.apiServer, ns, podName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("podip: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("podip: k8s api call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("podip: k8s api status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var pod struct {
		Status struct {
			PodIP string `json:"podIP"`
		} `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pod); err != nil {
		return "", fmt.Errorf("podip: decode response: %w", err)
	}
	if pod.Status.PodIP == "" {
		return "", fmt.Errorf("podip: pod %s/%s has no IP yet", ns, podName)
	}

	return fmt.Sprintf("http://%s:%d", pod.Status.PodIP, port), nil
}

// ParseSandboxID splits an ACS SandboxSet sandbox ID
// ("<namespace>--<podname>") into its components.
func ParseSandboxID(sandboxID string) (namespace, podName string, err error) {
	idx := strings.Index(sandboxID, "--")
	if idx <= 0 || idx+2 >= len(sandboxID) {
		return "", "", fmt.Errorf("podip: sandbox ID %q does not match <namespace>--<podname> format", sandboxID)
	}
	return sandboxID[:idx], sandboxID[idx+2:], nil
}
