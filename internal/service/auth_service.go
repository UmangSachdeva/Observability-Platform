package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"observability-platform/internal/dtos"
	"observability-platform/internal/mapper"
	"observability-platform/internal/repository/interfaces"
	"observability-platform/internal/utils"
	"observability-platform/internal/validation"
)

const signatureMaxAge = 5 * time.Minute

var ErrInvalidSignature = utils.NewError(401, "unauthorized", "Valid request signature required")

type AuthService struct {
	keys     interfaces.APIKeyRepository
	accounts interfaces.AccountRepository
	projects interfaces.ProjectRepository
	secrets  *utils.SecretCipher
}

func NewAuthService(keys interfaces.APIKeyRepository, accounts interfaces.AccountRepository, projects interfaces.ProjectRepository, encodedEncryptionKey string) (*AuthService, error) {
	secrets, err := utils.NewSecretCipher(encodedEncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("API_KEY_ENCRYPTION_KEY %w", err)
	}
	return &AuthService{keys: keys, accounts: accounts, projects: projects, secrets: secrets}, nil
}

// CreateProjectAPIKey creates a key whose events are stored under projectID.
// Any member of the account that owns the project may create one.
func (s *AuthService) CreateProjectAPIKey(ctx context.Context, accountID, projectID, userID string, request dtos.CreateAPIKeyRequest) (dtos.CreateAPIKeyResponse, error) {
	request.ClientName = strings.TrimSpace(request.ClientName)
	if err := validation.ValidateCreateAPIKey(request); err != nil {
		return dtos.CreateAPIKeyResponse{}, err
	}
	if err := requireProjectAccess(ctx, s.accounts, s.projects, accountID, projectID, userID); err != nil {
		return dtos.CreateAPIKeyResponse{}, err
	}
	accessKey, err := randomToken("ak_", 24)
	if err != nil {
		return dtos.CreateAPIKeyResponse{}, utils.WrapError(500, "key_creation_failed", "Could not create API credentials", err)
	}
	secretKey, err := randomToken("", 32)
	if err != nil {
		return dtos.CreateAPIKeyResponse{}, utils.WrapError(500, "key_creation_failed", "Could not create API credentials", err)
	}
	encryptedSecret, err := s.secrets.Encrypt([]byte(secretKey), nil)
	if err != nil {
		return dtos.CreateAPIKeyResponse{}, utils.WrapError(500, "key_creation_failed", "Could not create API credentials", err)
	}
	credential := mapper.CreateAPIKeyRequestToEntity(request, accessKey, userID, accountID, projectID, encryptedSecret)
	if err := s.keys.Create(ctx, credential); err != nil {
		return dtos.CreateAPIKeyResponse{}, utils.WrapError(503, "storage_unavailable", "Could not create API credentials", err)
	}
	return dtos.CreateAPIKeyResponse{
		AccessKey: accessKey,
		SecretKey: secretKey,
		Algorithm: "HMAC-SHA256",
		AccountID: accountID,
		ProjectID: projectID,
	}, nil
}

const maxListedAPIKeys = 200

// ListAPIKeys returns the caller's keys without their secrets.
func (s *AuthService) ListAPIKeys(ctx context.Context, userID string) (dtos.ListAPIKeysResponse, error) {
	credentials, err := s.keys.ListByUser(ctx, userID, maxListedAPIKeys)
	if err != nil {
		return dtos.ListAPIKeysResponse{}, utils.WrapError(503, "storage_unavailable", "Could not load API keys", err)
	}
	response := dtos.ListAPIKeysResponse{Keys: make([]dtos.APIKeyResponse, 0, len(credentials))}
	for _, credential := range credentials {
		response.Keys = append(response.Keys, mapper.APIKeyToResponse(credential))
	}
	return response, nil
}

// ClientIdentity identifies the project an authenticated API key writes to.
type ClientIdentity struct {
	AccessKey string
	AccountID string
	ProjectID string
}

var ErrAPIKeyNotScoped = utils.NewError(403, "key_not_scoped",
	"This API key is not linked to a project; create a new key with POST /v1/accounts/{accountID}/projects/{projectID}/keys")

// Authenticate verifies a signed request and returns the key's project.
func (s *AuthService) Authenticate(ctx context.Context, accessKey, timestamp, method, requestURI string, body []byte, signature string) (ClientIdentity, error) {
	if accessKey == "" || timestamp == "" || signature == "" {
		return ClientIdentity{}, ErrInvalidSignature
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ClientIdentity{}, ErrInvalidSignature
	}
	requestTime := time.Unix(seconds, 0)
	if age := time.Since(requestTime); age > signatureMaxAge || age < -signatureMaxAge {
		return ClientIdentity{}, ErrInvalidSignature
	}
	credential, err := s.keys.FindByAccessKey(ctx, accessKey)
	if errors.Is(err, interfaces.ErrAPIKeyNotFound) {
		return ClientIdentity{}, ErrInvalidSignature
	}
	if err != nil {
		return ClientIdentity{}, utils.WrapError(503, "authentication_unavailable", "Could not verify request credentials", fmt.Errorf("look up API key: %w", err))
	}
	secret, err := s.secrets.Decrypt(credential.EncryptedSecret, nil)
	if err != nil {
		return ClientIdentity{}, utils.WrapError(500, "internal_error", "Could not verify request credentials", fmt.Errorf("decrypt API key secret: %w", err))
	}
	provided, err := hex.DecodeString(signature)
	if err != nil {
		return ClientIdentity{}, ErrInvalidSignature
	}
	bodyHash := sha256.Sum256(body)
	canonical := timestamp + "\n" + method + "\n" + requestURI + "\n" + hex.EncodeToString(bodyHash[:])
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return ClientIdentity{}, ErrInvalidSignature
	}
	// Keys created before events were linked to projects still verify, but
	// cannot write: their events would have no owner.
	if credential.ProjectID == "" || credential.AccountID == "" {
		return ClientIdentity{}, ErrAPIKeyNotScoped
	}
	return ClientIdentity{AccessKey: credential.AccessKey, AccountID: credential.AccountID, ProjectID: credential.ProjectID}, nil
}

func randomToken(prefix string, bytesCount int) (string, error) {
	bytes := make([]byte, bytesCount)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	if prefix == "" {
		return base64.RawURLEncoding.EncodeToString(bytes), nil
	}
	return prefix + hex.EncodeToString(bytes), nil
}
