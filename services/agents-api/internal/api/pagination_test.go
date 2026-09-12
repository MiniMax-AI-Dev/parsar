package api

import (
	"net/http/httptest"
	"testing"
)

func TestPageSizePolicy(t *testing.T) {
	for _, test := range []struct {
		query              string
		cappedOK, strictOK bool
		limit              int
	}{
		{"", true, true, 20}, {"limit=1", true, true, 1}, {"limit=100", true, true, 100},
		{"limit=101", true, false, 100}, {"limit=9223372036854775807", true, false, 100},
		{"limit=9223372036854775808", false, false, 0}, {"limit=0", false, false, 0},
		{"limit=-1", false, false, 0}, {"limit=1.5", false, false, 0}, {"limit=", false, false, 0},
		{"limit=null", false, false, 0}, {"limit=2&limit=3", false, false, 0}, {"order=invalid", false, false, 0},
		{"tenant_id=other", false, false, 0},
	} {
		t.Run(test.query, func(t *testing.T) {
			for _, strict := range []bool{true, false} {
				w := httptest.NewRecorder()
				r := httptest.NewRequest("GET", "/v1/agents?"+test.query, nil)
				page, ok := readPageSize(w, r, strict)
				want := test.cappedOK
				if strict {
					want = test.strictOK
				}
				if ok != want || (ok && page.limit != test.limit) || (!ok && w.Code != 400) {
					t.Fatalf("strict=%t page=%+v ok=%t status=%d", strict, page, ok, w.Code)
				}
			}
		})
	}
}
