package events

import "errors"

// ErrMalformedEvent indicates the event failed validation (poison → DLQ)
var ErrMalformedEvent = errors.New("malformed event")

// ErrTransientFailure indicates a temporary issue (ClickHouse down → retry)
var ErrTransientFailure = errors.New("transient failure")

// IsMalformedEvent checks if an error is due to malformed event
func IsMalformedEvent(err error) bool {
	return errors.Is(err, ErrMalformedEvent)
}

// IsTransientFailure checks if error is transient (should retry)
func IsTransientFailure(err error) bool {
	return errors.Is(err, ErrTransientFailure)
}
