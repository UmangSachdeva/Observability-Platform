package observability

import (
	"fmt"
	"net/http"
	"runtime/debug"
)

// Middleware recovers panics from next, reports each one as a "fatal" event
// with its stack trace (linked to the request's trace when there is one), and
// answers 500 so the server keeps running.
func (c *Client) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			if recovered == http.ErrAbortHandler {
				panic(recovered) // net/http uses this panic to abort a response on purpose
			}
			c.ReportContext(r.Context(), Event{
				Level:      "fatal",
				Message:    fmt.Sprintf("panic: %v", recovered),
				StackTrace: string(debug.Stack()),
				Attributes: map[string]string{"method": r.Method, "path": r.URL.Path},
			})
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}
