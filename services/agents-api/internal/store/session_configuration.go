package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func canonicalConfiguration(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return nil, fmt.Errorf("%w: configuration must be a JSON object", ErrInvalidInput)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("%w: configuration must contain exactly one object", ErrInvalidInput)
	}
	canonical, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid configuration", ErrInvalidInput)
	}
	return canonical, nil
}
