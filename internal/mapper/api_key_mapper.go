package mapper

import (
	"time"

	"observability-platform/internal/dtos"
	"observability-platform/internal/entity"
)

func CreateAPIKeyRequestToEntity(request dtos.CreateAPIKeyRequest, accessKey, userID, accountID, projectID, encryptedSecret string) entity.ClientCredential {
	return entity.ClientCredential{
		AccessKey: accessKey, UserID: userID, AccountID: accountID, ProjectID: projectID, ClientName: request.ClientName,
		EncryptedSecret: encryptedSecret, CreatedAt: time.Now().UTC(),
	}
}

func APIKeyToResponse(credential entity.ClientCredential) dtos.APIKeyResponse {
	return dtos.APIKeyResponse{
		AccessKey: credential.AccessKey, ClientName: credential.ClientName,
		AccountID: credential.AccountID, ProjectID: credential.ProjectID, CreatedAt: credential.CreatedAt,
	}
}
