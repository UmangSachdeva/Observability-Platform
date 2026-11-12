package utils

import (
	"errors"
	"fmt"
)

// AppError carries a safe message for API clients and an optional internal cause.
type AppError struct {
	Status  int
	Code    string
	Message string
	Cause   error
}

func (e *AppError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *AppError) Unwrap() error { return e.Cause }

func NewError(status int, code, message string) *AppError {
	return &AppError{Status: status, Code: code, Message: message}
}

func WrapError(status int, code, message string, cause error) *AppError {
	return &AppError{Status: status, Code: code, Message: message, Cause: cause}
}

func ErrorResponse(err error) (status int, code, message string) {
	var appError *AppError
	if errors.As(err, &appError) {
		return appError.Status, appError.Code, appError.Message
	}
	return 500, "internal_error", "An internal error occurred"
}
