package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"observability-platform/internal/dtos"
	"observability-platform/internal/entity"
	"observability-platform/internal/mapper"
	"observability-platform/internal/repository/interfaces"
	"observability-platform/internal/utils"
	"observability-platform/internal/validation"
)

const (
	userTokenLifetime        = 24 * time.Hour
	userRefreshTokenLifetime = 30 * 24 * time.Hour
)

type UserService struct {
	users    interfaces.UserRepository
	accounts interfaces.AccountRepository
	tokenKey []byte
}

func NewUserService(users interfaces.UserRepository, accounts interfaces.AccountRepository, encodedTokenKey string) (*UserService, error) {
	key, err := base64.StdEncoding.DecodeString(encodedTokenKey)
	if err != nil {
		return nil, fmt.Errorf("USER_JWT_SECRET must be base64 encoded: %w", err)
	}
	if len(key) < 32 {
		return nil, errors.New("USER_JWT_SECRET must decode to at least 32 bytes")
	}
	return &UserService{users: users, accounts: accounts, tokenKey: key}, nil
}

func (s *UserService) Signup(ctx context.Context, request dtos.SignupRequest) (dtos.SignupResponse, error) {
	request.Username = strings.ToLower(strings.TrimSpace(request.Username))
	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	if err := validation.ValidateSignup(request); err != nil {
		return dtos.SignupResponse{}, err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(request.Password), 12)
	if err != nil {
		return dtos.SignupResponse{}, utils.WrapError(500, "user_creation_failed", "Could not create user account", err)
	}
	id, err := newID()
	if err != nil {
		return dtos.SignupResponse{}, utils.WrapError(500, "user_creation_failed", "Could not create user account", err)
	}
	accountID, err := newID()
	if err != nil {
		return dtos.SignupResponse{}, utils.WrapError(500, "user_creation_failed", "Could not create user account", err)
	}
	now := time.Now().UTC()
	account := mapper.SignupRequestToAccount(request, accountID, id, now)
	if err := s.accounts.Create(ctx, account); err != nil {
		return dtos.SignupResponse{}, utils.WrapError(503, "storage_unavailable", "Could not create user account", err)
	}
	user := mapper.SignupRequestToUser(request, id, string(passwordHash), now)
	if err := s.users.Create(ctx, user); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cleanupErr := s.accounts.DeleteByID(cleanupCtx, accountID)
		cancel()
		if cleanupErr != nil {
			return dtos.SignupResponse{}, utils.WrapError(500, "account_cleanup_failed", "Could not complete user registration", errors.Join(err, cleanupErr))
		}
		if errors.Is(err, interfaces.ErrUserAlreadyExists) {
			return dtos.SignupResponse{}, utils.NewError(409, "user_exists", "Username or email is already registered")
		}
		return dtos.SignupResponse{}, utils.WrapError(503, "storage_unavailable", "Could not create user account", err)
	}
	response := mapper.UserToSignupResponse(user, accountID)
	accessToken, refreshToken, accessExpiresAt, refreshExpiresAt, err := s.createTokenPair(user)
	if err != nil {
		return dtos.SignupResponse{}, utils.WrapError(500, "token_creation_failed", "Could not create login tokens", err)
	}
	response.AccessToken = accessToken
	response.RefreshToken = refreshToken
	response.TokenType = "Bearer"
	response.ExpiresAt = accessExpiresAt
	response.RefreshExpiresAt = refreshExpiresAt
	return response, nil
}

func (s *UserService) Login(ctx context.Context, request dtos.LoginRequest) (dtos.LoginResponse, error) {
	request.Login = strings.ToLower(strings.TrimSpace(request.Login))
	if err := validation.ValidateLogin(request); err != nil {
		return dtos.LoginResponse{}, err
	}
	user, err := s.users.FindByLogin(ctx, request.Login)
	if errors.Is(err, interfaces.ErrUserNotFound) {
		return dtos.LoginResponse{}, invalidLoginError()
	}
	if err != nil {
		return dtos.LoginResponse{}, utils.WrapError(503, "storage_unavailable", "Could not verify login", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(request.Password)); err != nil {
		return dtos.LoginResponse{}, invalidLoginError()
	}
	// A user who has left every account can still log in (for example to accept
	// a new invite); the response then has no account_id.
	account, err := s.accounts.FindDefaultForUser(ctx, user.ID)
	if err != nil && !errors.Is(err, interfaces.ErrAccountNotFound) {
		return dtos.LoginResponse{}, utils.WrapError(503, "storage_unavailable", "Could not load user account", err)
	}
	accessToken, refreshToken, accessExpiresAt, refreshExpiresAt, err := s.createTokenPair(user)
	if err != nil {
		return dtos.LoginResponse{}, utils.WrapError(500, "token_creation_failed", "Could not create login tokens", err)
	}
	return dtos.LoginResponse{
		AccountID: account.ID, AccessToken: accessToken, RefreshToken: refreshToken, TokenType: "Bearer",
		ExpiresAt: accessExpiresAt, RefreshExpiresAt: refreshExpiresAt,
	}, nil
}

func invalidLoginError() error {
	return utils.NewError(401, "invalid_credentials", "Username/email or password is incorrect")
}

type userTokenClaims struct {
	Subject  string `json:"sub"`
	Username string `json:"username"`
	Email    string `json:"email"`
	TokenUse string `json:"token_use"`
	IssuedAt int64  `json:"iat"`
	Expires  int64  `json:"exp"`
}

type UserIdentity struct {
	ID       string
	Username string
	Email    string
}

var ErrInvalidUserToken = utils.NewError(401, "unauthorized", "Valid user bearer token required")

func (s *UserService) AuthenticateToken(token string) (UserIdentity, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return UserIdentity{}, ErrInvalidUserToken
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return UserIdentity{}, ErrInvalidUserToken
	}
	var header map[string]string
	if err := json.Unmarshal(headerJSON, &header); err != nil || header["alg"] != "HS256" || header["typ"] != "JWT" {
		return UserIdentity{}, ErrInvalidUserToken
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return UserIdentity{}, ErrInvalidUserToken
	}
	unsigned := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, s.tokenKey)
	_, _ = mac.Write([]byte(unsigned))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return UserIdentity{}, ErrInvalidUserToken
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return UserIdentity{}, ErrInvalidUserToken
	}
	var claims userTokenClaims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return UserIdentity{}, ErrInvalidUserToken
	}
	now := time.Now().Unix()
	if claims.Subject == "" || claims.TokenUse != "access" || claims.Expires <= now || claims.IssuedAt > now+60 || claims.Expires-claims.IssuedAt > int64(userTokenLifetime.Seconds()) {
		return UserIdentity{}, ErrInvalidUserToken
	}
	return UserIdentity{ID: claims.Subject, Username: claims.Username, Email: claims.Email}, nil
}

func (s *UserService) createTokenPair(user entity.User) (accessToken, refreshToken string, accessExpiresAt, refreshExpiresAt time.Time, err error) {
	issuedAt := time.Now().UTC()
	accessExpiresAt = issuedAt.Add(userTokenLifetime)
	refreshExpiresAt = issuedAt.Add(userRefreshTokenLifetime)
	accessToken, err = s.createToken(user, "access", issuedAt, accessExpiresAt)
	if err != nil {
		return "", "", time.Time{}, time.Time{}, err
	}
	refreshToken, err = s.createToken(user, "refresh", issuedAt, refreshExpiresAt)
	if err != nil {
		return "", "", time.Time{}, time.Time{}, err
	}
	return accessToken, refreshToken, accessExpiresAt, refreshExpiresAt, nil
}

func (s *UserService) createToken(user entity.User, tokenUse string, issuedAt, expiresAt time.Time) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(userTokenClaims{
		Subject: user.ID, Username: user.Username, Email: user.Email,
		TokenUse: tokenUse, IssuedAt: issuedAt.Unix(), Expires: expiresAt.Unix(),
	})
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, s.tokenKey)
	_, _ = mac.Write([]byte(unsigned))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return unsigned + "." + signature, nil
}
