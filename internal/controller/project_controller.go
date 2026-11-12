package controller

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"observability-platform/internal/dtos"
	"observability-platform/internal/middleware"
	"observability-platform/internal/service"
	"observability-platform/internal/utils"
)

type ProjectController struct {
	service *service.ProjectService
	logger  *slog.Logger
}

func NewProjectController(projectService *service.ProjectService, logger *slog.Logger) *ProjectController {
	return &ProjectController{service: projectService, logger: logger}
}

func (c *ProjectController) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/accounts/{accountID}/projects", c.list)
	mux.HandleFunc("POST /v1/accounts/{accountID}/projects", c.create)
}

func (c *ProjectController) list(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		utils.WriteError(w, utils.NewError(http.StatusUnauthorized, "unauthorized", "Valid user bearer token required"))
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			utils.WriteErrorResponse(w, http.StatusBadRequest, "validation_error", "limit must be between 1 and 500")
			return
		}
		limit = parsed
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := c.service.ListByAccount(ctx, r.PathValue("accountID"), user.ID, limit)
	if err != nil {
		if status, _, _ := utils.ErrorResponse(err); status >= 500 {
			c.logger.Error("project listing failed", "error", err)
		}
		utils.WriteError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, response)
}

func (c *ProjectController) create(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		utils.WriteError(w, utils.NewError(http.StatusUnauthorized, "unauthorized", "Valid user bearer token required"))
		return
	}
	var request dtos.CreateProjectRequest
	if !decodeRequest(w, r, &request, 16*1024) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	project, err := c.service.Create(ctx, r.PathValue("accountID"), user.ID, request)
	if err != nil {
		if status, _, _ := utils.ErrorResponse(err); status >= 500 {
			c.logger.Error("project creation failed", "error", err)
		}
		utils.WriteError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusCreated, project)
}
