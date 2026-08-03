package skillcatalog

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	idPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func (c Catalog) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("skill catalog schema_version %d is unsupported", c.SchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(c.UpdatedAt)); err != nil {
		return fmt.Errorf("skill catalog updated_at must be RFC3339: %w", err)
	}
	seen := make(map[string]struct{}, len(c.Items))
	for index, item := range c.Items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("skill catalog item[%d]: %w", index, err)
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return fmt.Errorf("skill catalog item id %q is duplicated", item.ID)
		}
		seen[item.ID] = struct{}{}
	}
	return nil
}

func (i Item) Validate() error {
	if !idPattern.MatchString(i.ID) {
		return fmt.Errorf("id %q is invalid", i.ID)
	}
	if strings.TrimSpace(i.Name) == "" || strings.TrimSpace(i.Description) == "" {
		return fmt.Errorf("item %q name and description are required", i.ID)
	}
	if strings.TrimSpace(i.Publisher.Name) == "" {
		return fmt.Errorf("item %q publisher name is required", i.ID)
	}
	for label, value := range map[string]string{
		"publisher.url":  i.Publisher.URL,
		"icon_url":       i.IconURL,
		"homepage_url":   i.HomepageURL,
		"repository_url": i.RepositoryURL,
	} {
		if err := validateHTTPSURL(label, value, label == "publisher.url"); err != nil {
			return fmt.Errorf("item %q: %w", i.ID, err)
		}
	}
	if i.FeaturedRank < 1 {
		return fmt.Errorf("item %q featured_rank must be positive", i.ID)
	}
	if strings.TrimSpace(i.Version) == "" || strings.TrimSpace(i.License) == "" {
		return fmt.Errorf("item %q version and license are required", i.ID)
	}
	if !shaPattern.MatchString(i.SourceRef) {
		return fmt.Errorf("item %q source_ref must be a full lowercase commit SHA", i.ID)
	}
	if strings.TrimSpace(i.SourcePath) == "" || strings.HasPrefix(i.SourcePath, "/") || strings.Contains(i.SourcePath, "..") {
		return fmt.Errorf("item %q source_path is invalid", i.ID)
	}
	if i.ContentPath != "items/"+i.ID {
		return fmt.Errorf("item %q content_path must be items/%s", i.ID, i.ID)
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
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("%s must be an https URL without embedded credentials", label)
	}
	return nil
}
