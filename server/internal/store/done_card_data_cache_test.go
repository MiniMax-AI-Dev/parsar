package store

import (
	"testing"
)

// TestCacheTokensFromRaw mirrors the shapes the daemon writes (see
// apps/parsar-daemon/.../claudecode/parser.go — cache_* keys only stamped
// when at least one is non-zero) plus defensive fallbacks.
func TestCacheTokensFromRaw(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		raw             []byte
		wantCacheRead   int
		wantCacheCreate int
	}{
		{
			name:            "nil_raw_returns_zero",
			raw:             nil,
			wantCacheRead:   0,
			wantCacheCreate: 0,
		},
		{
			name:            "empty_bytes_returns_zero",
			raw:             []byte{},
			wantCacheRead:   0,
			wantCacheCreate: 0,
		},
		{
			name:            "empty_object_returns_zero",
			raw:             []byte(`{}`),
			wantCacheRead:   0,
			wantCacheCreate: 0,
		},
		{
			name:            "daemon_wire_shape_both_present",
			raw:             []byte(`{"cache_creation_input_tokens":1200,"cache_read_input_tokens":30000}`),
			wantCacheRead:   30000,
			wantCacheCreate: 1200,
		},
		{
			name:            "only_cache_read",
			raw:             []byte(`{"cache_read_input_tokens":15000}`),
			wantCacheRead:   15000,
			wantCacheCreate: 0,
		},
		{
			name:            "only_cache_creation",
			raw:             []byte(`{"cache_creation_input_tokens":8000}`),
			wantCacheRead:   0,
			wantCacheCreate: 8000,
		},
		{
			// json.Unmarshal yields float64 for numbers; a string value
			// degrades to 0 rather than crashing.
			name:            "string_value_degrades_to_zero",
			raw:             []byte(`{"cache_read_input_tokens":"30000"}`),
			wantCacheRead:   0,
			wantCacheCreate: 0,
		},
		{
			name:            "malformed_json_does_not_panic",
			raw:             []byte(`{not json`),
			wantCacheRead:   0,
			wantCacheCreate: 0,
		},
		{
			name:            "negative_clamped_to_zero",
			raw:             []byte(`{"cache_read_input_tokens":-5}`),
			wantCacheRead:   0,
			wantCacheCreate: 0,
		},
		{
			name:            "extra_unrelated_keys_ignored",
			raw:             []byte(`{"cache_read_input_tokens":1000,"foo":"bar","nested":{"x":1}}`),
			wantCacheRead:   1000,
			wantCacheCreate: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRead, gotCreate := cacheTokensFromRaw(tc.raw)
			if gotRead != tc.wantCacheRead {
				t.Errorf("cache_read = %d, want %d", gotRead, tc.wantCacheRead)
			}
			if gotCreate != tc.wantCacheCreate {
				t.Errorf("cache_creation = %d, want %d", gotCreate, tc.wantCacheCreate)
			}
		})
	}
}
