package mongo

import (
	"context"
	"regexp"

	"go.mongodb.org/mongo-driver/v2/bson"

	"observability-platform/internal/entity"
	"observability-platform/internal/repository/interfaces"
)

type errorRepository struct {
	mongo      interfaces.MongoLayer
	collection string
}

var _ interfaces.ErrorRepository = (*errorRepository)(nil)

func NewErrorRepository(mongo interfaces.MongoLayer, collection string) interfaces.ErrorRepository {
	return &errorRepository{mongo: mongo, collection: collection}
}

func (r *errorRepository) Create(ctx context.Context, event entity.ErrorEvent) error {
	return r.mongo.InsertOne(ctx, r.collection, event)
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
