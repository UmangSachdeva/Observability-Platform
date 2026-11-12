package controller

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"observability-platform/internal/dtos"
	"observability-platform/internal/middleware"
	"observability-platform/internal/service"
	"observability-platform/internal/utils"
)

const maxBodyBytes = 1 << 20

type ErrorController struct {
	service *service.ErrorService
	logger  *slog.Logger
}

func NewErrorController(errorService *service.ErrorService, logger *slog.Logger) *ErrorController {
	return &ErrorController{service: errorService, logger: logger}
}

func (c *ErrorController) RegisterPublicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", c.health)
}

// RegisterProtectedRoutes registers routes that need a signed API-key request.
func (c *ErrorController) RegisterProtectedRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/errors", c.create)
}

// RegisterUserRoutes registers routes that need a user bearer token.
func (c *ErrorController) RegisterUserRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/accounts/{accountID}/projects/{projectID}/errors", c.list)
	mux.HandleFunc("GET /v1/accounts/{accountID}/errors", c.searchAccount)
}

func (c *ErrorController) health(w http.ResponseWriter, _ *http.Request) {
	utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (c *ErrorController) create(w http.ResponseWriter, r *http.Request) {
	client, ok := middleware.ClientFromContext(r.Context())
	if !ok {
		utils.WriteError(w, service.ErrInvalidSignature)
		return
	}
	var request dtos.CreateErrorRequest
	if !decodeRequest(w, r, &request, maxBodyBytes) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	event, err := c.service.Create(ctx, client.AccountID, client.ProjectID, request)
	if err != nil {
		if status, _, _ := utils.ErrorResponse(err); status >= 500 {
			c.logger.Error("could not store error event", "error", err)
		}
		utils.WriteError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusAccepted, dtos.CreateErrorResponse{ID: event.ID, Status: "accepted"})
}

func (c *ErrorController) list(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		utils.WriteError(w, utils.NewError(http.StatusUnauthorized, "unauthorized", "Valid user bearer token required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := c.service.SearchProject(ctx, r.PathValue("accountID"), r.PathValue("projectID"), user.ID, searchRequestFromQuery(r.URL.Query()))
	c.writeSearch(w, response, err)
}

func (c *ErrorController) searchAccount(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		utils.WriteError(w, utils.NewError(http.StatusUnauthorized, "unauthorized", "Valid user bearer token required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := c.service.SearchAccount(ctx, r.PathValue("accountID"), user.ID, searchRequestFromQuery(r.URL.Query()))
	c.writeSearch(w, response, err)
}

func (c *ErrorController) writeSearch(w http.ResponseWriter, response dtos.ListErrorsResponse, err error) {
	if err != nil {
		if status, _, _ := utils.ErrorResponse(err); status >= 500 {
			c.logger.Error("could not search error events", "error", err)
		}
		utils.WriteError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, response)
}

// searchRequestFromQuery reads search parameters. List filters accept repeated
// parameters (?level=error&level=warning) and comma-separated values
// (?level=error,warning). Attribute filters use attr.<key>=<value>.
func searchRequestFromQuery(values url.Values) dtos.SearchErrorsRequest {
	request := dtos.SearchErrorsRequest{
		Query:        values.Get("q"),
		ProjectIDs:   splitValues(values["project_id"]),
		Services:     splitValues(values["service"]),
		Environments: splitValues(values["environment"]),
		Levels:       splitValues(values["level"]),
		From:         values.Get("from"),
		To:           values.Get("to"),
		Sort:         values.Get("sort"),
		Limit:        values.Get("limit"),
		Cursor:       values.Get("cursor"),
	}
	for key, vals := range values {
		if name, ok := strings.CutPrefix(key, "attr."); ok && len(vals) > 0 {
			if request.Attributes == nil {
				request.Attributes = map[string]string{}
			}
			request.Attributes[name] = vals[len(vals)-1]
		}
	}
	return request
}

func splitValues(values []string) []string {
	var split []string
	for _, value := range values {
		split = append(split, strings.Split(value, ",")...)
	}
	return split
}

func RequestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}
