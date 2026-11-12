package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"observability-platform/internal/entity"
	"observability-platform/internal/repository/interfaces"
)

const maxCursorLength = 1024

// errorCursorToken is the opaque ?cursor= value. It is not secret: the
// account filter is always applied on the server, so a tampered cursor can
// only move within data the caller may already read.
type errorCursorToken struct {
	Query string `json:"q"`
	Value string `json:"v"`
	ID    string `json:"id"`
}

var errInvalidCursor = errors.New("invalid cursor")

// queryFingerprint identifies the filters and sort a cursor belongs to, so a
// cursor cannot be reused with different parameters.
func queryFingerprint(query interfaces.ErrorQuery) string {
	sorted := func(values []string) []string {
		copied := slices.Clone(values)
		slices.Sort(copied)
		return copied
	}
	formatTime := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.UTC().Format(time.RFC3339Nano)
	}
	canonical, _ := json.Marshal(map[string]any{
		"account":    query.AccountID,
		"projects":   sorted(query.ProjectIDs),
		"text":       query.Text,
		"services":   sorted(query.Services),
		"envs":       sorted(query.Environments),
		"levels":     sorted(query.Levels),
		"attributes": query.Attributes,
		"from":       formatTime(query.From),
		"to":         formatTime(query.To),
		"sort":       query.SortField,
		"descending": query.SortDesc,
	})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:8])
}

func isTimeSortField(field interfaces.ErrorSortField) bool {
	return field == interfaces.ErrorSortReceivedAt || field == interfaces.ErrorSortTimestamp
}

func sortValue(field interfaces.ErrorSortField, event entity.ErrorEvent) string {
	switch field {
	case interfaces.ErrorSortTimestamp:
		return event.Timestamp.UTC().Format(time.RFC3339Nano)
	case interfaces.ErrorSortLevel:
		return event.Level
	case interfaces.ErrorSortService:
		return event.Service
	default:
		return event.ReceivedAt.UTC().Format(time.RFC3339Nano)
	}
}

func encodeErrorCursor(query interfaces.ErrorQuery, fingerprint string, last entity.ErrorEvent) string {
	token, _ := json.Marshal(errorCursorToken{Query: fingerprint, Value: sortValue(query.SortField, last), ID: last.ID})
	return base64.RawURLEncoding.EncodeToString(token)
}

func decodeErrorCursor(raw string, query interfaces.ErrorQuery, fingerprint string) (*interfaces.ErrorCursor, error) {
	if len(raw) > maxCursorLength {
		return nil, errInvalidCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, errInvalidCursor
	}
	var token errorCursorToken
	if err := json.Unmarshal(data, &token); err != nil || token.ID == "" || token.Query != fingerprint {
		return nil, errInvalidCursor
	}
	if isTimeSortField(query.SortField) {
		parsed, err := time.Parse(time.RFC3339Nano, token.Value)
		if err != nil {
			return nil, errInvalidCursor
		}
		return &interfaces.ErrorCursor{Value: parsed.UTC(), ID: token.ID}, nil
	}
	return &interfaces.ErrorCursor{Value: token.Value, ID: token.ID}, nil
}
