package controller

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"observability-platform/internal/dtos"
	"observability-platform/internal/middleware"
	"observability-platform/internal/service"
	"observability-platform/internal/utils"
)

type AuthController struct {
	service *service.AuthService
	logger  *slog.Logger
	limiter *utils.KeyIssueLimiter
}

func NewAuthController(authService *service.AuthService, logger *slog.Logger) *AuthController {
	return &AuthController{service: authService, logger: logger, limiter: utils.NewKeyIssueLimiter(5, time.Minute)}
}

func (c *AuthController) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/accounts/{accountID}/projects/{projectID}/keys", c.createKey)
	mux.HandleFunc("POST /v1/auth/keys", c.keyEndpointMoved)
	mux.HandleFunc("GET /v1/auth/keys", c.listKeys)
}

func (c *AuthController) listKeys(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		utils.WriteError(w, utils.NewError(http.StatusUnauthorized, "unauthorized", "Valid user bearer token required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := c.service.ListAPIKeys(ctx, user.ID)
	if err != nil {
		c.logger.Error("could not list API keys", "error", err)
		utils.WriteError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	utils.WriteJSON(w, http.StatusOK, response)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// keyEndpointMoved answers the old unscoped key endpoint. Keys now belong to
// a project so that their events do too.
func (c *AuthController) keyEndpointMoved(w http.ResponseWriter, _ *http.Request) {
	utils.WriteErrorResponse(w, http.StatusGone, "endpoint_moved",
		"Create API keys under a project with POST /v1/accounts/{accountID}/projects/{projectID}/keys")
}

func (c *AuthController) createKey(w http.ResponseWriter, r *http.Request) {
	if !c.limiter.Allow(clientIP(r)) {
		utils.WriteErrorResponse(w, http.StatusTooManyRequests, "rate_limited", "Too many key requests; try again later")
		return
	}
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		utils.WriteError(w, utils.NewError(http.StatusUnauthorized, "unauthorized", "Valid user bearer token required"))
		return
	}
	var request dtos.CreateAPIKeyRequest
	if !decodeRequest(w, r, &request, 16*1024) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	credentials, err := c.service.CreateProjectAPIKey(ctx, r.PathValue("accountID"), r.PathValue("projectID"), user.ID, request)
	if err != nil {
		if status, _, _ := utils.ErrorResponse(err); status >= 500 {
			c.logger.Error("could not create API key", "error", err)
		}
		utils.WriteError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	utils.WriteJSON(w, http.StatusCreated, credentials)
}
