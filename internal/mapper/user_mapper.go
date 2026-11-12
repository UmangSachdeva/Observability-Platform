package mapper

import (
	"time"

	"observability-platform/internal/dtos"
	"observability-platform/internal/entity"
)

func SignupRequestToUser(request dtos.SignupRequest, id, passwordHash string, now time.Time) entity.User {
	return entity.User{
		ID: id, Username: request.Username, Email: request.Email,
		PasswordHash: passwordHash, CreatedAt: now, UpdatedAt: now,
	}
}

func SignupRequestToAccount(request dtos.SignupRequest, accountID, userID string, now time.Time) entity.Account {
	return entity.Account{
		ID: accountID, Name: request.Username + "'s account",
		Members:   []entity.AccountMember{{UserID: userID, Role: "admin", JoinedAt: now}},
		CreatedAt: now, UpdatedAt: now,
	}
}

func UserToSignupResponse(user entity.User, accountID string) dtos.SignupResponse {
	return dtos.SignupResponse{
		ID: user.ID, AccountID: accountID, Username: user.Username, Email: user.Email,
		CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt,
	}
}
