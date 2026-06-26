package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// mergeYAML decodes YAML over an existing Config. Fields present
// (even with the zero value) overwrite. KnownFields(true) rejects
// unknown keys so a `serer:` typo surfaces as a Load error rather
// than silently dropping config.
func mergeYAML(cfg *Config, data []byte) error {
	if cfg == nil {
		return fmt.Errorf("nil config receiver")
	}
	if len(data) == 0 {
		return nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}
