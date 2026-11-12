package validation

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"observability-platform/internal/dtos"
	"observability-platform/internal/repository/interfaces"
	"observability-platform/internal/utils"
)

const (
	defaultErrorPageSize = 100
	maxErrorPageSize     = 500
	maxSearchTextLength  = 200
	maxFilterValues      = 20
	maxFilterValueLength = 100
	maxAttributeFilters  = 10
)

var attributeKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

var errorSortFields = map[string]interfaces.ErrorSortField{
	"received_at": interfaces.ErrorSortReceivedAt,
	"timestamp":   interfaces.ErrorSortTimestamp,
	"level":       interfaces.ErrorSortLevel,
	"service":     interfaces.ErrorSortService,
}

// ParseErrorSearch validates the search parameters and turns them into a
// repository query. The caller sets AccountID and the cursor.
func ParseErrorSearch(request dtos.SearchErrorsRequest) (interfaces.ErrorQuery, error) {
	query := interfaces.ErrorQuery{
		SortField: interfaces.ErrorSortReceivedAt,
		SortDesc:  true,
		Limit:     defaultErrorPageSize,
	}
	query.Text = strings.TrimSpace(request.Query)
	if len(query.Text) > maxSearchTextLength {
		return interfaces.ErrorQuery{}, invalidSearch("q must be at most 200 characters")
	}
	var err error
	if query.ProjectIDs, err = filterValues("project_id", request.ProjectIDs, false); err != nil {
		return interfaces.ErrorQuery{}, err
	}
	if query.Services, err = filterValues("service", request.Services, false); err != nil {
		return interfaces.ErrorQuery{}, err
	}
	if query.Environments, err = filterValues("environment", request.Environments, false); err != nil {
		return interfaces.ErrorQuery{}, err
	}
	if query.Levels, err = filterValues("level", request.Levels, true); err != nil {
		return interfaces.ErrorQuery{}, err
	}
	if len(request.Attributes) > maxAttributeFilters {
		return interfaces.ErrorQuery{}, invalidSearch("at most 10 attr.<key> filters are allowed")
	}
	for key, value := range request.Attributes {
		if !attributeKeyPattern.MatchString(key) {
			return interfaces.ErrorQuery{}, invalidSearch("attr.<key> keys may only contain letters, digits, '_' and '-' (max 64)")
		}
		if len(value) > maxFilterValueLength {
			return interfaces.ErrorQuery{}, invalidSearch("attr." + key + " must be at most 100 characters")
		}
	}
	if len(request.Attributes) > 0 {
		query.Attributes = request.Attributes
	}
	if query.From, err = parseSearchTime("from", request.From); err != nil {
		return interfaces.ErrorQuery{}, err
	}
	if query.To, err = parseSearchTime("to", request.To); err != nil {
		return interfaces.ErrorQuery{}, err
	}
	if query.From != nil && query.To != nil && query.From.After(*query.To) {
		return interfaces.ErrorQuery{}, invalidSearch("from must not be later than to")
	}
	if sort := strings.TrimSpace(request.Sort); sort != "" {
		desc := strings.HasPrefix(sort, "-")
		field, ok := errorSortFields[strings.TrimPrefix(sort, "-")]
		if !ok {
			return interfaces.ErrorQuery{}, invalidSearch("sort must be one of received_at, timestamp, level, service, optionally prefixed with '-' for descending")
		}
		query.SortField, query.SortDesc = field, desc
	}
	if raw := strings.TrimSpace(request.Limit); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxErrorPageSize {
			return interfaces.ErrorQuery{}, invalidSearch("limit must be between 1 and 500")
		}
		query.Limit = limit
	}
	return query, nil
}

// filterValues trims, drops empty and duplicate values, and checks limits.
func filterValues(name string, values []string, lower bool) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lower {
			value = strings.ToLower(value)
		}
		if value == "" {
			continue
		}
		if len(value) > maxFilterValueLength {
			return nil, invalidSearch(name + " values must be at most 100 characters")
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		cleaned = append(cleaned, value)
	}
	if len(cleaned) > maxFilterValues {
		return nil, invalidSearch("at most 20 " + name + " values are allowed")
	}
	if len(cleaned) == 0 {
		return nil, nil
	}
	return cleaned, nil
}

func parseSearchTime(name, raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, invalidSearch(name + " must be an RFC 3339 timestamp, for example 2026-11-12T09:00:00Z")
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func invalidSearch(message string) error {
	return utils.NewError(400, "validation_error", message)
}
