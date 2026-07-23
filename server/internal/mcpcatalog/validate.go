package mcpcatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

var (
	idPattern              = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	envPattern             = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	credentialAssignmentRE = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|token|secret|password)\s*[:=]\s*\S+`)
	bearerValueRE          = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]{8,}`)
)

func Decode(data []byte) (Catalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var catalog Catalog
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode catalog: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Catalog{}, fmt.Errorf("decode catalog: trailing JSON data")
	}
	if err := catalog.Validate(); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func (c Catalog) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("catalog schema_version %d is unsupported", c.SchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(c.UpdatedAt)); err != nil {
		return fmt.Errorf("catalog updated_at must be RFC3339: %w", err)
	}
	seen := make(map[string]struct{}, len(c.Items))
	for index, item := range c.Items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("catalog item[%d]: %w", index, err)
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return fmt.Errorf("catalog item id %q is duplicated", item.ID)
		}
		seen[item.ID] = struct{}{}
	}
	return nil
}

func (i Item) Validate() error {
	if !idPattern.MatchString(i.ID) {
		return fmt.Errorf("id %q must contain only lowercase letters, digits, dots, hyphens, or underscores", i.ID)
	}
	if strings.TrimSpace(i.Name) == "" {
		return fmt.Errorf("item %q name is required", i.ID)
	}
	if strings.TrimSpace(i.Description) == "" {
		return fmt.Errorf("item %q description is required", i.ID)
	}
	if strings.TrimSpace(i.Publisher.Name) == "" {
		return fmt.Errorf("item %q publisher name is required", i.ID)
	}
	if err := validateHTTPURL("publisher.url", i.Publisher.URL, true); err != nil {
		return fmt.Errorf("item %q: %w", i.ID, err)
	}
	for label, value := range map[string]string{
		"icon_url":       i.IconURL,
		"homepage_url":   i.HomepageURL,
		"repository_url": i.RepositoryURL,
	} {
		if err := validateHTTPURL(label, value, false); err != nil {
			return fmt.Errorf("item %q: %w", i.ID, err)
		}
	}
	if i.FeaturedRank < 1 {
		return fmt.Errorf("item %q featured_rank must be positive", i.ID)
	}
	if strings.TrimSpace(i.Version) == "" {
		return fmt.Errorf("item %q version is required", i.ID)
	}
	if i.Transport != canonical.MCPTransportStdio && i.Transport != canonical.MCPTransportStreamableHTTP {
		return fmt.Errorf("item %q transport %q is unsupported", i.ID, i.Transport)
	}
	switch i.Authentication.EffectiveType() {
	case "none":
		if strings.TrimSpace(i.Authentication.CredentialKind) != "" {
			return fmt.Errorf("item %q authentication credential_kind requires oauth2", i.ID)
		}
		if strings.TrimSpace(i.Authentication.ClientRegistration) != "" {
			return fmt.Errorf("item %q authentication client_registration requires oauth2", i.ID)
		}
	case "oauth2":
		if i.Transport != canonical.MCPTransportStreamableHTTP {
			return fmt.Errorf("item %q oauth2 authentication requires streamable-http", i.ID)
		}
		if !envPattern.MatchString(i.Authentication.CredentialKind) {
			return fmt.Errorf("item %q authentication credential_kind %q is invalid", i.ID, i.Authentication.CredentialKind)
		}
		switch i.Authentication.EffectiveClientRegistration() {
		case ClientRegistrationDynamic, ClientRegistrationApprovedClient:
		default:
			return fmt.Errorf("item %q authentication client_registration %q is unsupported", i.ID, i.Authentication.ClientRegistration)
		}
	default:
		return fmt.Errorf("item %q authentication type %q is unsupported", i.ID, i.Authentication.Type)
	}
	categorySeen := make(map[string]struct{}, len(i.Categories))
	for _, category := range i.Categories {
		category = strings.TrimSpace(category)
		if category == "" {
			return fmt.Errorf("item %q has an empty category", i.ID)
		}
		if _, duplicate := categorySeen[category]; duplicate {
			return fmt.Errorf("item %q category %q is duplicated", i.ID, category)
		}
		categorySeen[category] = struct{}{}
	}
	return i.Server.Validate(i.ID, i.Transport)
}

func (s Server) Validate(itemID, transport string) error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("item %q server name is required", itemID)
	}
	if s.StartupTimeoutSec < 0 || s.StartupTimeoutSec > 300 {
		return fmt.Errorf("item %q startup_timeout_sec must be between 0 and 300", itemID)
	}
	switch transport {
	case canonical.MCPTransportStdio:
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("item %q server command is required", itemID)
		}
		if strings.TrimSpace(s.URL) != "" {
			return fmt.Errorf("item %q stdio server must not set url", itemID)
		}
	case canonical.MCPTransportStreamableHTTP:
		if err := validateHTTPURL("server.url", s.URL, true); err != nil {
			return fmt.Errorf("item %q: %w", itemID, err)
		}
		parsed, _ := url.Parse(strings.TrimSpace(s.URL))
		if parsed.Scheme != "https" {
			return fmt.Errorf("item %q streamable-http server.url must use https", itemID)
		}
		if strings.TrimSpace(s.Command) != "" || len(s.Args) > 0 || len(s.Env) > 0 {
			return fmt.Errorf("item %q streamable-http server must not set command, args, or env", itemID)
		}
	default:
		return fmt.Errorf("item %q transport %q is unsupported", itemID, transport)
	}
	for _, arg := range s.Args {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("item %q contains a NUL byte in args", itemID)
		}
		if credentialAssignmentRE.MatchString(arg) || bearerValueRE.MatchString(arg) {
			return fmt.Errorf("item %q args appear to contain a credential value", itemID)
		}
		lower := strings.ToLower(strings.TrimSpace(arg))
		if strings.Contains(lower, "@latest") || strings.HasSuffix(lower, ":latest") {
			return fmt.Errorf("item %q uses an unpinned latest package", itemID)
		}
	}
	for name, value := range s.Env {
		if !envPattern.MatchString(name) {
			return fmt.Errorf("item %q env name %q is invalid", itemID, name)
		}
		if value != "" {
			return fmt.Errorf("item %q env %q must not contain a value", itemID, name)
		}
	}
	return nil
}

func validateHTTPURL(label, value string, required bool) error {
	value = strings.TrimSpace(value)
	if value == "" {
		if required {
			return fmt.Errorf("%s is required", label)
		}
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s must be an http or https URL", label)
	}
	if parsed.User != nil {
		return fmt.Errorf("%s must not contain embedded credentials", label)
	}
	for key, values := range parsed.Query() {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") {
			for _, value := range values {
				if value != "" {
					return fmt.Errorf("%s must not contain credential query parameters", label)
				}
			}
		}
	}
	return nil
}
