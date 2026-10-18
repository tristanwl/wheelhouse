package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"
)

func TestErrorTextNeverEchoesUnknownErrors(t *testing.T) {
	_, urlErr := url.Parse(fmt.Sprintf("http://user:%s@example.com:port/", "CANARY-PASS"))
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"provider error":         {&ProviderError{Message: "Sonarr: API key incorrect"}, "Sonarr: API key incorrect"},
		"wrapped provider error": {fmt.Errorf("x: %w", &ProviderError{Message: "down"}), "down"},
		"deadline":               {fmt.Errorf("get: %w", context.DeadlineExceeded), "Request timed out"},
		"url error with creds":   {urlErr, "unknown error"},
		"plain":                  {errors.New("token=CANARY"), "unknown error"},
	} {
		if got := ErrorText(tc.err); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}
