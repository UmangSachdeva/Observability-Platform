package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"observability-platform/internal/dtos"
	"observability-platform/internal/entity"
	"observability-platform/internal/mapper"
	"observability-platform/internal/repository/interfaces"
	"observability-platform/internal/utils"
	"observability-platform/internal/validation"
)

type ErrorService struct {
	repository interfaces.ErrorRepository
	accounts   interfaces.AccountRepository
	projects   interfaces.ProjectRepository
}

func NewErrorService(repository interfaces.ErrorRepository, accounts interfaces.AccountRepository, projects interfaces.ProjectRepository) *ErrorService {
	return &ErrorService{repository: repository, accounts: accounts, projects: projects}
}

// Create stores an event under the project of the API key that sent it.
func (s *ErrorService) Create(ctx context.Context, accountID, projectID string, request dtos.CreateErrorRequest) (entity.ErrorEvent, error) {
	if accountID == "" || projectID == "" {
		return entity.ErrorEvent{}, utils.WrapError(500, "internal_error", "Could not accept error event", errors.New("event has no project"))
	}
	if err := validation.ValidateCreateError(request); err != nil {
		return entity.ErrorEvent{}, err
	}
	request.Service = strings.TrimSpace(request.Service)
	request.Message = strings.TrimSpace(request.Message)
	request.Level = strings.ToLower(strings.TrimSpace(request.Level))
	if request.Level == "" {
		request.Level = "error"
	}
	if request.Timestamp.IsZero() {
		request.Timestamp = time.Now().UTC()
	} else {
		request.Timestamp = request.Timestamp.UTC()
	}
	request.Environment = strings.TrimSpace(request.Environment)
	id, err := newID()
	if err != nil {
		return entity.ErrorEvent{}, utils.WrapError(500, "internal_error", "Could not create error event", err)
	}
	event := mapper.CreateErrorRequestToEntity(request, id, accountID, projectID, time.Now().UTC())
	if err := s.repository.Create(ctx, event); err != nil {
		return entity.ErrorEvent{}, utils.WrapError(503, "storage_unavailable", "Could not accept error event", err)
	}
	return event, nil
}

// SearchProject searches one project's events for a member of its account.
func (s *ErrorService) SearchProject(ctx context.Context, accountID, projectID, userID string, request dtos.SearchErrorsRequest) (dtos.ListErrorsResponse, error) {
	if len(request.ProjectIDs) > 0 {
		return dtos.ListErrorsResponse{}, utils.NewError(400, "validation_error",
			"project_id is only accepted by GET /v1/accounts/{accountID}/errors")
	}
	if err := requireProjectAccess(ctx, s.accounts, s.projects, accountID, projectID, userID); err != nil {
		return dtos.ListErrorsResponse{}, err
	}
	request.ProjectIDs = []string{projectID}
	return s.search(ctx, accountID, request)
}

// SearchAccount searches every project of the account, optionally narrowed
// with project_id, for a member of the account.
func (s *ErrorService) SearchAccount(ctx context.Context, accountID, userID string, request dtos.SearchErrorsRequest) (dtos.ListErrorsResponse, error) {
	member, err := s.accounts.IsMember(ctx, accountID, userID)
	if err != nil {
		return dtos.ListErrorsResponse{}, utils.WrapError(503, "storage_unavailable", "Could not verify account membership", err)
	}
	if !member {
		return dtos.ListErrorsResponse{}, utils.NewError(403, "forbidden", "You are not a member of this account")
	}
	return s.search(ctx, accountID, request)
}

func (s *ErrorService) search(ctx context.Context, accountID string, request dtos.SearchErrorsRequest) (dtos.ListErrorsResponse, error) {
	query, err := validation.ParseErrorSearch(request)
	if err != nil {
		return dtos.ListErrorsResponse{}, err
	}
	query.AccountID = accountID
	fingerprint := queryFingerprint(query)
	if request.Cursor != "" {
		after, err := decodeErrorCursor(request.Cursor, query, fingerprint)
		if err != nil {
			return dtos.ListErrorsResponse{}, utils.NewError(400, "invalid_cursor",
				"cursor is invalid or was created for different filters or sort")
		}
		query.After = after
	}
	pageSize := query.Limit
	query.Limit = pageSize + 1 // one extra row tells whether another page exists
	events, err := s.repository.Search(ctx, query)
	if err != nil {
		return dtos.ListErrorsResponse{}, utils.WrapError(503, "storage_unavailable", "Could not load error events", err)
	}
	response := dtos.ListErrorsResponse{}
	if len(events) > pageSize {
		events = events[:pageSize]
		response.HasMore = true
		response.NextCursor = encodeErrorCursor(query, fingerprint, events[len(events)-1])
	}
	response.Errors = make([]dtos.ErrorEventResponse, 0, len(events))
	for _, event := range events {
		response.Errors = append(response.Errors, mapper.EntityToErrorEventResponse(event))
	}
	return response, nil
}

func newID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
