package service

import "errors"

// Sentinel errors. Handlers use errors.Is to map these to HTTP status codes.
var (
	// ErrValidation signals a required field is missing or empty → 400 Bad Request.
	ErrValidation = errors.New("validation error")

	// ErrUnprocessable signals a field is present but semantically invalid
	// (e.g. entry_point_type not in the allow-list) → 422 Unprocessable Entity.
	ErrUnprocessable = errors.New("unprocessable entity")

	// ErrNotFound signals the addressed resource does not exist → 404 Not Found.
	ErrNotFound = errors.New("not found")

	// ErrTemporalUnavailable signals Temporal is not configured → 503.
	ErrTemporalUnavailable = errors.New("temporal not configured")
)

// FieldError wraps ErrValidation or ErrUnprocessable with a specific message so
// handlers can surface the exact error strings the current API returns.
type FieldError struct {
	Kind    error  // ErrValidation or ErrUnprocessable
	Message string // human-readable; byte-identical to current handler wording
}

func (e *FieldError) Error() string { return e.Message }
func (e *FieldError) Unwrap() error { return e.Kind }

// validation returns an ErrValidation FieldError with the given message.
func validation(msg string) error {
	return &FieldError{Kind: ErrValidation, Message: msg}
}

// unprocessable returns an ErrUnprocessable FieldError with the given message.
func unprocessable(msg string) error {
	return &FieldError{Kind: ErrUnprocessable, Message: msg}
}
