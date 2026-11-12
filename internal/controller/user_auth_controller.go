package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"observability-platform/internal/dtos"
	"observability-platform/internal/service"
	"observability-platform/internal/utils"
)

type UserAuthController struct {
	service *service.UserService
	logger  *slog.Logger
	limiter *utils.KeyIssueLimiter
}

func NewUserAuthController(userService *service.UserService, logger *slog.Logger) *UserAuthController {
	return &UserAuthController{service: userService, logger: logger, limiter: utils.NewKeyIssueLimiter(10, time.Minute)}
}

func (c *UserAuthController) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/signup", c.signup)
	mux.HandleFunc("POST /v1/auth/login", c.login)
}

func (c *UserAuthController) signup(w http.ResponseWriter, r *http.Request) {
	if !c.limiter.Allow(clientIP(r)) {
		utils.WriteErrorResponse(w, http.StatusTooManyRequests, "rate_limited", "Too many authentication requests; try again later")
		return
	}
	var request dtos.SignupRequest
	if !decodeRequest(w, r, &request, 16*1024) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := c.service.Signup(ctx, request)
	if err != nil {
		if status, _, _ := utils.ErrorResponse(err); status >= 500 {
			c.logger.Error("signup failed", "error", err)
		}
		utils.WriteError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusCreated, response)
}

func (c *UserAuthController) login(w http.ResponseWriter, r *http.Request) {
	if !c.limiter.Allow(clientIP(r)) {
		utils.WriteErrorResponse(w, http.StatusTooManyRequests, "rate_limited", "Too many authentication requests; try again later")
		return
	}
	var request dtos.LoginRequest
	if !decodeRequest(w, r, &request, 16*1024) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := c.service.Login(ctx, request)
	if err != nil {
		if status, _, _ := utils.ErrorResponse(err); status >= 500 {
			c.logger.Error("login failed", "error", err)
		}
		utils.WriteError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	utils.WriteJSON(w, http.StatusOK, response)
}

func decodeRequest(w http.ResponseWriter, r *http.Request, destination any, maxBytes int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(destination); err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "invalid_json", "Request body must be a valid JSON object")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "invalid_json", "Request body must contain one JSON object")
		return false
	}
	return true
}
