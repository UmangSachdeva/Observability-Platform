package mongo

import (
	"context"
	"errors"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"

	"observability-platform/internal/entity"
	"observability-platform/internal/repository/interfaces"
)

type spanRepository struct {
	mongo  interfaces.MongoLayer
	spans  string
	traces string
}

var _ interfaces.SpanRepository = (*spanRepository)(nil)

func NewSpanRepository(mongo interfaces.MongoLayer, spansCollection, tracesCollection string) interfaces.SpanRepository {
	return &spanRepository{mongo: mongo, spans: spansCollection, traces: tracesCollection}
}

const duplicateKeyCode = 11000

func (r *spanRepository) InsertSpans(ctx context.Context, spans []entity.Span) ([]entity.Span, error) {
	documents := make([]any, len(spans))
	for i := range spans {
		documents[i] = spans[i]
	}
	err := r.mongo.InsertMany(ctx, r.spans, documents)
	if err == nil {
		return spans, nil
	}
	// Exporters retry failed batches. The unique (project_id, trace_id,
	// span_id) index rejects spans already stored while the rest still insert
	// (unordered); any other failure is a real error.
	var bulk driver.BulkWriteException
	if !errors.As(err, &bulk) || bulk.WriteConcernError != nil || len(bulk.WriteErrors) == 0 {
		return nil, err
	}
	skipped := make(map[int]bool, len(bulk.WriteErrors))
	for _, writeError := range bulk.WriteErrors {
		if writeError.Code != duplicateKeyCode {
			return nil, err
		}
		skipped[writeError.Index] = true
	}
	stored := make([]entity.Span, 0, len(spans)-len(skipped))
	for i, span := range spans {
		if !skipped[i] {
			stored = append(stored, span)
		}
	}
	return stored, nil
}

func (r *spanRepository) UpsertTraceSummary(ctx context.Context, u interfaces.TraceSummaryUpdate) error {
	rootFields := bson.D{
		{Key: "root_name", Value: u.Root.Name},
		{Key: "root_service", Value: u.Root.ServiceName},
		{Key: "root_status", Value: u.Root.StatusCode},
		{Key: "root_http_status", Value: attributeValue(u.Root.Attributes, "http.response.status_code")},
		{Key: "has_root", Value: u.RootIsTrue},
	}
	set := bson.D{{Key: "updated_at", Value: time.Now().UTC()}}
	setOnInsert := bson.D{{Key: "account_id", Value: u.AccountID}}
	if u.RootIsTrue {
		set = append(set, rootFields...)
	} else {
		setOnInsert = append(setOnInsert, rootFields...)
	}
	update := bson.D{
		{Key: "$set", Value: set},
		{Key: "$setOnInsert", Value: setOnInsert},
		{Key: "$min", Value: bson.D{{Key: "start_time", Value: u.Start}}},
		{Key: "$max", Value: bson.D{{Key: "end_time", Value: u.End}}},
		{Key: "$inc", Value: bson.D{{Key: "span_count", Value: u.SpanCount}, {Key: "error_count", Value: u.ErrorCount}}},
		{Key: "$addToSet", Value: bson.D{{Key: "services", Value: bson.D{{Key: "$each", Value: u.Services}}}}},
	}
	filter := bson.D{{Key: "project_id", Value: u.ProjectID}, {Key: "trace_id", Value: u.TraceID}}
	return r.mongo.UpsertOne(ctx, r.traces, filter, update)
}

func (r *spanRepository) SearchTraces(ctx context.Context, query interfaces.TraceQuery) ([]entity.TraceSummary, error) {
	conditions := bson.A{
		bson.D{{Key: "account_id", Value: query.AccountID}},
		bson.D{{Key: "project_id", Value: query.ProjectID}},
	}
	if len(query.Services) > 0 {
		conditions = append(conditions, bson.D{{Key: "services", Value: bson.D{{Key: "$in", Value: query.Services}}}})
	}
	if query.ErrorsOnly {
		conditions = append(conditions, bson.D{{Key: "error_count", Value: bson.D{{Key: "$gt", Value: 0}}}})
	}
	if query.Text != "" {
		conditions = append(conditions, bson.D{{Key: "root_name", Value: bson.D{
			{Key: "$regex", Value: regexp.QuoteMeta(query.Text)}, {Key: "$options", Value: "i"},
		}}})
	}
	if query.From != nil || query.To != nil {
		timeRange := bson.D{}
		if query.From != nil {
			timeRange = append(timeRange, bson.E{Key: "$gte", Value: *query.From})
		}
		if query.To != nil {
			timeRange = append(timeRange, bson.E{Key: "$lte", Value: *query.To})
		}
		conditions = append(conditions, bson.D{{Key: "start_time", Value: timeRange}})
	}
	if query.After != nil {
		conditions = append(conditions, bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "start_time", Value: bson.D{{Key: "$lt", Value: query.After.StartTime}}}},
			bson.D{{Key: "start_time", Value: query.After.StartTime}, {Key: "trace_id", Value: bson.D{{Key: "$lt", Value: query.After.TraceID}}}},
		}}})
	}
	traces := make([]entity.TraceSummary, 0, query.Limit)
	sort := bson.D{{Key: "start_time", Value: -1}, {Key: "trace_id", Value: -1}}
	if err := r.mongo.Find(ctx, r.traces, bson.D{{Key: "$and", Value: conditions}}, sort, int64(query.Limit), &traces); err != nil {
		return nil, err
	}
	return traces, nil
}

func (r *spanRepository) FindTrace(ctx context.Context, accountID, projectID, traceID string, limit int) ([]entity.Span, error) {
	filter := bson.D{{Key: "account_id", Value: accountID}, {Key: "project_id", Value: projectID}, {Key: "trace_id", Value: traceID}}
	spans := make([]entity.Span, 0, 64)
	sort := bson.D{{Key: "start_time", Value: 1}, {Key: "span_id", Value: 1}}
	if err := r.mongo.Find(ctx, r.spans, filter, sort, int64(limit), &spans); err != nil {
		return nil, err
	}
	return spans, nil
}

func attributeValue(attributes []entity.Attribute, key string) string {
	for _, attribute := range attributes {
		if attribute.Key == key {
			return attribute.Value
		}
	}
	return ""
}
