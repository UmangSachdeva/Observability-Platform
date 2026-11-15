package mongo

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"observability-platform/internal/repository/interfaces"
)

var tracer = otel.Tracer("observability-platform/mongo")

// startSpan starts a CLIENT span for one MongoDB command, but only inside an
// existing trace. Commands without a parent (startup, OTLP ingestion, which
// is not traced to avoid the API tracing its own span exports) stay untraced.
func startSpan(ctx context.Context, operation, collection string) (context.Context, trace.Span) {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ctx, trace.SpanFromContext(ctx)
	}
	return tracer.Start(ctx, "mongo "+operation+" "+collection,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system.name", "mongodb"),
			attribute.String("db.operation.name", operation),
			attribute.String("db.collection.name", collection),
		))
}

func endSpan(span trace.Span, err error) {
	if err != nil && !errors.Is(err, driver.ErrNoDocuments) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

type Client struct {
	client   *driver.Client
	database string
}

var _ interfaces.MongoLayer = (*Client)(nil)

func Connect(ctx context.Context, uri, database string) (*Client, error) {
	client, err := driver.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("create MongoDB client: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("ping MongoDB: %w", err)
	}
	return &Client{client: client, database: database}, nil
}

func (c *Client) InsertOne(ctx context.Context, collection string, document any) (err error) {
	ctx, span := startSpan(ctx, "insert", collection)
	defer func() { endSpan(span, err) }()
	if _, err := c.client.Database(c.database).Collection(collection).InsertOne(ctx, document); err != nil {
		return fmt.Errorf("insert document into %q: %w", collection, err)
	}
	return nil
}

func (c *Client) InsertMany(ctx context.Context, collection string, documents []any) (err error) {
	ctx, span := startSpan(ctx, "insert", collection)
	defer func() { endSpan(span, err) }()
	if len(documents) == 0 {
		return nil
	}
	_, err = c.client.Database(c.database).Collection(collection).InsertMany(ctx, documents, options.InsertMany().SetOrdered(false))
	if err != nil {
		return fmt.Errorf("insert documents into %q: %w", collection, err)
	}
	return nil
}

func (c *Client) UpsertOne(ctx context.Context, collection string, filter any, update any) (err error) {
	ctx, span := startSpan(ctx, "update", collection)
	defer func() { endSpan(span, err) }()
	_, err = c.client.Database(c.database).Collection(collection).UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("upsert document in %q: %w", collection, err)
	}
	return nil
}

func (c *Client) FindOne(ctx context.Context, collection string, filter any, result any) (err error) {
	ctx, span := startSpan(ctx, "find", collection)
	defer func() { endSpan(span, err) }()
	if err := c.client.Database(c.database).Collection(collection).FindOne(ctx, filter).Decode(result); err != nil {
		return fmt.Errorf("find one document in %q: %w", collection, err)
	}
	return nil
}

func (c *Client) Find(ctx context.Context, collection string, filter any, sort bson.D, limit int64, result any) (err error) {
	ctx, span := startSpan(ctx, "find", collection)
	defer func() { endSpan(span, err) }()
	findOptions := options.Find().SetSort(sort).SetLimit(limit)
	cursor, err := c.client.Database(c.database).Collection(collection).Find(ctx, filter, findOptions)
	if err != nil {
		return fmt.Errorf("find documents in %q: %w", collection, err)
	}
	defer cursor.Close(ctx)
	if err := cursor.All(ctx, result); err != nil {
		return fmt.Errorf("decode documents from %q: %w", collection, err)
	}
	return nil
}

func (c *Client) UpdateOne(ctx context.Context, collection string, filter any, update any) (modified int64, err error) {
	ctx, span := startSpan(ctx, "update", collection)
	defer func() { endSpan(span, err) }()
	result, err := c.client.Database(c.database).Collection(collection).UpdateOne(ctx, filter, update)
	if err != nil {
		return 0, fmt.Errorf("update document in %q: %w", collection, err)
	}
	return result.ModifiedCount, nil
}

func (c *Client) DeleteOne(ctx context.Context, collection string, filter any) (deleted int64, err error) {
	ctx, span := startSpan(ctx, "delete", collection)
	defer func() { endSpan(span, err) }()
	result, err := c.client.Database(c.database).Collection(collection).DeleteOne(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("delete document from %q: %w", collection, err)
	}
	return result.DeletedCount, nil
}

func (c *Client) CreateIndex(ctx context.Context, collection string, keys bson.D, unique bool) error {
	_, err := c.client.Database(c.database).Collection(collection).Indexes().CreateOne(ctx, driver.IndexModel{
		Keys: keys, Options: options.Index().SetUnique(unique),
	})
	if err != nil {
		return fmt.Errorf("create index on %q: %w", collection, err)
	}
	return nil
}

func (c *Client) Close(ctx context.Context) error {
	return c.client.Disconnect(ctx)
}
