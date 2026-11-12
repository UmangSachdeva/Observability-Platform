package mongo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"

	"observability-platform/internal/entity"
	"observability-platform/internal/repository/interfaces"
)

type inviteRepository struct {
	mongo      interfaces.MongoLayer
	collection string
}

var _ interfaces.InviteRepository = (*inviteRepository)(nil)

func NewInviteRepository(mongo interfaces.MongoLayer, collection string) interfaces.InviteRepository {
	return &inviteRepository{mongo: mongo, collection: collection}
}

func (r *inviteRepository) Create(ctx context.Context, invite entity.AccountInvite) error {
	return r.mongo.InsertOne(ctx, r.collection, invite)
}

func (r *inviteRepository) FindByTokenHash(ctx context.Context, tokenHash string) (entity.AccountInvite, error) {
	var invite entity.AccountInvite
	err := r.mongo.FindOne(ctx, r.collection, bson.D{{Key: "token_hash", Value: tokenHash}}, &invite)
	if errors.Is(err, driver.ErrNoDocuments) {
		return entity.AccountInvite{}, interfaces.ErrInviteNotFound
	}
	if err != nil {
		return entity.AccountInvite{}, err
	}
	return invite, nil
}

func (r *inviteRepository) MarkAccepted(ctx context.Context, tokenHash string, acceptedAt time.Time) (bool, error) {
	modified, err := r.mongo.UpdateOne(ctx, r.collection,
		bson.D{{Key: "token_hash", Value: tokenHash}, {Key: "accepted_at", Value: nil}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "accepted_at", Value: acceptedAt}}}},
	)
	if err != nil {
		return false, err
	}
	return modified > 0, nil
}

func (r *inviteRepository) FindByID(ctx context.Context, id string) (entity.AccountInvite, error) {
	var invite entity.AccountInvite
	err := r.mongo.FindOne(ctx, r.collection, bson.D{{Key: "id", Value: id}}, &invite)
	if errors.Is(err, driver.ErrNoDocuments) {
		return entity.AccountInvite{}, interfaces.ErrInviteNotFound
	}
	if err != nil {
		return entity.AccountInvite{}, err
	}
	return invite, nil
}

func (r *inviteRepository) ListPendingByEmail(ctx context.Context, email string, now time.Time, includeSnoozed bool, limit int) ([]entity.AccountInvite, error) {
	filter := bson.D{
		{Key: "email", Value: email},
		{Key: "accepted_at", Value: nil},
		{Key: "expires_at", Value: bson.D{{Key: "$gt", Value: now}}},
	}
	if !includeSnoozed {
		filter = append(filter, bson.E{Key: "$or", Value: bson.A{
			bson.D{{Key: "snoozed_until", Value: nil}},
			bson.D{{Key: "snoozed_until", Value: bson.D{{Key: "$lte", Value: now}}}},
		}})
	}
	invites := make([]entity.AccountInvite, 0, limit)
	err := r.mongo.Find(ctx, r.collection, filter, bson.D{{Key: "created_at", Value: -1}}, int64(limit), &invites)
	if err != nil {
		return nil, err
	}
	return invites, nil
}

func (r *inviteRepository) SetSnooze(ctx context.Context, id string, snoozedUntil *time.Time) (bool, error) {
	update := bson.D{{Key: "$unset", Value: bson.D{{Key: "snoozed_until", Value: ""}}}}
	if snoozedUntil != nil {
		update = bson.D{{Key: "$set", Value: bson.D{{Key: "snoozed_until", Value: *snoozedUntil}}}}
	}
	modified, err := r.mongo.UpdateOne(ctx, r.collection,
		bson.D{{Key: "id", Value: id}, {Key: "accepted_at", Value: nil}}, update)
	if err != nil {
		return false, err
	}
	return modified > 0, nil
}
