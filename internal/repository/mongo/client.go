package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"observability-platform/internal/repository/interfaces"
)

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

func (c *Client) InsertOne(ctx context.Context, collection string, document any) error {
	if _, err := c.client.Database(c.database).Collection(collection).InsertOne(ctx, document); err != nil {
		return fmt.Errorf("insert document into %q: %w", collection, err)
	}
	return nil
}

func (c *Client) FindOne(ctx context.Context, collection string, filter any, result any) error {
	if err := c.client.Database(c.database).Collection(collection).FindOne(ctx, filter).Decode(result); err != nil {
		return fmt.Errorf("find one document in %q: %w", collection, err)
	}
	return nil
}

func (c *Client) Find(ctx context.Context, collection string, filter any, sort bson.D, limit int64, result any) error {
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

func (c *Client) UpdateOne(ctx context.Context, collection string, filter any, update any) (int64, error) {
	result, err := c.client.Database(c.database).Collection(collection).UpdateOne(ctx, filter, update)
	if err != nil {
		return 0, fmt.Errorf("update document in %q: %w", collection, err)
	}
	return result.ModifiedCount, nil
}

func (c *Client) DeleteOne(ctx context.Context, collection string, filter any) (int64, error) {
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
