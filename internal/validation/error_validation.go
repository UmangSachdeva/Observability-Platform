package validation

import (
	"regexp"
	"strings"

	"observability-platform/internal/dtos"
	"observability-platform/internal/utils"
)

var ErrInvalidErrorRequest = utils.NewError(400, "validation_error", "service and message are required")

func ValidateCreateError(request dtos.CreateErrorRequest) error {
	if strings.TrimSpace(request.Service) == "" || strings.TrimSpace(request.Message) == "" {
		return ErrInvalidErrorRequest
	}
	if request.TraceID != "" && !IsTraceID(request.TraceID) {
		return utils.NewError(400, "validation_error", "trace_id must be 32 lowercase hex characters")
	}
	if request.SpanID != "" && !spanIDPattern.MatchString(request.SpanID) {
		return utils.NewError(400, "validation_error", "span_id must be 16 lowercase hex characters")
	}
	if request.SpanID != "" && request.TraceID == "" {
		return utils.NewError(400, "validation_error", "span_id requires trace_id")
	}
	return nil
}

var (
	traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	spanIDPattern  = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// IsTraceID reports whether id is a non-zero OpenTelemetry trace id in
// lowercase hex.
func IsTraceID(id string) bool {
	return traceIDPattern.MatchString(id) && id != strings.Repeat("0", 32)
}
