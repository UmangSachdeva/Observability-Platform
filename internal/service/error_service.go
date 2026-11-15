package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"observability-platform/internal/dtos"
	"observability-platform/internal/entity"
	"observability-platform/internal/mapper"
	"observability-platform/internal/repository/interfaces"
	"observability-platform/internal/utils"
	"observability-platform/internal/validation"
)

var tracer = otel.Tracer("observability-platform/service")

type ErrorService struct {
	repository interfaces.ErrorRepository
	accounts   interfaces.AccountRepository
	projects   interfaces.ProjectRepository
}

func NewErrorService(repository interfaces.ErrorRepository, accounts interfaces.AccountRepository, projects interfaces.ProjectRepository) *ErrorService {
	return &ErrorService{repository: repository, accounts: accounts, projects: projects}
}

// Create records one occurrence of an error under the API key's project.
// Errors with the same service, environment, level, message and stack trace
// are grouped into one record whose count grows; a different message is a
// different error. A retried or replayed delivery is recognised and not
// counted again (duplicate is true).
func (s *ErrorService) Create(ctx context.Context, accountID, projectID string, request dtos.CreateErrorRequest) (event entity.ErrorEvent, duplicate bool, err error) {
	ctx, span := tracer.Start(ctx, "ErrorService.Create", trace.WithAttributes(attribute.String("project.id", projectID)))
	defer span.End()
	event, duplicate, err = s.create(ctx, accountID, projectID, request)
	if err == nil {
		span.SetAttributes(attribute.String("event.level", event.Level), attribute.String("event.id", event.ID), attribute.Bool("event.duplicate", duplicate))
	}
	return event, duplicate, err
}

func (s *ErrorService) create(ctx context.Context, accountID, projectID string, request dtos.CreateErrorRequest) (entity.ErrorEvent, bool, error) {
	if accountID == "" || projectID == "" {
		return entity.ErrorEvent{}, false, utils.WrapError(500, "internal_error", "Could not accept error event", errors.New("event has no project"))
	}
	if err := validation.ValidateCreateError(request); err != nil {
		return entity.ErrorEvent{}, false, err
	}
	request.Service = strings.TrimSpace(request.Service)
	request.Message = strings.TrimSpace(request.Message)
	request.Level = strings.ToLower(strings.TrimSpace(request.Level))
	if request.Level == "" {
		request.Level = "error"
	}
	receivedAt := time.Now().UTC()
	if request.Timestamp.IsZero() {
		request.Timestamp = receivedAt
	} else {
		request.Timestamp = request.Timestamp.UTC()
	}
	request.Environment = strings.TrimSpace(request.Environment)
	request.TraceID = strings.ToLower(strings.TrimSpace(request.TraceID))
	request.SpanID = strings.ToLower(strings.TrimSpace(request.SpanID))
	if err := validation.ValidateCreateError(request); err != nil {
		return entity.ErrorEvent{}, false, err
	}
	fingerprint := errorFingerprint(projectID, request)
	// The id comes from the fingerprint, so every occurrence of the same
	// error lands on the same record without a lookup first.
	event := mapper.CreateErrorRequestToEntity(request, fingerprint[:32], accountID, projectID, receivedAt)
	event.Fingerprint = fingerprint
	occurrenceID, err := newID()
	if err != nil {
		return entity.ErrorEvent{}, false, utils.WrapError(500, "internal_error", "Could not create error event", err)
	}
	occurrence := entity.ErrorOccurrence{
		ID: occurrenceID, ErrorID: event.ID, AccountID: accountID, ProjectID: projectID,
		Key: occurrenceKey(request), Timestamp: request.Timestamp, ReceivedAt: receivedAt,
		TraceID: request.TraceID, SpanID: request.SpanID, Attributes: request.Attributes,
	}
	stored, err := s.repository.RecordOccurrence(ctx, event, occurrence)
	if err != nil {
		return entity.ErrorEvent{}, false, utils.WrapError(503, "storage_unavailable", "Could not accept error event", err)
	}
	return event, !stored, nil
}

// errorFingerprint identifies "the same error": project, service,
// environment, level, message and stack trace. Time, attributes and trace
// ids are per occurrence and do not change it.
func errorFingerprint(projectID string, request dtos.CreateErrorRequest) string {
	hash := sha256.New()
	for _, part := range []string{"v1", projectID, request.Service, request.Environment, request.Level, request.Message, request.StackTrace} {
		hash.Write([]byte(part))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// occurrenceKey identifies one delivery of an occurrence. A retried or
// replayed request has the same timestamp, trace, span and attributes. When
// the client sends no timestamp the server's receive time is used, so such
// events are always counted.
func occurrenceKey(request dtos.CreateErrorRequest) string {
	keys := make([]string, 0, len(request.Attributes))
	for key := range request.Attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, part := range []string{request.Timestamp.Format(time.RFC3339Nano), request.TraceID, request.SpanID} {
		hash.Write([]byte(part))
		hash.Write([]byte{0})
	}
	for _, key := range keys {
		hash.Write([]byte(key))
		hash.Write([]byte{1})
		hash.Write([]byte(request.Attributes[key]))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
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
// GetProjectEvent returns one event of a project the user can access.
func (s *ErrorService) GetProjectEvent(ctx context.Context, accountID, projectID, eventID, userID string) (entity.ErrorEvent, error) {
	if err := requireProjectAccess(ctx, s.accounts, s.projects, accountID, projectID, userID); err != nil {
		return entity.ErrorEvent{}, err
	}
	if eventID == "" || len(eventID) > 128 {
		return entity.ErrorEvent{}, utils.NewError(404, "error_not_found", "Error event was not found in this project")
	}
	event, err := s.repository.FindByID(ctx, accountID, projectID, eventID)
	if errors.Is(err, interfaces.ErrErrorEventNotFound) {
		return entity.ErrorEvent{}, utils.NewError(404, "error_not_found", "Error event was not found in this project")
	}
	if err != nil {
		return entity.ErrorEvent{}, utils.WrapError(503, "storage_unavailable", "Could not load error event", err)
	}
	return event, nil
}

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
	if query.TraceID != "" {
		// An error matches a trace when any of its occurrences was in it.
		projectID := ""
		if len(query.ProjectIDs) == 1 {
			projectID = query.ProjectIDs[0]
		}
		ids, err := s.repository.ErrorIDsForTrace(ctx, accountID, projectID, query.TraceID, maxErrorsPerTraceLookup)
		if err != nil {
			return dtos.ListErrorsResponse{}, utils.WrapError(503, "storage_unavailable", "Could not load error events", err)
		}
		query.TraceErrorIDs = ids
	}
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

const (
	maxErrorsPerTraceLookup = 200
	defaultOccurrencePage   = 100
	maxOccurrencePage       = 500
)

// ListOccurrences returns when an error happened, newest first, for a member
// of the project's account.
func (s *ErrorService) ListOccurrences(ctx context.Context, accountID, projectID, errorID, userID, rawLimit, rawCursor string) (dtos.ListOccurrencesResponse, error) {
	event, err := s.GetProjectEvent(ctx, accountID, projectID, errorID, userID)
	if err != nil {
		return dtos.ListOccurrencesResponse{}, err
	}
	limit := defaultOccurrencePage
	if rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > maxOccurrencePage {
			return dtos.ListOccurrencesResponse{}, utils.NewError(400, "validation_error", "limit must be between 1 and 500")
		}
		limit = parsed
	}
	var after *interfaces.OccurrenceCursor
	if rawCursor != "" {
		if after, err = decodeOccurrenceCursor(rawCursor, errorID); err != nil {
			return dtos.ListOccurrencesResponse{}, utils.NewError(400, "invalid_cursor", "cursor is invalid or belongs to another error")
		}
	}
	occurrences, err := s.repository.ListOccurrences(ctx, accountID, projectID, errorID, after, limit+1)
	if err != nil {
		return dtos.ListOccurrencesResponse{}, utils.WrapError(503, "storage_unavailable", "Could not load occurrences", err)
	}
	summary := mapper.EntityToErrorEventResponse(event)
	response := dtos.ListOccurrencesResponse{Total: summary.OccurrenceCount, FirstSeen: summary.FirstSeen, LastSeen: event.Timestamp}
	if len(occurrences) == 0 && after == nil && event.OccurrenceCount == 0 {
		// Stored before grouping: the event itself is its only occurrence.
		occurrences = []entity.ErrorOccurrence{{
			ID: event.ID, Timestamp: event.Timestamp, ReceivedAt: event.ReceivedAt,
			TraceID: event.TraceID, SpanID: event.SpanID, Attributes: event.Attributes,
		}}
	}
	if len(occurrences) > limit {
		occurrences = occurrences[:limit]
		response.HasMore = true
		response.NextCursor = encodeOccurrenceCursor(errorID, occurrences[len(occurrences)-1])
	}
	response.Occurrences = make([]dtos.OccurrenceResponse, 0, len(occurrences))
	for _, occurrence := range occurrences {
		response.Occurrences = append(response.Occurrences, mapper.OccurrenceToResponse(occurrence))
	}
	return response, nil
}

type occurrenceCursorToken struct {
	ErrorID    string `json:"e"`
	ReceivedAt string `json:"t"`
	ID         string `json:"id"`
}

func encodeOccurrenceCursor(errorID string, last entity.ErrorOccurrence) string {
	token, _ := json.Marshal(occurrenceCursorToken{ErrorID: errorID, ReceivedAt: last.ReceivedAt.UTC().Format(time.RFC3339Nano), ID: last.ID})
	return base64.RawURLEncoding.EncodeToString(token)
}

func decodeOccurrenceCursor(raw, errorID string) (*interfaces.OccurrenceCursor, error) {
	if len(raw) > maxCursorLength {
		return nil, errInvalidCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, errInvalidCursor
	}
	var token occurrenceCursorToken
	if err := json.Unmarshal(data, &token); err != nil || token.ErrorID != errorID || token.ID == "" {
		return nil, errInvalidCursor
	}
	receivedAt, err := time.Parse(time.RFC3339Nano, token.ReceivedAt)
	if err != nil {
		return nil, errInvalidCursor
	}
	return &interfaces.OccurrenceCursor{ReceivedAt: receivedAt.UTC(), ID: token.ID}, nil
}
