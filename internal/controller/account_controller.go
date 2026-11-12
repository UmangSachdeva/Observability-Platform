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

type AccountController struct {
	service *service.AccountService
	logger  *slog.Logger
	limiter *utils.KeyIssueLimiter
}

func NewAccountController(accountService *service.AccountService, logger *slog.Logger) *AccountController {
	return &AccountController{service: accountService, logger: logger, limiter: utils.NewKeyIssueLimiter(20, time.Minute)}
}

func (c *AccountController) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/accounts/{accountID}", c.getAccount)
	mux.HandleFunc("POST /v1/accounts/{accountID}/invites", c.createInvite)
	mux.HandleFunc("POST /v1/invites/accept", c.acceptInvite)
	mux.HandleFunc("GET /v1/invites", c.listPendingInvites)
	mux.HandleFunc("PATCH /v1/invites/{inviteID}", c.snoozeInvite)
	mux.HandleFunc("DELETE /v1/accounts/{accountID}/members/me", c.leaveAccount)
}

func (c *AccountController) leaveAccount(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		utils.WriteError(w, utils.NewError(http.StatusUnauthorized, "unauthorized", "Valid user bearer token required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := c.service.LeaveAccount(ctx, r.PathValue("accountID"), user.ID)
	if err != nil {
		if status, _, _ := utils.ErrorResponse(err); status >= 500 {
			c.logger.Error("leaving account failed", "error", err)
		}
		utils.WriteError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, response)
}

func (c *AccountController) getAccount(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		utils.WriteError(w, utils.NewError(http.StatusUnauthorized, "unauthorized", "Valid user bearer token required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	account, err := c.service.GetAccount(ctx, r.PathValue("accountID"), user.ID)
	if err != nil {
		if status, _, _ := utils.ErrorResponse(err); status >= 500 {
			c.logger.Error("account lookup failed", "error", err)
		}
		utils.WriteError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, account)
}

func (c *AccountController) createInvite(w http.ResponseWriter, r *http.Request) {
	if !c.limiter.Allow(clientIP(r)) {
		utils.WriteErrorResponse(w, http.StatusTooManyRequests, "rate_limited", "Too many invite requests; try again later")
		return
	}
	var request dtos.CreateInviteRequest
	if !decodeRequest(w, r, &request, 16*1024) {
		return
	}
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		utils.WriteError(w, utils.NewError(http.StatusUnauthorized, "unauthorized", "Valid user bearer token required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := c.service.CreateInvite(ctx, r.PathValue("accountID"), user.ID, request)
	if err != nil {
		if status, _, _ := utils.ErrorResponse(err); status >= 500 {
			c.logger.Error("invite creation failed", "error", err)
		}
		utils.WriteError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	utils.WriteJSON(w, http.StatusCreated, response)
}

func (c *AccountController) acceptInvite(w http.ResponseWriter, r *http.Request) {
	if !c.limiter.Allow(clientIP(r)) {
		utils.WriteErrorResponse(w, http.StatusTooManyRequests, "rate_limited", "Too many invite requests; try again later")
		return
	}
	var request dtos.AcceptInviteRequest
	if !decodeRequest(w, r, &request, 16*1024) {
		return
	}
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		utils.WriteError(w, utils.NewError(http.StatusUnauthorized, "unauthorized", "Valid user bearer token required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := c.service.AcceptInvite(ctx, user.ID, user.Email, request)
	if err != nil {
		if status, _, _ := utils.ErrorResponse(err); status >= 500 {
			c.logger.Error("invite acceptance failed", "error", err)
		}
		utils.WriteError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, response)
}

func (c *AccountController) listPendingInvites(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		utils.WriteError(w, utils.NewError(http.StatusUnauthorized, "unauthorized", "Valid user bearer token required"))
		return
	}
	includeSnoozed := false
	if raw := r.URL.Query().Get("include_snoozed"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			utils.WriteErrorResponse(w, http.StatusBadRequest, "validation_error", "include_snoozed must be true or false")
			return
		}
		includeSnoozed = parsed
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := c.service.ListPendingInvites(ctx, user.Email, includeSnoozed)
	if err != nil {
		if status, _, _ := utils.ErrorResponse(err); status >= 500 {
			c.logger.Error("pending invite listing failed", "error", err)
		}
		utils.WriteError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	utils.WriteJSON(w, http.StatusOK, response)
}

func (c *AccountController) snoozeInvite(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		utils.WriteError(w, utils.NewError(http.StatusUnauthorized, "unauthorized", "Valid user bearer token required"))
		return
	}
	var request dtos.SnoozeInviteRequest
	if !decodeRequest(w, r, &request, 16*1024) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := c.service.SnoozeInvite(ctx, r.PathValue("inviteID"), user.Email, request)
	if err != nil {
		if status, _, _ := utils.ErrorResponse(err); status >= 500 {
			c.logger.Error("invite snooze failed", "error", err)
		}
		utils.WriteError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	utils.WriteJSON(w, http.StatusOK, response)
}
