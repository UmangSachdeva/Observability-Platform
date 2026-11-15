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
		TraceID: request.TraceID, SpanID: request.SpanID,
	}
}

func EntityToErrorEventResponse(event entity.ErrorEvent) dtos.ErrorEventResponse {
	count, firstSeen := event.OccurrenceCount, event.FirstSeen
	if count == 0 { // stored before grouping: one occurrence
		count = 1
	}
	if firstSeen.IsZero() {
		firstSeen = event.Timestamp
	}
	return dtos.ErrorEventResponse{
		ID: event.ID, OccurrenceCount: count, FirstSeen: firstSeen, ProjectID: event.ProjectID, Timestamp: event.Timestamp, ReceivedAt: event.ReceivedAt,
		Service: event.Service, Environment: event.Environment, Level: event.Level,
		Message: event.Message, StackTrace: event.StackTrace, Attributes: event.Attributes,
		TraceID: event.TraceID, SpanID: event.SpanID,
	}
}

func OccurrenceToResponse(occurrence entity.ErrorOccurrence) dtos.OccurrenceResponse {
	return dtos.OccurrenceResponse{
		ID: occurrence.ID, Timestamp: occurrence.Timestamp, ReceivedAt: occurrence.ReceivedAt,
		TraceID: occurrence.TraceID, SpanID: occurrence.SpanID, Attributes: occurrence.Attributes,
	}
}
