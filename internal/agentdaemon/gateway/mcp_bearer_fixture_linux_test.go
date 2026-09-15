//go:build linux

package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpBearerFixture struct {
	private, anonymous                             *httptest.Server
	caFile, memory                                 string
	mu                                             sync.Mutex
	accepted, rejected, anonymousRequests, crossed int
	calls                                          map[string]int
}

func newMCPBearerFixture(t *testing.T, root, token string) *mcpBearerFixture {
	t.Helper()
	f := &mcpBearerFixture{memory: "REMEMBER_" + mcpBearerNonce(t), calls: make(map[string]int)}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("cannot create owned TLS key")
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: "Owned MCP acceptance CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal("cannot create owned TLS certificate")
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("cannot create owned TLS leaf key")
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(now.UnixNano() + 1), Subject: pkix.Name{CommonName: "Owned MCP HTTPS endpoint"}, NotBefore: cert.NotBefore, NotAfter: cert.NotAfter, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, cert, &leafKey.PublicKey, key)
	if err != nil {
		t.Fatal("cannot sign owned HTTPS leaf certificate")
	}
	// Keep the host's provider trust roots alongside the owned MCP CA.
	var roots []byte
	for _, path := range []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt"} {
		if data, err := os.ReadFile(path); err == nil {
			roots = data
			break
		}
	}
	if len(roots) == 0 {
		t.Fatal("system CA bundle required for real provider TLS")
	}
	roots = append(append(roots, '\n'), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	f.caFile = filepath.Join(root, "trusted-ca.pem")
	if err := os.WriteFile(f.caFile, roots, 0600); err != nil {
		t.Fatal("cannot save owned trust bundle")
	}
	start := func(anonymous bool) *httptest.Server {
		server := mcp.NewServer(&mcp.Implementation{Name: "owned-bearer-acceptance", Version: "1"}, nil)
		names := []string{"remember", "fail"}
		if anonymous {
			names = []string{"ping"}
		}
		for _, name := range names {
			mcp.AddTool(server, &mcp.Tool{Name: name, Description: map[string]string{"remember": "Return the unpredictable value to remember.", "fail": "Return an intentional ordinary tool error. Do not retry.", "ping": "Confirm this separate anonymous MCP server works."}[name]}, func(_ context.Context, _ *mcp.CallToolRequest, args struct {
				Tag string `json:"tag" jsonschema:"The requested verification tag"`
			}) (*mcp.CallToolResult, any, error) {
				f.mu.Lock()
				f.calls[name]++
				f.mu.Unlock()
				text := f.memory
				if name == "fail" {
					text = "INTENTIONAL_MCP_TOOL_ERROR:" + args.Tag
				}
				if name == "ping" {
					text = "ANONYMOUS_OK"
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: name == "fail"}, nil, nil
			})
		}
		transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			allowed := len(r.Header.Values("Authorization")) == 1 && r.Header.Get("Authorization") == "Bearer "+token
			if anonymous {
				f.anonymousRequests++
				allowed = len(r.Header.Values("Authorization")) == 0
				if !allowed {
					f.crossed++
				}
			} else if allowed {
				f.accepted++
			} else {
				f.rejected++
			}
			f.mu.Unlock()
			if !allowed {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			transport.ServeHTTP(w, r)
		})
		s := httptest.NewUnstartedServer(handler)
		s.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, der}, PrivateKey: leafKey}}, MinVersion: tls.VersionTLS12}
		s.StartTLS()
		t.Cleanup(s.Close)
		return s
	}
	f.private, f.anonymous = start(false), start(true)
	for _, authorization := range []string{"", "Bearer wrong-" + mcpBearerNonce(t)} {
		req, _ := http.NewRequest(http.MethodPost, f.private.URL, strings.NewReader("{}"))
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		response, err := f.private.Client().Do(req)
		if err != nil {
			t.Fatal("owned HTTPS authorization probe failed")
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatal("missing or wrong bearer was accepted")
		}
	}
	return f
}

func (f *mcpBearerFixture) observations() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := make(map[string]int, len(f.calls))
	for name, count := range f.calls {
		calls[name] = count
	}
	return map[string]any{"authenticated_requests": f.accepted, "rejected_requests": f.rejected, "anonymous_requests": f.anonymousRequests, "cross_forwarded_authorization": f.crossed, "tool_calls": calls}
}
