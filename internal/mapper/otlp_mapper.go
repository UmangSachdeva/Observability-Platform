package mapper

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"observability-platform/internal/entity"
)

const (
	maxAttributesPerSpan = 128
	maxEventsPerSpan     = 128
	maxAttributeValueLen = 8 << 10
	maxStacktraceLen     = 64 << 10
)

var zeroTraceID = make([]byte, 16)

// OTLPToSpans converts an OTLP export request into spans for one project.
// Spans with an invalid trace or span id are skipped and counted in rejected,
// which the caller reports back as OTLP partial success.
func OTLPToSpans(request *coltracepb.ExportTraceServiceRequest, accountID, projectID string, receivedAt time.Time) (spans []entity.Span, rejected int64, reason string) {
	for _, resourceSpans := range request.GetResourceSpans() {
		resource := attributesToList(resourceSpans.GetResource().GetAttributes())
		serviceName := "unknown_service"
		for _, attribute := range resource {
			if attribute.Key == "service.name" && attribute.Value != "" {
				serviceName = attribute.Value
			}
		}
		for _, scopeSpans := range resourceSpans.GetScopeSpans() {
			scope := scopeSpans.GetScope().GetName()
			for _, span := range scopeSpans.GetSpans() {
				if len(span.GetTraceId()) != 16 || bytes.Equal(span.GetTraceId(), zeroTraceID) || len(span.GetSpanId()) != 8 ||
					(len(span.GetParentSpanId()) != 0 && len(span.GetParentSpanId()) != 8) {
					rejected++
					reason = "spans need a 16-byte trace_id, an 8-byte span_id and an empty or 8-byte parent_span_id"
					continue
				}
				spans = append(spans, convertSpan(span, accountID, projectID, serviceName, scope, resource, receivedAt))
			}
		}
	}
	return spans, rejected, reason
}

func convertSpan(span *tracepb.Span, accountID, projectID, serviceName, scope string, resource []entity.Attribute, receivedAt time.Time) entity.Span {
	start := unixNano(span.GetStartTimeUnixNano())
	end := unixNano(span.GetEndTimeUnixNano())
	if end.Before(start) {
		end = start
	}
	name := strings.TrimSpace(span.GetName())
	if name == "" {
		name = "(unnamed span)"
	}
	converted := entity.Span{
		AccountID:          accountID,
		ProjectID:          projectID,
		TraceID:            hex.EncodeToString(span.GetTraceId()),
		SpanID:             hex.EncodeToString(span.GetSpanId()),
		ParentSpanID:       hex.EncodeToString(span.GetParentSpanId()),
		Name:               truncate(name, 512),
		Kind:               strings.TrimPrefix(span.GetKind().String(), "SPAN_KIND_"),
		ServiceName:        serviceName,
		ScopeName:          scope,
		StartTime:          start,
		EndTime:            end,
		DurationNs:         end.Sub(start).Nanoseconds(),
		StatusCode:         strings.TrimPrefix(span.GetStatus().GetCode().String(), "STATUS_CODE_"),
		StatusMessage:      truncate(span.GetStatus().GetMessage(), maxAttributeValueLen),
		Attributes:         attributesToList(span.GetAttributes()),
		ResourceAttributes: resource,
		ReceivedAt:         receivedAt,
	}
	for i, event := range span.GetEvents() {
		if i == maxEventsPerSpan {
			break
		}
		converted.Events = append(converted.Events, entity.SpanEvent{
			Name:       truncate(event.GetName(), 512),
			Time:       unixNano(event.GetTimeUnixNano()),
			Attributes: attributesToList(event.GetAttributes()),
		})
	}
	return converted
}

func attributesToList(attributes []*commonpb.KeyValue) []entity.Attribute {
	if len(attributes) == 0 {
		return nil
	}
	list := make([]entity.Attribute, 0, min(len(attributes), maxAttributesPerSpan))
	for _, attribute := range attributes {
		if len(list) == maxAttributesPerSpan {
			break
		}
		limit := maxAttributeValueLen
		if attribute.GetKey() == "exception.stacktrace" {
			limit = maxStacktraceLen
		}
		list = append(list, entity.Attribute{Key: truncate(attribute.GetKey(), 256), Value: truncate(anyValueString(attribute.GetValue()), limit)})
	}
	return list
}

func anyValueString(value *commonpb.AnyValue) string {
	switch v := value.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return v.StringValue
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(v.BoolValue)
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(v.IntValue, 10)
	case *commonpb.AnyValue_DoubleValue:
		return strconv.FormatFloat(v.DoubleValue, 'g', -1, 64)
	case *commonpb.AnyValue_BytesValue:
		return base64.StdEncoding.EncodeToString(v.BytesValue)
	case *commonpb.AnyValue_ArrayValue:
		items := make([]string, 0, len(v.ArrayValue.GetValues()))
		for _, item := range v.ArrayValue.GetValues() {
			items = append(items, anyValueString(item))
		}
		encoded, _ := json.Marshal(items)
		return string(encoded)
	case *commonpb.AnyValue_KvlistValue:
		items := map[string]string{}
		for _, item := range v.KvlistValue.GetValues() {
			items[item.GetKey()] = anyValueString(item.GetValue())
		}
		encoded, _ := json.Marshal(items)
		return string(encoded)
	default:
		return ""
	}
}

func unixNano(n uint64) time.Time {
	if n == 0 || n > uint64(1<<63-1) {
		return time.Unix(0, 0).UTC()
	}
	return time.Unix(0, int64(n)).UTC()
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "…"
}

// FixOTLPJSONIDs rewrites the hex traceId, spanId and parentSpanId fields of
// an OTLP/JSON body to base64, the form the standard protobuf JSON decoder
// expects. OTLP/JSON uses hex for ids, unlike the usual protobuf mapping.
func FixOTLPJSONIDs(data []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber() // keep 64-bit nanosecond timestamps exact
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if err := rewriteIDs(document); err != nil {
		return nil, err
	}
	return json.Marshal(document)
}

func rewriteIDs(node any) error {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "traceId" || key == "spanId" || key == "parentSpanId" {
				if text, ok := child.(string); ok {
					raw, err := hex.DecodeString(text)
					if err != nil {
						return fmt.Errorf("%s must be hex: %w", key, err)
					}
					value[key] = base64.StdEncoding.EncodeToString(raw)
					continue
				}
			}
			if err := rewriteIDs(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := rewriteIDs(child); err != nil {
				return err
			}
		}
	}
	return nil
}
