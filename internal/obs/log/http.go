package log

import (
	"net/http"
)

// HTTPMiddleware adopts a valid inbound `traceparent`, mints a fresh
// Carrier otherwise, echoes it on the response, and installs it on
// ctx so downstream `log.Ctx(ctx).Info(...)` gets trace attrs. A
// malformed header is treated identically to a missing one — never
// 500 a real request over a logging concern.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		carrier, ok := parseInbound(r.Header.Get(HeaderName))
		if !ok {
			carrier = NewCarrier()
		}
		w.Header().Set(HeaderName, carrier.String())
		ctx := WithTrace(r.Context(), carrier)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// parseInbound returns (carrier, true) only when the header parses as
// a valid W3C traceparent. Missing and malformed both yield false.
func parseInbound(headerValue string) (Carrier, bool) {
	if headerValue == "" {
		return Carrier{}, false
	}
	c, err := ParseTraceparent(headerValue)
	if err != nil {
		return Carrier{}, false
	}
	return c, true
}
