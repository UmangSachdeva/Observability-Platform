package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"observability-platform/internal/dtos"
	"observability-platform/internal/entity"
	"observability-platform/internal/mapper"
	"observability-platform/internal/repository/interfaces"
	"observability-platform/internal/utils"
	"observability-platform/internal/validation"
)

const (
	maxSpansPerTrace  = 2000
	maxErrorsPerTrace = 50
	maxSpansPerExport = 20000
)

type TraceService struct {
	spans    interfaces.SpanRepository
	errors   interfaces.ErrorRepository
	accounts interfaces.AccountRepository
	projects interfaces.ProjectRepository
}

func NewTraceService(spans interfaces.SpanRepository, errorEvents interfaces.ErrorRepository, accounts interfaces.AccountRepository, projects interfaces.ProjectRepository) *TraceService {
	return &TraceService{spans: spans, errors: errorEvents, accounts: accounts, projects: projects}
}

// Ingest stores the spans of an OTLP export request under the API key's
// project. It returns how many spans were rejected and why.
func (s *TraceService) Ingest(ctx context.Context, client ClientIdentity, request *coltracepb.ExportTraceServiceRequest) (int64, string, error) {
	spans, rejected, reason := mapper.OTLPToSpans(request, client.AccountID, client.ProjectID, time.Now().UTC())
	if len(spans) > maxSpansPerExport {
		return 0, "", utils.NewError(413, "too_many_spans", "An export may contain at most 20000 spans")
	}
	if len(spans) == 0 {
		return rejected, reason, nil
	}
	stored, err := s.spans.InsertSpans(ctx, spans)
	if err != nil {
		return 0, "", utils.WrapError(503, "storage_unavailable", "Could not store spans", err)
	}
	// Spans an exporter re-sent after a retry are already stored; counting
	// only new ones keeps span and error counts right.
	for _, update := range summarizeByTrace(stored) {
		if err := s.spans.UpsertTraceSummary(ctx, update); err != nil {
			return 0, "", utils.WrapError(503, "storage_unavailable", "Could not update trace summary", err)
		}
	}
	return rejected, reason, nil
}

func summarizeByTrace(spans []entity.Span) []interfaces.TraceSummaryUpdate {
	byTrace := map[string]*interfaces.TraceSummaryUpdate{}
	services := map[string]map[string]bool{}
	order := []string{}
	for _, span := range spans {
		update, ok := byTrace[span.TraceID]
		if !ok {
			update = &interfaces.TraceSummaryUpdate{
				AccountID: span.AccountID, ProjectID: span.ProjectID, TraceID: span.TraceID,
				Start: span.StartTime, End: span.EndTime, Root: span,
			}
			byTrace[span.TraceID] = update
			services[span.TraceID] = map[string]bool{}
			order = append(order, span.TraceID)
		}
		update.SpanCount++
		if span.StatusCode == "ERROR" {
			update.ErrorCount++
		}
		if span.StartTime.Before(update.Start) {
			update.Start = span.StartTime
		}
		if span.EndTime.After(update.End) {
			update.End = span.EndTime
		}
		services[span.TraceID][span.ServiceName] = true
		isRoot := span.ParentSpanID == ""
		switch {
		case isRoot && !update.RootIsTrue:
			update.Root, update.RootIsTrue = span, true
		case !isRoot && !update.RootIsTrue && span.StartTime.Before(update.Root.StartTime):
			update.Root = span
		}
	}
	updates := make([]interfaces.TraceSummaryUpdate, 0, len(order))
	for _, traceID := range order {
		update := byTrace[traceID]
		for name := range services[traceID] {
			update.Services = append(update.Services, name)
		}
		sort.Strings(update.Services)
		updates = append(updates, *update)
	}
	return updates
}

// Search lists a project's traces, newest first, with cursor pagination.
func (s *TraceService) Search(ctx context.Context, accountID, projectID, userID string, request dtos.SearchTracesRequest) (dtos.ListTracesResponse, error) {
	if err := requireProjectAccess(ctx, s.accounts, s.projects, accountID, projectID, userID); err != nil {
		return dtos.ListTracesResponse{}, err
	}
	query, err := validation.ParseTraceSearch(request)
	if err != nil {
		return dtos.ListTracesResponse{}, err
	}
	query.AccountID, query.ProjectID = accountID, projectID
	fingerprint := traceQueryFingerprint(query)
	if request.Cursor != "" {
		after, err := decodeTraceCursor(request.Cursor, fingerprint)
		if err != nil {
			return dtos.ListTracesResponse{}, utils.NewError(400, "invalid_cursor", "cursor is invalid or was created for different filters")
		}
		query.After = after
	}
	pageSize := query.Limit
	query.Limit = pageSize + 1
	traces, err := s.spans.SearchTraces(ctx, query)
	if err != nil {
		return dtos.ListTracesResponse{}, utils.WrapError(503, "storage_unavailable", "Could not load traces", err)
	}
	response := dtos.ListTracesResponse{}
	if len(traces) > pageSize {
		traces = traces[:pageSize]
		response.HasMore = true
		last := traces[len(traces)-1]
		response.NextCursor = encodeTraceCursor(fingerprint, last)
	}
	response.Traces = make([]dtos.TraceSummaryResponse, 0, len(traces))
	for _, trace := range traces {
		response.Traces = append(response.Traces, dtos.TraceSummaryResponse{
			TraceID: trace.TraceID, RootName: trace.RootName, RootService: trace.RootService,
			RootStatus: trace.RootStatus, HTTPStatusCode: trace.RootHTTPStatus, Partial: !trace.HasRoot,
			StartTime: trace.StartTime, DurationMs: milliseconds(trace.EndTime.Sub(trace.StartTime)),
			SpanCount: trace.SpanCount, ErrorCount: trace.ErrorCount, Services: trace.Services,
		})
	}
	return response, nil
}

// Get returns one trace as waterfall rows plus the error events linked to it.
func (s *TraceService) Get(ctx context.Context, accountID, projectID, traceID, userID string) (dtos.TraceResponse, error) {
	if err := requireProjectAccess(ctx, s.accounts, s.projects, accountID, projectID, userID); err != nil {
		return dtos.TraceResponse{}, err
	}
	traceID = strings.ToLower(traceID)
	if !validation.IsTraceID(traceID) {
		return dtos.TraceResponse{}, utils.NewError(404, "trace_not_found", "Trace was not found in this project")
	}
	spans, err := s.spans.FindTrace(ctx, accountID, projectID, traceID, maxSpansPerTrace+1)
	if err != nil {
		return dtos.TraceResponse{}, utils.WrapError(503, "storage_unavailable", "Could not load trace", err)
	}
	if len(spans) == 0 {
		return dtos.TraceResponse{}, utils.NewError(404, "trace_not_found", "Trace was not found in this project")
	}
	response := dtos.TraceResponse{TraceID: traceID}
	if len(spans) > maxSpansPerTrace {
		spans, response.Truncated = spans[:maxSpansPerTrace], true
	}
	response.StartTime, response.DurationMs, response.Spans = buildWaterfall(spans)
	services := map[string]bool{}
	for _, span := range spans {
		services[span.ServiceName] = true
		if span.StatusCode == "ERROR" {
			response.ErrorCount++
		}
	}
	for name := range services {
		response.Services = append(response.Services, name)
	}
	sort.Strings(response.Services)
	response.SpanCount = len(spans)

	errorIDs, err := s.errors.ErrorIDsForTrace(ctx, accountID, projectID, traceID, maxErrorsPerTrace)
	if err != nil {
		return dtos.TraceResponse{}, utils.WrapError(503, "storage_unavailable", "Could not load the trace's errors", err)
	}
	events, err := s.errors.Search(ctx, interfaces.ErrorQuery{
		AccountID: accountID, ProjectIDs: []string{projectID}, TraceID: traceID, TraceErrorIDs: errorIDs,
		SortField: interfaces.ErrorSortReceivedAt, SortDesc: true, Limit: maxErrorsPerTrace,
	})
	if err != nil {
		return dtos.TraceResponse{}, utils.WrapError(503, "storage_unavailable", "Could not load the trace's errors", err)
	}
	response.Errors = make([]dtos.ErrorEventResponse, 0, len(events))
	for _, event := range events {
		response.Errors = append(response.Errors, mapper.EntityToErrorEventResponse(event))
	}
	return response, nil
}

// buildWaterfall orders spans parent-first (children by start time) and
// computes each span's offset from the trace start and its depth.
func buildWaterfall(spans []entity.Span) (time.Time, float64, []dtos.WaterfallSpanResponse) {
	sorted := slices.Clone(spans)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].StartTime.Equal(sorted[j].StartTime) {
			return sorted[i].StartTime.Before(sorted[j].StartTime)
		}
		return sorted[i].SpanID < sorted[j].SpanID
	})
	start, end := sorted[0].StartTime, sorted[0].EndTime
	byID := make(map[string]bool, len(sorted))
	for _, span := range sorted {
		byID[span.SpanID] = true
		if span.EndTime.After(end) {
			end = span.EndTime
		}
	}
	children := map[string][]int{}
	var roots []int
	for i, span := range sorted {
		if span.ParentSpanID != "" && byID[span.ParentSpanID] && span.ParentSpanID != span.SpanID {
			children[span.ParentSpanID] = append(children[span.ParentSpanID], i)
		} else {
			roots = append(roots, i)
		}
	}
	rows := make([]dtos.WaterfallSpanResponse, 0, len(sorted))
	visited := make([]bool, len(sorted))
	var walk func(index, depth int)
	walk = func(index, depth int) {
		if visited[index] {
			return
		}
		visited[index] = true
		span := sorted[index]
		rows = append(rows, waterfallRow(span, start, depth, span.ParentSpanID != "" && !byID[span.ParentSpanID]))
		for _, child := range children[span.SpanID] {
			walk(child, depth+1)
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	for i := range sorted { // spans caught in a parent cycle
		if !visited[i] {
			walk(i, 0)
		}
	}
	return start, milliseconds(end.Sub(start)), rows
}

func waterfallRow(span entity.Span, traceStart time.Time, depth int, orphan bool) dtos.WaterfallSpanResponse {
	row := dtos.WaterfallSpanResponse{
		SpanID: span.SpanID, ParentSpanID: span.ParentSpanID, Name: span.Name, Kind: span.Kind,
		ServiceName: span.ServiceName, StartTime: span.StartTime,
		OffsetMs: milliseconds(span.StartTime.Sub(traceStart)), DurationMs: float64(span.DurationNs) / 1e6,
		Depth: depth, StatusCode: span.StatusCode, StatusMessage: span.StatusMessage,
		Attributes: attributeMap(span.Attributes), Resource: attributeMap(span.ResourceAttributes), Orphan: orphan,
	}
	for _, event := range span.Events {
		row.Events = append(row.Events, dtos.SpanEventResponse{
			Name: event.Name, OffsetMs: milliseconds(event.Time.Sub(traceStart)), Attributes: attributeMap(event.Attributes),
		})
	}
	return row
}

func attributeMap(attributes []entity.Attribute) map[string]string {
	if len(attributes) == 0 {
		return nil
	}
	values := make(map[string]string, len(attributes))
	for _, attribute := range attributes {
		values[attribute.Key] = attribute.Value
	}
	return values
}

func milliseconds(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

type traceCursorToken struct {
	Query string `json:"q"`
	Start string `json:"t"`
	ID    string `json:"id"`
}

func traceQueryFingerprint(query interfaces.TraceQuery) string {
	services := slices.Clone(query.Services)
	slices.Sort(services)
	format := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.UTC().Format(time.RFC3339Nano)
	}
	canonical, _ := json.Marshal([]any{query.AccountID, query.ProjectID, services, query.Text, query.ErrorsOnly, format(query.From), format(query.To)})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:8])
}

func encodeTraceCursor(fingerprint string, last entity.TraceSummary) string {
	token, _ := json.Marshal(traceCursorToken{Query: fingerprint, Start: last.StartTime.UTC().Format(time.RFC3339Nano), ID: last.TraceID})
	return base64.RawURLEncoding.EncodeToString(token)
}

func decodeTraceCursor(raw, fingerprint string) (*interfaces.TraceCursor, error) {
	if len(raw) > maxCursorLength {
		return nil, errInvalidCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, errInvalidCursor
	}
	var token traceCursorToken
	if err := json.Unmarshal(data, &token); err != nil || token.Query != fingerprint || !validation.IsTraceID(token.ID) {
		return nil, errInvalidCursor
	}
	start, err := time.Parse(time.RFC3339Nano, token.Start)
	if err != nil {
		return nil, errors.Join(errInvalidCursor, err)
	}
	return &interfaces.TraceCursor{StartTime: start.UTC(), TraceID: token.ID}, nil
}
