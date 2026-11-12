package mapper

import (
	"time"

	"observability-platform/internal/dtos"
	"observability-platform/internal/entity"
)

func CreateErrorRequestToEntity(request dtos.CreateErrorRequest, id, accountID, projectID string, receivedAt time.Time) entity.ErrorEvent {
	return entity.ErrorEvent{
		ID: id, AccountID: accountID, ProjectID: projectID, Timestamp: request.Timestamp, ReceivedAt: receivedAt,
		Service: request.Service, Environment: request.Environment, Level: request.Level,
		Message: request.Message, StackTrace: request.StackTrace, Attributes: request.Attributes,
	}
}

func EntityToErrorEventResponse(event entity.ErrorEvent) dtos.ErrorEventResponse {
	return dtos.ErrorEventResponse{
		ID: event.ID, ProjectID: event.ProjectID, Timestamp: event.Timestamp, ReceivedAt: event.ReceivedAt,
		Service: event.Service, Environment: event.Environment, Level: event.Level,
		Message: event.Message, StackTrace: event.StackTrace, Attributes: event.Attributes,
	}
}
