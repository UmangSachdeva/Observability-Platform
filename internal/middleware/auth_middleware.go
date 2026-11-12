package middleware

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"observability-platform/internal/service"
	"observability-platform/internal/utils"
)

const maxSignedBodyBytes = 1 << 20

type clientContextKey struct{}

// ClientFromContext returns the project identity of the API key that signed
// the request.
func ClientFromContext(ctx context.Context) (service.ClientIdentity, bool) {
	client, ok := ctx.Value(clientContextKey{}).(service.ClientIdentity)
	return client, ok
}

func SignatureAuthMiddleware(authService *service.AuthService, next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSignedBodyBytes))
		if err != nil {
			utils.WriteErrorResponse(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request body exceeds the 1 MiB limit")
			return
		}
		authCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		client, err := authService.Authenticate(authCtx,
			r.Header.Get("X-Access-Key"),
			r.Header.Get("X-Timestamp"),
			r.Method,
			r.URL.RequestURI(),
			body,
			r.Header.Get("X-Signature"),
		)
		if err != nil {
			if status, _, _ := utils.ErrorResponse(err); status >= 500 {
				logger.Error("could not verify request signature", "error", err)
			}
			utils.WriteError(w, err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		ctx := context.WithValue(r.Context(), clientContextKey{}, client)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
