package core

import (
	"context"
	"errors"
)

// ErrorText is the text of err that is safe to show: a ProviderError's
// (redacted) message, a fixed phrase for a timeout, and "unknown error" for
// anything else, so an unredacted error (a net/url error carries the URL, and
// so possibly credentials) can never reach a response.
func ErrorText(err error) string {
	if pe, ok := errors.AsType[*ProviderError](err); ok {
		return pe.Message
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Request timed out"
	}
	return "unknown error"
}
