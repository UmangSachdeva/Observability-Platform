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

type accountRepository struct {
	mongo      interfaces.MongoLayer
	collection string
}

var _ interfaces.AccountRepository = (*accountRepository)(nil)

func NewAccountRepository(mongo interfaces.MongoLayer, collection string) interfaces.AccountRepository {
	return &accountRepository{mongo: mongo, collection: collection}
}

func (r *accountRepository) Create(ctx context.Context, account entity.Account) error {
	return r.mongo.InsertOne(ctx, r.collection, account)
}

func (r *accountRepository) DeleteByID(ctx context.Context, id string) error {
	deleted, err := r.mongo.DeleteOne(ctx, r.collection, bson.D{{Key: "id", Value: id}})
	if err != nil {
		return err
	}
	if deleted == 0 {
		return interfaces.ErrAccountNotFound
	}
	return nil
}

func (r *accountRepository) FindByID(ctx context.Context, id string) (entity.Account, error) {
	var account entity.Account
	err := r.mongo.FindOne(ctx, r.collection, bson.D{{Key: "id", Value: id}}, &account)
	if errors.Is(err, driver.ErrNoDocuments) {
		return entity.Account{}, interfaces.ErrAccountNotFound
	}
	if err != nil {
		return entity.Account{}, err
	}
	return account, nil
}

func (r *accountRepository) FindDefaultForUser(ctx context.Context, userID string) (entity.Account, error) {
	var accounts []entity.Account
	adminFilter := bson.D{{Key: "members", Value: bson.D{{Key: "$elemMatch", Value: bson.D{
		{Key: "user_id", Value: userID},
		{Key: "role", Value: bson.D{{Key: "$in", Value: bson.A{"admin", "owner"}}}},
	}}}}}
	err := r.mongo.Find(ctx, r.collection, adminFilter, bson.D{{Key: "created_at", Value: 1}}, 1, &accounts)
	if err != nil {
		return entity.Account{}, err
	}
	if len(accounts) > 0 {
		return accounts[0], nil
	}
	accounts = nil
	err = r.mongo.Find(ctx, r.collection,
		bson.D{{Key: "members.user_id", Value: userID}},
		bson.D{{Key: "created_at", Value: 1}}, 1, &accounts)
	if err != nil {
		return entity.Account{}, err
	}
	if len(accounts) == 0 {
		return entity.Account{}, interfaces.ErrAccountNotFound
	}
	return accounts[0], nil
}

func (r *accountRepository) IsMember(ctx context.Context, accountID, userID string) (bool, error) {
	var account entity.Account
	err := r.mongo.FindOne(ctx, r.collection, bson.D{
		{Key: "id", Value: accountID},
		{Key: "members.user_id", Value: userID},
	}, &account)
	if err != nil {
		if errors.Is(err, driver.ErrNoDocuments) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *accountRepository) AddMember(ctx context.Context, accountID string, member entity.AccountMember) error {
	modified, err := r.mongo.UpdateOne(ctx, r.collection,
		bson.D{
			{Key: "id", Value: accountID},
			{Key: "members.user_id", Value: bson.D{{Key: "$ne", Value: member.UserID}}},
		},
		bson.D{
			{Key: "$push", Value: bson.D{{Key: "members", Value: member}}},
			{Key: "$set", Value: bson.D{{Key: "updated_at", Value: time.Now().UTC()}}},
		},
	)
	if err != nil {
		return err
	}
	if modified == 0 {
		isMember, err := r.IsMember(ctx, accountID, member.UserID)
		if err != nil {
			return err
		}
		if isMember {
			return nil
		}
		return interfaces.ErrAccountNotFound
	}
	return nil
}

var accountAdminRoles = bson.A{"admin", "owner"}

// RemoveMember removes userID from the account in a single atomic update.
// A non-admin member can always leave. An admin can leave only while another
// admin remains, so the account is never left without an admin even when two
// admins leave at the same time.
func (r *accountRepository) RemoveMember(ctx context.Context, accountID, userID string) error {
	filter := bson.D{
		{Key: "id", Value: accountID},
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "members", Value: bson.D{{Key: "$elemMatch", Value: bson.D{
				{Key: "user_id", Value: userID},
				{Key: "role", Value: bson.D{{Key: "$nin", Value: accountAdminRoles}}},
			}}}}},
			bson.D{{Key: "$and", Value: bson.A{
				bson.D{{Key: "members.user_id", Value: userID}},
				bson.D{{Key: "members", Value: bson.D{{Key: "$elemMatch", Value: bson.D{
					{Key: "user_id", Value: bson.D{{Key: "$ne", Value: userID}}},
					{Key: "role", Value: bson.D{{Key: "$in", Value: accountAdminRoles}}},
				}}}}},
			}}},
		}},
	}
	update := bson.D{
		{Key: "$pull", Value: bson.D{{Key: "members", Value: bson.D{{Key: "user_id", Value: userID}}}}},
		{Key: "$set", Value: bson.D{{Key: "updated_at", Value: time.Now().UTC()}}},
	}
	modified, err := r.mongo.UpdateOne(ctx, r.collection, filter, update)
	if err != nil {
		return err
	}
	if modified > 0 {
		return nil
	}
	account, err := r.FindByID(ctx, accountID)
	if err != nil {
		return err
	}
	for _, member := range account.Members {
		if member.UserID == userID {
			return interfaces.ErrLastAccountAdmin
		}
	}
	return interfaces.ErrNotAccountMember
}
