package controller

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"observability-platform/internal/dtos"
	"observability-platform/internal/mapper"
	"observability-platform/internal/middleware"
	"observability-platform/internal/service"
	"observability-platform/internal/utils"
)

const (
	maxOTLPBodyBytes    = 8 << 20
	maxOTLPDecodedBytes = 32 << 20
	contentTypeProtobuf = "application/x-protobuf"
	contentTypeJSON     = "application/json"
)

type TraceController struct {
	service *service.TraceService
	logger  *slog.Logger
}

func NewTraceController(traceService *service.TraceService, logger *slog.Logger) *TraceController {
	return &TraceController{service: traceService, logger: logger}
}

// RegisterIngestRoutes registers the OTLP receiver (API key headers).
func (c *TraceController) RegisterIngestRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/traces", c.ingest)
}

// RegisterUserRoutes registers trace reads (user bearer token).
func (c *TraceController) RegisterUserRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/accounts/{accountID}/projects/{projectID}/traces", c.list)
	mux.HandleFunc("GET /v1/accounts/{accountID}/projects/{projectID}/traces/{traceID}", c.get)
}

// ingest implements OTLP/HTTP for traces: protobuf or JSON body, optionally
// gzip-compressed, answered in the same encoding.
func (c *TraceController) ingest(w http.ResponseWriter, r *http.Request) {
	client, ok := middleware.ClientFromContext(r.Context())
	if !ok {
		utils.WriteError(w, service.ErrInvalidAPIKey)
		return
	}
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != contentTypeProtobuf && mediaType != contentTypeJSON {
		utils.WriteErrorResponse(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/x-protobuf or application/json")
		return
	}
	var reader io.Reader = http.MaxBytesReader(w, r.Body, maxOTLPBodyBytes)
	switch r.Header.Get("Content-Encoding") {
	case "", "identity":
	case "gzip":
		gz, err := gzip.NewReader(reader)
		if err != nil {
			utils.WriteErrorResponse(w, http.StatusBadRequest, "invalid_otlp", "Body is not valid gzip")
			return
		}
		defer gz.Close()
		reader = gz
	default:
		utils.WriteErrorResponse(w, http.StatusUnsupportedMediaType, "unsupported_encoding", "Content-Encoding must be gzip or left out")
		return
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxOTLPDecodedBytes+1))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) || len(data) > maxOTLPDecodedBytes {
		utils.WriteErrorResponse(w, http.StatusRequestEntityTooLarge, "request_too_large", "OTLP body exceeds 8 MiB (32 MiB uncompressed)")
		return
	}
	if err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "invalid_otlp", "Could not read the request body")
		return
	}
	request := &coltracepb.ExportTraceServiceRequest{}
	if mediaType == contentTypeProtobuf {
		err = proto.Unmarshal(data, request)
	} else if fixed, fixErr := mapper.FixOTLPJSONIDs(data); fixErr != nil {
		err = fixErr
	} else {
		err = protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(fixed, request)
	}
	if err != nil {
		utils.WriteErrorResponse(w, http.StatusBadRequest, "invalid_otlp", "Body is not a valid OTLP ExportTraceServiceRequest")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rejected, reason, err := c.service.Ingest(ctx, client, request)
	if err != nil {
		if status, _, _ := utils.ErrorResponse(err); status >= 500 {
			c.logger.Error("could not store spans", "error", err)
		}
		utils.WriteError(w, err)
		return
	}
	response := &coltracepb.ExportTraceServiceResponse{}
	if rejected > 0 {
		response.PartialSuccess = &coltracepb.ExportTracePartialSuccess{RejectedSpans: rejected, ErrorMessage: reason}
	}
	var body []byte
	if mediaType == contentTypeProtobuf {
		body, err = proto.Marshal(response)
	} else {
		body, err = protojson.Marshal(response)
	}
	if err != nil {
		utils.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", mediaType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (c *TraceController) list(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		utils.WriteError(w, utils.NewError(http.StatusUnauthorized, "unauthorized", "Valid user bearer token required"))
		return
	}
	values := r.URL.Query()
	request := dtos.SearchTracesRequest{
		Query: values.Get("q"), Services: splitValues(values["service"]), Status: values.Get("status"),
		From: values.Get("from"), To: values.Get("to"), Limit: values.Get("limit"), Cursor: values.Get("cursor"),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := c.service.Search(ctx, r.PathValue("accountID"), r.PathValue("projectID"), user.ID, request)
	c.write(w, response, err)
}

func (c *TraceController) get(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		utils.WriteError(w, utils.NewError(http.StatusUnauthorized, "unauthorized", "Valid user bearer token required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := c.service.Get(ctx, r.PathValue("accountID"), r.PathValue("projectID"), r.PathValue("traceID"), user.ID)
	c.write(w, response, err)
}

func (c *TraceController) write(w http.ResponseWriter, response any, err error) {
	if err != nil {
		if status, _, _ := utils.ErrorResponse(err); status >= 500 {
			c.logger.Error("trace request failed", "error", err)
		}
		utils.WriteError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, response)
}
