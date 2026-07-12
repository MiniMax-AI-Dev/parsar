package store

import "encoding/json"

func decodeJSONBValue[T any](raw any) T {
	var value T
	switch raw := raw.(type) {
	case nil:
		return value
	case []byte:
		_ = json.Unmarshal(raw, &value)
	case string:
		_ = json.Unmarshal([]byte(raw), &value)
	default:
		if data, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(data, &value)
		}
	}
	return value
}
