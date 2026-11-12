package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"observability-platform/internal/service"
	"observability-platform/internal/utils"
)

type userContextKey struct{}

func UserFromContext(ctx context.Context) (service.UserIdentity, bool) {
	user, ok := ctx.Value(userContextKey{}).(service.UserIdentity)
	return user, ok
}

func UserAuthMiddleware(userService *service.UserService, next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			utils.WriteError(w, service.ErrInvalidUserToken)
			return
		}
		user, err := userService.AuthenticateToken(parts[1])
		if err != nil {
			if err != service.ErrInvalidUserToken {
				logger.Error("user token validation failed", "error", err)
			}
			utils.WriteError(w, err)
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
