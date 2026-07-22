package mcpcatalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	mcpcatalogdata "github.com/MiniMax-AI-Dev/parsar/catalog/mcp"
)

const (
	EnvCatalogURL          = "PARSAR_MCP_CATALOG_URL"
	defaultCacheTTL        = 5 * time.Minute
	defaultHTTPTimeout     = 5 * time.Second
	defaultMaxResponseSize = 2 << 20
)

type Source string

const (
	SourceBuiltin Source = "builtin"
	SourceRemote  Source = "remote"
)

type Snapshot struct {
	Catalog Catalog
	Source  Source
}

type Options struct {
	RemoteURL        string
	HTTPClient       *http.Client
	CacheTTL         time.Duration
	MaxResponseBytes int64
	BuiltinJSON      []byte
}

type Loader struct {
	remoteURL        *url.URL
	remoteConfigErr  error
	client           *http.Client
	cacheTTL         time.Duration
	maxResponseBytes int64
	builtin          Catalog
	builtinErr       error

	mu        sync.Mutex
	cached    Snapshot
	expiresAt time.Time
}

func New(options Options) *Loader {
	builtinJSON := options.BuiltinJSON
	if len(builtinJSON) == 0 {
		builtinJSON = mcpcatalogdata.CatalogJSON
	}
	builtin, builtinErr := Decode(builtinJSON)

	cacheTTL := options.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = defaultCacheTTL
	}
	maxResponseBytes := options.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultMaxResponseSize
	}

	client := http.Client{}
	if options.HTTPClient != nil {
		client = *options.HTTPClient
	}
	if client.Timeout <= 0 {
		client.Timeout = defaultHTTPTimeout
	}
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("too many catalog redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("catalog redirect uses unsupported scheme %q", req.URL.Scheme)
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		return nil
	}

	var remoteURL *url.URL
	var remoteConfigErr error
	if raw := strings.TrimSpace(options.RemoteURL); raw != "" {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
			remoteConfigErr = fmt.Errorf("%s must be an http or https URL without embedded credentials", EnvCatalogURL)
		} else {
			remoteURL = parsed
		}
	}

	return &Loader{
		remoteURL:        remoteURL,
		remoteConfigErr:  remoteConfigErr,
		client:           &client,
		cacheTTL:         cacheTTL,
		maxResponseBytes: maxResponseBytes,
		builtin:          builtin,
		builtinErr:       builtinErr,
	}
}

func (l *Loader) Load(ctx context.Context) (Snapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if !l.expiresAt.IsZero() && now.Before(l.expiresAt) {
		return l.cached, nil
	}

	var remoteErr error
	if l.remoteConfigErr != nil {
		remoteErr = l.remoteConfigErr
	} else if l.remoteURL != nil {
		catalog, err := l.loadRemote(ctx)
		if err == nil {
			l.cached = Snapshot{Catalog: catalog, Source: SourceRemote}
			l.expiresAt = now.Add(l.cacheTTL)
			return l.cached, nil
		}
		remoteErr = err
	}

	if l.builtinErr == nil {
		l.cached = Snapshot{Catalog: l.builtin, Source: SourceBuiltin}
		l.expiresAt = now.Add(l.cacheTTL)
		return l.cached, nil
	}
	if remoteErr != nil {
		return Snapshot{}, fmt.Errorf("load remote catalog: %v; load builtin catalog: %w", remoteErr, l.builtinErr)
	}
	return Snapshot{}, fmt.Errorf("load builtin catalog: %w", l.builtinErr)
}

func (l *Loader) loadRemote(ctx context.Context) (Catalog, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.remoteURL.String(), nil)
	if err != nil {
		return Catalog{}, fmt.Errorf("build catalog request: %w", err)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return Catalog{}, fmt.Errorf("fetch catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Catalog{}, fmt.Errorf("fetch catalog: unexpected HTTP status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, l.maxResponseBytes+1))
	if err != nil {
		return Catalog{}, fmt.Errorf("read catalog: %w", err)
	}
	if int64(len(data)) > l.maxResponseBytes {
		return Catalog{}, fmt.Errorf("read catalog: response exceeds %d bytes", l.maxResponseBytes)
	}
	return Decode(data)
}

func (s Snapshot) Find(id string) (Item, bool) {
	id = strings.TrimSpace(id)
	for _, item := range s.Catalog.Items {
		if item.ID == id {
			return item, true
		}
	}
	return Item{}, false
}
