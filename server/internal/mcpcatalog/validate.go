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
	idPattern         = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	credentialPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
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
	if strings.TrimSpace(i.Name) == "" || strings.TrimSpace(i.Description) == "" {
		return fmt.Errorf("item %q name and description are required", i.ID)
	}
	if strings.TrimSpace(i.Publisher.Name) == "" {
		return fmt.Errorf("item %q publisher name is required", i.ID)
	}
	for label, value := range map[string]string{
		"publisher.url": i.Publisher.URL,
		"server.url":    i.Server.URL,
	} {
		if err := validateHTTPSURL(label, value, true); err != nil {
			return fmt.Errorf("item %q: %w", i.ID, err)
		}
	}
	for label, value := range map[string]string{
		"icon_url":       i.IconURL,
		"homepage_url":   i.HomepageURL,
		"repository_url": i.RepositoryURL,
	} {
		if err := validateHTTPSURL(label, value, false); err != nil {
			return fmt.Errorf("item %q: %w", i.ID, err)
		}
	}
	if i.FeaturedRank < 1 {
		return fmt.Errorf("item %q featured_rank must be positive", i.ID)
	}
	if strings.TrimSpace(i.Version) == "" {
		return fmt.Errorf("item %q version is required", i.ID)
	}
	if i.Transport != canonical.MCPTransportStreamableHTTP {
		return fmt.Errorf("item %q transport %q is unsupported", i.ID, i.Transport)
	}
	switch i.Authentication.EffectiveType() {
	case "none":
		if strings.TrimSpace(i.Authentication.CredentialKind) != "" {
			return fmt.Errorf("item %q authentication credential_kind requires oauth2", i.ID)
		}
	case "oauth2":
		if !credentialPattern.MatchString(i.Authentication.CredentialKind) {
			return fmt.Errorf("item %q authentication credential_kind %q is invalid", i.ID, i.Authentication.CredentialKind)
		}
	default:
		return fmt.Errorf("item %q authentication type %q is unsupported", i.ID, i.Authentication.Type)
	}
	if strings.TrimSpace(i.Server.Name) == "" {
		return fmt.Errorf("item %q server name is required", i.ID)
	}
	seenCategories := make(map[string]struct{}, len(i.Categories))
	for _, category := range i.Categories {
		category = strings.TrimSpace(category)
		if category == "" {
			return fmt.Errorf("item %q has an empty category", i.ID)
		}
		if _, duplicate := seenCategories[category]; duplicate {
			return fmt.Errorf("item %q category %q is duplicated", i.ID, category)
		}
		seenCategories[category] = struct{}{}
	}
	return nil
}

func validateHTTPSURL(label, value string, required bool) error {
	value = strings.TrimSpace(value)
	if value == "" {
		if required {
			return fmt.Errorf("%s is required", label)
		}
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" || parsed.User != nil {
		return fmt.Errorf("%s must be an https URL without embedded credentials", label)
	}
	return nil
}
