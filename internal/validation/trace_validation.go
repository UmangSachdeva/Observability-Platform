package validation

import (
	"strconv"
	"strings"

	"observability-platform/internal/dtos"
	"observability-platform/internal/repository/interfaces"
)

const (
	defaultTracePageSize = 50
	maxTracePageSize     = 200
)

// ParseTraceSearch validates trace list parameters. The caller sets the
// account, project and cursor.
func ParseTraceSearch(request dtos.SearchTracesRequest) (interfaces.TraceQuery, error) {
	query := interfaces.TraceQuery{Limit: defaultTracePageSize}
	query.Text = strings.TrimSpace(request.Query)
	if len(query.Text) > maxSearchTextLength {
		return interfaces.TraceQuery{}, invalidSearch("q must be at most 200 characters")
	}
	var err error
	if query.Services, err = filterValues("service", request.Services, false); err != nil {
		return interfaces.TraceQuery{}, err
	}
	switch strings.ToLower(strings.TrimSpace(request.Status)) {
	case "":
	case "error":
		query.ErrorsOnly = true
	default:
		return interfaces.TraceQuery{}, invalidSearch("status must be error or left out")
	}
	if query.From, err = parseSearchTime("from", request.From); err != nil {
		return interfaces.TraceQuery{}, err
	}
	if query.To, err = parseSearchTime("to", request.To); err != nil {
		return interfaces.TraceQuery{}, err
	}
	if query.From != nil && query.To != nil && query.From.After(*query.To) {
		return interfaces.TraceQuery{}, invalidSearch("from must not be later than to")
	}
	if raw := strings.TrimSpace(request.Limit); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxTracePageSize {
			return interfaces.TraceQuery{}, invalidSearch("limit must be between 1 and 200")
		}
		query.Limit = limit
	}
	return query, nil
}
