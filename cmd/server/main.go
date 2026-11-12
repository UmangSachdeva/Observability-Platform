package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"observability-platform/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	config := server.Config{
		HTTPAddr:                env("HTTP_ADDR", ":8080"),
		MongoURI:                env("MONGODB_URI", "mongodb://localhost:27017"),
		MongoDatabase:           env("MONGODB_DATABASE", "observability"),
		MongoCollection:         env("MONGODB_COLLECTION", "errors"),
		MongoKeysCollection:     env("MONGODB_KEYS_COLLECTION", "api_keys"),
		MongoUsersCollection:    env("MONGODB_USERS_COLLECTION", "users"),
		MongoAccountsCollection: env("MONGODB_ACCOUNTS_COLLECTION", "accounts"),
		MongoInvitesCollection:  env("MONGODB_INVITES_COLLECTION", "account_invites"),
		MongoProjectsCollection: env("MONGODB_PROJECTS_COLLECTION", "projects"),
		APIKeyEncryptionKey:     os.Getenv("API_KEY_ENCRYPTION_KEY"),
		UserJWTSecret:           os.Getenv("USER_JWT_SECRET"),
	}

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	app, err := server.New(startupCtx, config, logger)
	startupCancel()
	if err != nil {
		logger.Error("server initialization failed", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.HTTP.Shutdown(shutdownCtx); err != nil {
			logger.Error("HTTP shutdown failed", "error", err)
		}
	}()

	logger.Info("server starting", "addr", config.HTTPAddr)
	if err := app.HTTP.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server failed", "error", err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.Close(closeCtx); err != nil {
		logger.Error("application shutdown failed", "error", err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
