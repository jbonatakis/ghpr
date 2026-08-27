package gh

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// html502 is shaped like a real gateway error page: multi-line, tagged, and
// carrying a multibyte character.
const html502 = `<html>
<head><title>502 Bad Gateway</title></head>
<body>
<center><h1>502 Bad Gateway — upstream timed out</h1></center>
<hr><center>GitHub.com</center>
</body>
</html>`

func clientFor(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := NewClient("t")
	c.Endpoint = srv.URL
	return c
}

func TestCleanMessageFlattensHTMLBodies(t *testing.T) {
	got := CleanMessage(html502, 200)

	if strings.ContainsAny(got, "\n\r\t") {
		t.Errorf("message still contains line breaks: %q", got)
	}
	if strings.Contains(got, "<") || strings.Contains(got, ">") {
		t.Errorf("HTML tags survived: %q", got)
	}
	if !strings.Contains(got, "502 Bad Gateway") {
		t.Errorf("lost the useful part: %q", got)
	}
	if strings.Contains(got, "  ") {
		t.Errorf("whitespace not collapsed: %q", got)
	}
}

func TestCleanMessageCutsOnRuneBoundaries(t *testing.T) {
	// Every cut point must leave valid UTF-8, including mid-multibyte ones.
	s := strings.Repeat("é—ü", 100)
	for _, n := range []int{0, 1, 2, 3, 7, 50, 199} {
		got := CleanMessage(s, n)
		if !utf8.ValidString(got) {
			t.Errorf("CleanMessage(%d) produced invalid UTF-8: %q", n, got)
		}
		if n > 0 && utf8.RuneCountInString(got) > n+1 { // +1 for the ellipsis
			t.Errorf("CleanMessage(%d) returned %d runes", n, utf8.RuneCountInString(got))
		}
	}
}

func TestServerErrorsAreTransient(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504, 429} {
		c := clientFor(t, status, html502)
		_, err := c.Fetch(context.Background(), "q", 10)
		if err == nil {
			t.Fatalf("status %d: expected an error", status)
		}
		if !IsTransient(err) {
			t.Errorf("status %d should be transient, got %v", status, err)
		}
		if strings.ContainsAny(err.Error(), "\n\r") {
			t.Errorf("status %d: error message spans lines: %q", status, err.Error())
		}
	}
}

func TestBadGatewayExplainsItself(t *testing.T) {
	c := clientFor(t, http.StatusBadGateway, html502)
	_, err := c.Fetch(context.Background(), "q", 10)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("message should name the status: %q", err.Error())
	}
	var te *TransientError
	if !errors.As(err, &te) || te.Status != http.StatusBadGateway {
		t.Errorf("expected a TransientError carrying 502, got %#v", err)
	}
}

func TestAuthFailuresAreNotTransient(t *testing.T) {
	c := clientFor(t, http.StatusUnauthorized, "")
	_, err := c.Fetch(context.Background(), "q", 10)
	if err == nil {
		t.Fatal("expected an error")
	}
	if IsTransient(err) {
		t.Error("a rejected token must not be retried silently forever")
	}
}

func TestForbiddenRateLimitIsTransientButOtherForbiddenIsNot(t *testing.T) {
	limited := clientFor(t, http.StatusForbidden, `{"message":"You have exceeded a secondary rate limit"}`)
	_, err := limited.Fetch(context.Background(), "q", 10)
	if !IsTransient(err) {
		t.Errorf("secondary rate limiting should be transient, got %v", err)
	}

	denied := clientFor(t, http.StatusForbidden, `{"message":"Resource not accessible"}`)
	_, err = denied.Fetch(context.Background(), "q", 10)
	if IsTransient(err) {
		t.Errorf("a genuine permission failure should surface, got %v", err)
	}
}

func TestNetworkFailureIsTransient(t *testing.T) {
	c := NewClient("t")
	c.Endpoint = "http://127.0.0.1:1" // nothing listening
	_, err := c.Fetch(context.Background(), "q", 10)
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if !IsTransient(err) {
		t.Errorf("a network blip should be transient, got %v", err)
	}
}

func TestGraphQLTimeoutIsTransient(t *testing.T) {
	c := clientFor(t, http.StatusOK,
		`{"data":{},"errors":[{"type":"TIMEDOUT","message":"Something went wrong while executing your query."}]}`)
	_, err := c.Fetch(context.Background(), "q", 10)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsTransient(err) {
		t.Errorf("a GraphQL TIMEDOUT should be retried, got %v", err)
	}
}

func TestGraphQLValidationErrorIsNotTransient(t *testing.T) {
	c := clientFor(t, http.StatusOK,
		`{"errors":[{"type":"INVALID","message":"Field 'nope' doesn't exist"}]}`)
	_, err := c.Fetch(context.Background(), "q", 10)
	if err == nil {
		t.Fatal("expected an error")
	}
	if IsTransient(err) {
		t.Error("a malformed query is not going to fix itself")
	}
}

func TestMalformedJSONIsReported(t *testing.T) {
	c := clientFor(t, http.StatusOK, "{not json")
	_, err := c.Fetch(context.Background(), "q", 10)
	if err == nil {
		t.Fatal("expected an error for an unreadable body")
	}
}
