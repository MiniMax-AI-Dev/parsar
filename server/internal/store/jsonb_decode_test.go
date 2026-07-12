package store

import (
	"reflect"
	"testing"
)

func TestDecodeJSONBValue(t *testing.T) {
	type decodedValue struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	tests := []struct {
		name string
		raw  any
		want decodedValue
	}{
		{name: "nil", raw: nil},
		{name: "bytes", raw: []byte(`{"name":"bytes","count":1}`), want: decodedValue{Name: "bytes", Count: 1}},
		{name: "string", raw: `{"name":"string","count":2}`, want: decodedValue{Name: "string", Count: 2}},
		{name: "map", raw: map[string]any{"name": "map", "count": 3}, want: decodedValue{Name: "map", Count: 3}},
		{name: "malformed JSON", raw: []byte(`{"name":`)},
		{name: "marshal failure", raw: make(chan int)},
		{
			name: "partial decode",
			raw:  []byte(`{"name":"preserved","count":"invalid"}`),
			want: decodedValue{Name: "preserved"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := decodeJSONBValue[decodedValue](test.raw); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("decodeJSONBValue() = %#v, want %#v", got, test.want)
			}
		})
	}
}
