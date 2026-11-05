package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/observability"
)

// ErrorBody is the error envelope returned by every control-plane endpoint:
//
//	{"error":{"code":"not_found","message":"...","request_id":"...","details":...}}
type ErrorBody struct {
	Error ErrorInfo `json:"error"`
}

// ErrorInfo carries a stable machine code, a human message, the request id
// and optional structured details (field errors, conflicting revision, …).
type ErrorInfo struct {
	Code      string `json:"code" doc:"Stable machine-readable error code."`
	Message   string `json:"message" doc:"Human-readable message. Never contains secrets."`
	RequestID string `json:"request_id,omitempty" doc:"Correlates with server logs."`
	Details   any    `json:"details,omitempty" doc:"Optional structured details."`
}

// Error implements huma.StatusError.
type Error struct {
	status int
	ErrorBody
}

func (e *Error) Error() string  { return e.ErrorBody.Error.Message }
func (e *Error) GetStatus() int { return e.status }

// Code returns the machine-readable code.
func (e *Error) Code() string { return e.ErrorBody.Error.Code }

// FieldError describes one invalid input field.
type FieldError struct {
	Location string `json:"location"`
	Message  string `json:"message"`
	Value    any    `json:"value,omitempty"`
}

// Errorf builds an API error.
func Errorf(status int, code, msg string) *Error {
	return &Error{status: status, ErrorBody: ErrorBody{Error: ErrorInfo{Code: code, Message: msg}}}
}

// WithDetails attaches details.
func (e *Error) WithDetails(d any) *Error {
	e.ErrorBody.Error.Details = d
	return e
}

// Common errors.
func NotFound(what string) *Error { return Errorf(http.StatusNotFound, "not_found", what+" not found") }
func Conflict(msg string) *Error  { return Errorf(http.StatusConflict, "conflict", msg) }
func Invalid(msg string, fields ...FieldError) *Error {
	e := Errorf(http.StatusUnprocessableEntity, "validation_failed", msg)
	if len(fields) > 0 {
		e = e.WithDetails(map[string]any{"fields": fields})
	}
	return e
}
func Unauthorized() *Error {
	return Errorf(http.StatusUnauthorized, "unauthenticated", "authentication required")
}
func Forbidden(msg string) *Error { return Errorf(http.StatusForbidden, "forbidden", msg) }
func PreconditionFailed(current string) *Error {
	return Errorf(http.StatusPreconditionFailed, "revision_mismatch",
		"the object was modified since it was read; re-read and retry").WithDetails(map[string]string{"current_etag": current})
}
func PreconditionRequired() *Error {
	return Errorf(http.StatusPreconditionRequired, "precondition_required", "If-Match header with the current ETag is required")
}
func TooMany(msg string) *Error { return Errorf(http.StatusTooManyRequests, "rate_limited", msg) }
func Internal() *Error {
	return Errorf(http.StatusInternalServerError, "internal", "internal server error")
}

func init() {
	// Every list field is always serialized as an array (never null); the
	// published contract reflects that.
	huma.DefaultArrayNullable = false
	huma.NewErrorWithContext = func(hctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
		e := newHumaError(status, msg, errs...)
		if hctx != nil {
			e.ErrorBody.Error.RequestID = observability.RequestID(hctx.Context())
		}
		return e
	}
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		return newHumaError(status, msg, errs...)
	}
}

func newHumaError(status int, msg string, errs ...error) *Error {
	code := codeForStatus(status)
	var fields []FieldError
	for _, err := range errs {
		if err == nil {
			continue
		}
		var ae *Error
		if errors.As(err, &ae) {
			return ae
		}
		var d huma.ErrorDetailer
		if errors.As(err, &d) {
			det := d.ErrorDetail()
			fields = append(fields, FieldError{Location: det.Location, Message: det.Message, Value: det.Value})
			continue
		}
		fields = append(fields, FieldError{Message: err.Error()})
	}
	if status >= 500 {
		// Never leak internal error strings.
		return Internal()
	}
	e := Errorf(status, code, msg)
	if len(fields) > 0 {
		e = e.WithDetails(map[string]any{"fields": fields})
	}
	return e
}

func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthenticated"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusPreconditionFailed:
		return "revision_mismatch"
	case http.StatusRequestEntityTooLarge:
		return "too_large"
	case http.StatusUnprocessableEntity:
		return "validation_failed"
	case http.StatusTooManyRequests:
		return "rate_limited"
	}
	if status >= 500 {
		return "internal"
	}
	return "error"
}

// WriteError writes the envelope for non-huma handlers.
func WriteError(w http.ResponseWriter, r *http.Request, e *Error) {
	e.ErrorBody.Error.RequestID = observability.RequestID(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(e.status)
	_ = json.NewEncoder(w).Encode(e.ErrorBody)
}

// RequestIDFrom returns the correlation id of a context.
func RequestIDFrom(ctx context.Context) string { return observability.RequestID(ctx) }
