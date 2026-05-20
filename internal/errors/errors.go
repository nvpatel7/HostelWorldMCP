package errors

import (
	"errors"
	"fmt"
)

// Code is a stable, machine-readable error identifier. Tools return these in
// structured tool errors so the model can recognise the failure class and
// respond appropriately to the user.
type Code string

const (
	CodeInvalidInput     Code = "invalid_input"
	CodeNotFound         Code = "not_found"
	CodeNoAvailability   Code = "no_availability"
	CodeRateLimited      Code = "rate_limited"
	CodeServiceBusy      Code = "service_busy"
	CodeQuotaExhausted   Code = "quota_exhausted"
	CodeServiceDisabled  Code = "service_disabled"
	CodeServiceError     Code = "service_error"
)

// Error carries both an external (safe) face and an internal (full) face. The
// MCP layer only sees External(); logs record the full Error.
type Error struct {
	Code    Code
	Message string
	Retry   int
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

// External returns the safe view: code, message, and retry hint. Never
// includes the cause or any upstream body — those stay in logs.
func (e *Error) External() map[string]any {
	out := map[string]any{
		"code":    string(e.Code),
		"message": e.Message,
	}
	if e.Retry > 0 {
		out["retry_after_seconds"] = e.Retry
	}
	return out
}

func New(code Code, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

func Wrap(code Code, msg string, cause error) *Error {
	return &Error{Code: code, Message: msg, Cause: cause}
}

func InvalidInput(msg string) *Error  { return New(CodeInvalidInput, msg) }
func NotFound(msg string) *Error      { return New(CodeNotFound, msg) }
func NoAvailability(msg string) *Error { return New(CodeNoAvailability, msg) }
func ServiceError(cause error) *Error { return Wrap(CodeServiceError, "internal server error", cause) }

// As is a typed errors.As helper.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
