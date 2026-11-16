package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"observability-platform/internal/controller"
	"observability-platform/internal/middleware"
	"observability-platform/internal/repository/interfaces"
	"observability-platform/internal/repository/mongo"
	"observability-platform/internal/service"
	"observability-platform/internal/utils"
	"observability-platform/pkg/observability"
)

type Config struct {
	HTTPAddr                   string
	MongoURI                   string
	MongoDatabase              string
	MongoCollection            string
	MongoKeysCollection        string
	MongoUsersCollection       string
	MongoAccountsCollection    string
	MongoInvitesCollection     string
	MongoProjectsCollection    string
	MongoSpansCollection       string
	MongoOccurrencesCollection string
	MongoTracesCollection      string
	APIKeyEncryptionKey        string
	UserJWTSecret              string
}

type Server struct {
	HTTP        *http.Server
	mongoClient interfaces.MongoLayer
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Server, error) {
	mongoClient, err := mongo.Connect(ctx, config.MongoURI, config.MongoDatabase)
	if err != nil {
		return nil, err
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoKeysCollection, bson.D{{Key: "access_key", Value: 1}}, true); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoKeysCollection, bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}}, false); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoCollection, bson.D{{Key: "received_at", Value: -1}}, false); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoUsersCollection, bson.D{{Key: "username", Value: 1}}, true); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoUsersCollection, bson.D{{Key: "email", Value: 1}}, true); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoAccountsCollection, bson.D{{Key: "id", Value: 1}}, true); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoAccountsCollection, bson.D{{Key: "members.user_id", Value: 1}}, false); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoInvitesCollection, bson.D{{Key: "token_hash", Value: 1}}, true); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoInvitesCollection, bson.D{{Key: "id", Value: 1}}, true); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoInvitesCollection, bson.D{{Key: "email", Value: 1}, {Key: "created_at", Value: -1}}, false); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoCollection, bson.D{{Key: "project_id", Value: 1}, {Key: "received_at", Value: -1}}, false); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoCollection, bson.D{{Key: "account_id", Value: 1}, {Key: "received_at", Value: -1}}, false); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoCollection, bson.D{{Key: "id", Value: 1}}, false); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoCollection, bson.D{{Key: "project_id", Value: 1}, {Key: "timestamp", Value: -1}}, false); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	traceIndexes := []struct {
		collection string
		keys       bson.D
		unique     bool
	}{
		{config.MongoCollection, bson.D{{Key: "project_id", Value: 1}, {Key: "trace_id", Value: 1}}, false},
		// One record per distinct error; the id comes from its fingerprint.
		{config.MongoCollection, bson.D{{Key: "project_id", Value: 1}, {Key: "id", Value: 1}}, true},
		// One row per delivery of an occurrence: retries and replays collide.
		{config.MongoOccurrencesCollection, bson.D{{Key: "project_id", Value: 1}, {Key: "error_id", Value: 1}, {Key: "key", Value: 1}}, true},
		{config.MongoOccurrencesCollection, bson.D{{Key: "project_id", Value: 1}, {Key: "error_id", Value: 1}, {Key: "received_at", Value: -1}, {Key: "id", Value: -1}}, false},
		{config.MongoOccurrencesCollection, bson.D{{Key: "account_id", Value: 1}, {Key: "trace_id", Value: 1}}, false},
		{config.MongoSpansCollection, bson.D{{Key: "project_id", Value: 1}, {Key: "trace_id", Value: 1}, {Key: "span_id", Value: 1}}, true},
		{config.MongoTracesCollection, bson.D{{Key: "project_id", Value: 1}, {Key: "trace_id", Value: 1}}, true},
		{config.MongoTracesCollection, bson.D{{Key: "project_id", Value: 1}, {Key: "start_time", Value: -1}, {Key: "trace_id", Value: -1}}, false},
	}
	for _, index := range traceIndexes {
		if err := mongoClient.CreateIndex(ctx, index.collection, index.keys, index.unique); err != nil {
			_ = mongoClient.Close(context.Background())
			return nil, err
		}
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoProjectsCollection, bson.D{{Key: "id", Value: 1}}, true); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	if err := mongoClient.CreateIndex(ctx, config.MongoProjectsCollection, bson.D{{Key: "account_id", Value: 1}, {Key: "name", Value: 1}}, true); err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	errorRepository := mongo.NewErrorRepository(mongoClient, config.MongoCollection, config.MongoOccurrencesCollection)
	apiKeyRepository := mongo.NewAPIKeyRepository(mongoClient, config.MongoKeysCollection)
	userRepository := mongo.NewUserRepository(mongoClient, config.MongoUsersCollection)
	accountRepository := mongo.NewAccountRepository(mongoClient, config.MongoAccountsCollection)
	inviteRepository := mongo.NewInviteRepository(mongoClient, config.MongoInvitesCollection)
	projectRepository := mongo.NewProjectRepository(mongoClient, config.MongoProjectsCollection)
	authService, err := service.NewAuthService(apiKeyRepository, accountRepository, projectRepository, config.APIKeyEncryptionKey)
	if err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	userService, err := service.NewUserService(userRepository, accountRepository, config.UserJWTSecret)
	if err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, err
	}
	inviteTokens, err := utils.NewSecretCipher(config.APIKeyEncryptionKey)
	if err != nil {
		_ = mongoClient.Close(context.Background())
		return nil, fmt.Errorf("API_KEY_ENCRYPTION_KEY %w", err)
	}
	accountService := service.NewAccountService(accountRepository, inviteRepository, inviteTokens)
	projectService := service.NewProjectService(projectRepository, accountRepository)
	errorService := service.NewErrorService(errorRepository, accountRepository, projectRepository)
	spanRepository := mongo.NewSpanRepository(mongoClient, config.MongoSpansCollection, config.MongoTracesCollection)
	traceService := service.NewTraceService(spanRepository, errorRepository, accountRepository, projectRepository)
	traceController := controller.NewTraceController(traceService, logger)
	errorController := controller.NewErrorController(errorService, logger)
	authController := controller.NewAuthController(authService, logger)
	userAuthController := controller.NewUserAuthController(userService, logger)
	accountController := controller.NewAccountController(accountService, logger)
	projectController := controller.NewProjectController(projectService, logger)

	mux := http.NewServeMux()

	errorController.RegisterPublicRoutes(mux)
	userAuthController.RegisterRoutes(mux)
	userProtectedRoutes := http.NewServeMux()
	authController.RegisterRoutes(userProtectedRoutes)
	accountController.RegisterRoutes(userProtectedRoutes)
	projectController.RegisterRoutes(userProtectedRoutes)
	errorController.RegisterUserRoutes(userProtectedRoutes)
	traceController.RegisterUserRoutes(userProtectedRoutes)
	userAuth := middleware.UserAuthMiddleware(userService, observability.RouteNamer(userProtectedRoutes), logger)
	mux.Handle("/v1/auth/keys", userAuth)
	mux.Handle("/v1/accounts/", userAuth)
	mux.Handle("/v1/invites", userAuth)
	mux.Handle("/v1/invites/", userAuth)

	protectedRoutes := http.NewServeMux()

	errorController.RegisterProtectedRoutes(protectedRoutes)
	mux.Handle("/v1/errors", middleware.SignatureAuthMiddleware(authService, observability.RouteNamer(protectedRoutes), logger))

	// OTLP receiver: API key headers instead of HMAC, because standard OTLP
	// exporters can only send fixed headers.
	otlpRoutes := http.NewServeMux()
	traceController.RegisterIngestRoutes(otlpRoutes)
	mux.Handle("/v1/traces", middleware.APIKeyAuthMiddleware(authService, otlpRoutes, logger))

	// Every request gets a SERVER span named after its route. OTLP ingestion
	// and health checks are not traced: tracing /v1/traces would make the API
	// create a span for each batch of its own spans it exports, forever.
	handler := otelhttp.NewHandler(controller.RequestLogger(logger, observability.RouteNamer(mux)), "http.server",
		otelhttp.WithSpanNameFormatter(observability.SpanNameFromRoute),
		otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/v1/traces" && r.URL.Path != "/healthz" }))

	return &Server{
		HTTP: &http.Server{
			Addr:              config.HTTPAddr,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      15 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
		mongoClient: mongoClient,
	}, nil
}

func (s *Server) Close(ctx context.Context) error {
	return s.mongoClient.Close(ctx)
}
