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

type errorRepository struct {
	mongo       interfaces.MongoLayer
	collection  string
	occurrences string
}

var _ interfaces.ErrorRepository = (*errorRepository)(nil)

func NewErrorRepository(mongo interfaces.MongoLayer, collection, occurrencesCollection string) interfaces.ErrorRepository {
	return &errorRepository{mongo: mongo, collection: collection, occurrences: occurrencesCollection}
}

func (r *errorRepository) RecordOccurrence(ctx context.Context, event entity.ErrorEvent, occurrence entity.ErrorOccurrence) (bool, error) {
	// The unique (project_id, error_id, key) index turns a retried or
	// replayed delivery into a duplicate-key error: nothing is counted twice.
	if err := r.mongo.InsertOne(ctx, r.occurrences, occurrence); err != nil {
		if driver.IsDuplicateKeyError(err) {
			return false, nil
		}
		return false, err
	}
	if err := r.upsertError(ctx, event, occurrence); err != nil {
		// Remove the occurrence again so a retry of this request is counted.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = r.mongo.DeleteOne(cleanupCtx, r.occurrences, bson.D{{Key: "project_id", Value: occurrence.ProjectID}, {Key: "id", Value: occurrence.ID}})
		return false, err
	}
	return true, nil
}

func (r *errorRepository) upsertError(ctx context.Context, event entity.ErrorEvent, occurrence entity.ErrorOccurrence) error {
	filter := bson.D{{Key: "project_id", Value: event.ProjectID}, {Key: "id", Value: event.ID}}
	update := bson.D{
		{Key: "$setOnInsert", Value: bson.D{
			{Key: "account_id", Value: event.AccountID},
			{Key: "fingerprint", Value: event.Fingerprint},
			{Key: "service", Value: event.Service},
			{Key: "environment", Value: event.Environment},
			{Key: "level", Value: event.Level},
			{Key: "message", Value: event.Message},
			{Key: "stack_trace", Value: event.StackTrace},
		}},
		{Key: "$min", Value: bson.D{{Key: "first_seen", Value: occurrence.Timestamp}}},
		{Key: "$max", Value: bson.D{{Key: "received_at", Value: occurrence.ReceivedAt}, {Key: "timestamp", Value: occurrence.Timestamp}}},
		{Key: "$set", Value: bson.D{
			{Key: "attributes", Value: occurrence.Attributes},
			{Key: "trace_id", Value: occurrence.TraceID},
			{Key: "span_id", Value: occurrence.SpanID},
		}},
		{Key: "$inc", Value: bson.D{{Key: "occurrence_count", Value: 1}}},
	}
	err := r.mongo.UpsertOne(ctx, r.collection, filter, update)
	if err != nil && driver.IsDuplicateKeyError(err) {
		// Two first occurrences raced and the other one created the record;
		// this update now matches it.
		err = r.mongo.UpsertOne(ctx, r.collection, filter, update)
	}
	return err
}

func (r *errorRepository) ListOccurrences(ctx context.Context, accountID, projectID, errorID string, after *interfaces.OccurrenceCursor, limit int) ([]entity.ErrorOccurrence, error) {
	filter := bson.D{{Key: "account_id", Value: accountID}, {Key: "project_id", Value: projectID}, {Key: "error_id", Value: errorID}}
	if after != nil {
		filter = append(filter, bson.E{Key: "$or", Value: bson.A{
			bson.D{{Key: "received_at", Value: bson.D{{Key: "$lt", Value: after.ReceivedAt}}}},
			bson.D{{Key: "received_at", Value: after.ReceivedAt}, {Key: "id", Value: bson.D{{Key: "$lt", Value: after.ID}}}},
		}})
	}
	occurrences := make([]entity.ErrorOccurrence, 0, limit)
	sort := bson.D{{Key: "received_at", Value: -1}, {Key: "id", Value: -1}}
	if err := r.mongo.Find(ctx, r.occurrences, filter, sort, int64(limit), &occurrences); err != nil {
		return nil, err
	}
	return occurrences, nil
}

func (r *errorRepository) ErrorIDsForTrace(ctx context.Context, accountID, projectID, traceID string, limit int) ([]string, error) {
	filter := bson.D{{Key: "account_id", Value: accountID}, {Key: "trace_id", Value: traceID}}
	if projectID != "" {
		filter = append(filter, bson.E{Key: "project_id", Value: projectID})
	}
	var occurrences []entity.ErrorOccurrence
	if err := r.mongo.Find(ctx, r.occurrences, filter, bson.D{{Key: "received_at", Value: -1}}, int64(limit), &occurrences); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, occurrence := range occurrences {
		if !seen[occurrence.ErrorID] {
			seen[occurrence.ErrorID] = true
			ids = append(ids, occurrence.ErrorID)
		}
	}
	return ids, nil
}

// searchableFields are matched by the free-text search, case-insensitively.
var searchableFields = []string{"message", "service", "environment", "stack_trace"}

func (r *errorRepository) Search(ctx context.Context, query interfaces.ErrorQuery) ([]entity.ErrorEvent, error) {
	conditions := bson.A{bson.D{{Key: "account_id", Value: query.AccountID}}}
	addIn := func(field string, values []string) {
		if len(values) > 0 {
			conditions = append(conditions, bson.D{{Key: field, Value: bson.D{{Key: "$in", Value: values}}}})
		}
	}
	addIn("project_id", query.ProjectIDs)
	addIn("service", query.Services)
	addIn("environment", query.Environments)
	addIn("level", query.Levels)
	if query.TraceID != "" {
		byTrace := bson.A{bson.D{{Key: "trace_id", Value: query.TraceID}}}
		if len(query.TraceErrorIDs) > 0 {
			byTrace = append(byTrace, bson.D{{Key: "id", Value: bson.D{{Key: "$in", Value: query.TraceErrorIDs}}}})
		}
		conditions = append(conditions, bson.D{{Key: "$or", Value: byTrace}})
	}
	for key, value := range query.Attributes {
		// Keys are validated to letters, digits, "_" and "-", so they cannot
		// form operators or nested paths.
		conditions = append(conditions, bson.D{{Key: "attributes." + key, Value: value}})
	}
	if query.From != nil || query.To != nil {
		timeRange := bson.D{}
		if query.From != nil {
			timeRange = append(timeRange, bson.E{Key: "$gte", Value: *query.From})
		}
		if query.To != nil {
			timeRange = append(timeRange, bson.E{Key: "$lte", Value: *query.To})
		}
		conditions = append(conditions, bson.D{{Key: "received_at", Value: timeRange}})
	}
	if query.Text != "" {
		pattern := bson.D{{Key: "$regex", Value: regexp.QuoteMeta(query.Text)}, {Key: "$options", Value: "i"}}
		anyField := make(bson.A, 0, len(searchableFields))
		for _, field := range searchableFields {
			anyField = append(anyField, bson.D{{Key: field, Value: pattern}})
		}
		conditions = append(conditions, bson.D{{Key: "$or", Value: anyField}})
	}

	field := string(query.SortField)
	direction, after := 1, "$gt"
	if query.SortDesc {
		direction, after = -1, "$lt"
	}
	if query.After != nil {
		// Keyset pagination: rows past the last sort value, or rows with the
		// same value whose id comes after the last id.
		conditions = append(conditions, bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: field, Value: bson.D{{Key: after, Value: query.After.Value}}}},
			bson.D{{Key: field, Value: query.After.Value}, {Key: "id", Value: bson.D{{Key: after, Value: query.After.ID}}}},
		}}})
	}

	filter := bson.D{{Key: "$and", Value: conditions}}
	sort := bson.D{{Key: field, Value: direction}, {Key: "id", Value: direction}}
	events := make([]entity.ErrorEvent, 0, query.Limit)
	if err := r.mongo.Find(ctx, r.collection, filter, sort, int64(query.Limit), &events); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *errorRepository) FindByID(ctx context.Context, accountID, projectID, id string) (entity.ErrorEvent, error) {
	var event entity.ErrorEvent
	filter := bson.D{{Key: "id", Value: id}, {Key: "account_id", Value: accountID}, {Key: "project_id", Value: projectID}}
	err := r.mongo.FindOne(ctx, r.collection, filter, &event)
	if errors.Is(err, driver.ErrNoDocuments) {
		return entity.ErrorEvent{}, interfaces.ErrErrorEventNotFound
	}
	if err != nil {
		return entity.ErrorEvent{}, err
	}
	return event, nil
}
