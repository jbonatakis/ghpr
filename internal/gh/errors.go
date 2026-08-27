package gh

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

// TransientError marks a failure that is worth retrying rather than showing
// to the user as a problem with their setup: GitHub 5xx responses, secondary
// rate limiting, and network blips. A dashboard left open all day will hit
// these routinely and should ride them out.
type TransientError struct {
	Status int
	Detail string
	Err    error
}

func (e *TransientError) Error() string {
	switch {
	case e.Status == http.StatusBadGateway:
		return "GitHub returned 502 — the query was too slow upstream"
	case e.Status == http.StatusServiceUnavailable:
		return "GitHub is unavailable (503)"
	case e.Status == http.StatusGatewayTimeout:
		return "GitHub timed out (504)"
	case e.Status == http.StatusTooManyRequests:
		return "rate limited by GitHub (429)"
	case e.Status >= 500:
		return fmt.Sprintf("GitHub returned %d", e.Status)
	case e.Err != nil:
		return "network problem: " + CleanMessage(e.Err.Error(), 120)
	}
	return "temporary GitHub failure"
}

func (e *TransientError) Unwrap() error { return e.Err }

// Temporary keeps the type compatible with the net.Error convention.
func (e *TransientError) Temporary() bool { return true }

// IsTransient reports whether an error is worth a quiet retry.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var t *TransientError
	if errors.As(err, &t) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	return false
}

var tagPattern = regexp.MustCompile(`<[^>]*>`)

// CleanMessage turns an arbitrary upstream response into something safe to put
// on a single line of the UI: HTML stripped, all whitespace collapsed, and cut
// on a rune boundary so the result is never invalid UTF-8.
func CleanMessage(s string, max int) string {
	s = tagPattern.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:max])) + "…"
}
