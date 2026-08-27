package ui

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/jbonatakis/ghpr/internal/gh"
)

const html502 = `<html>
<head><title>502 Bad Gateway</title></head>
<body>
<center><h1>502 Bad Gateway — upstream timed out</h1></center>
<hr><center>GitHub.com</center>
</body>
</html>`

func transient502() error {
	return &gh.TransientError{Status: http.StatusBadGateway, Detail: gh.CleanMessage(html502, 160)}
}

func frameLines(m Model) []string { return strings.Split(m.View(), "\n") }

// TestFrameNeverOutgrowsTheTerminal is the invariant that a bad upstream
// message must never be able to break: the alt-screen corrupts if a frame is
// taller than the terminal, which reads to the user as a crash.
func TestFrameNeverOutgrowsTheTerminal(t *testing.T) {
	hostile := []error{
		transient502(),
		errors.New("github: HTTP 502: " + html502),
		errors.New(strings.Repeat("very long error ", 200)),
		errors.New("line one\nline two\nline three\nline four\nline five"),
	}
	sizes := []struct{ w, h int }{{60, 20}, {80, 24}, {100, 30}, {120, 40}, {200, 12}}

	for _, size := range sizes {
		for i, bad := range hostile {
			m := newLoaded(t, size.w, size.h)
			m.showDetail, m.showEvents = true, true
			m = update(m, fetchDoneMsg{seq: m.fetchSeq, err: bad})

			lines := frameLines(m)
			if len(lines) > size.h {
				t.Errorf("error %d at %dx%d: frame is %d lines", i, size.w, size.h, len(lines))
			}
			for j, ln := range lines {
				if w := ansi.StringWidth(ln); w > size.w {
					t.Errorf("error %d at %dx%d: line %d is %d cells", i, size.w, size.h, j, w)
				}
			}
		}
	}
}

func TestTransientErrorKeepsShowingTheLastGoodData(t *testing.T) {
	m := newLoaded(t, 120, 30)
	rows := len(m.rows)
	prs := len(m.prs)

	m = update(m, fetchDoneMsg{seq: m.fetchSeq, err: transient502()})

	if len(m.rows) != rows || len(m.prs) != prs {
		t.Errorf("data was discarded on a transient failure: %d/%d rows/prs, want %d/%d",
			len(m.rows), len(m.prs), rows, prs)
	}
	if m.err != nil {
		t.Errorf("a single blip should not raise a hard error, got %v", m.err)
	}
	if m.warn == "" {
		t.Error("expected a soft warning")
	}
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "retry") {
		t.Error("the header or footer should say a retry is coming")
	}
	if !strings.Contains(out, "#96") {
		t.Error("pull requests should still be listed during a blip")
	}
}

func TestTransientErrorRetriesSoonerThanTheNormalInterval(t *testing.T) {
	m := newLoaded(t, 120, 30)
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, err: transient502()})

	wait := time.Until(m.nextFetch)
	if wait > m.cfg.Interval {
		t.Errorf("retry scheduled in %s, want sooner than the %s interval", wait, m.cfg.Interval)
	}
	if wait <= 0 {
		t.Error("retry should be scheduled in the future, not hammered immediately")
	}
}

func TestRepeatedTransientFailuresBackOffAndEscalate(t *testing.T) {
	m := newLoaded(t, 120, 30)

	var prev time.Duration
	for i := 1; i <= 4; i++ {
		m = update(m, fetchDoneMsg{seq: m.fetchSeq, err: transient502()})
		wait := time.Until(m.nextFetch)
		if i > 1 && wait < prev {
			t.Errorf("failure %d waits %s, shorter than the previous %s", i, wait, prev)
		}
		prev = wait
	}
	if m.err == nil {
		t.Error("after several consecutive failures the user should see a real error")
	}
	if m.failures != 4 {
		t.Errorf("failures = %d, want 4", m.failures)
	}
}

func TestRecoveryClearsTheWarning(t *testing.T) {
	m := newLoaded(t, 120, 30)
	for i := 0; i < 4; i++ {
		m = update(m, fetchDoneMsg{seq: m.fetchSeq, err: transient502()})
	}
	if m.err == nil || m.warn == "" {
		t.Fatal("expected the model to be in a failed state")
	}

	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: loadFixture(t)})

	if m.err != nil || m.warn != "" || m.failures != 0 {
		t.Errorf("recovery left err=%v warn=%q failures=%d", m.err, m.warn, m.failures)
	}
	if !strings.Contains(ansi.Strip(m.View()), "#96") {
		t.Error("data should be back after recovery")
	}
}

func TestFatalErrorIsShownImmediately(t *testing.T) {
	m := newLoaded(t, 120, 30)
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, err: errors.New("token rejected (401) — re-run `gh auth login`")})

	if m.err == nil {
		t.Fatal("an auth failure should surface at once")
	}
	if m.warn != "" {
		t.Error("a fatal error should not be dressed up as a retry")
	}
	if !strings.Contains(ansi.Strip(m.View()), "401") {
		t.Error("the footer should show the auth error")
	}
}

func TestFooterStaysOnOneLineWithoutEvictingContent(t *testing.T) {
	for _, bad := range []error{
		transient502(),
		errors.New("a\nb\nc\nd"),
		errors.New(strings.Repeat("x", 5000)),
		errors.New("github: HTTP 502: " + html502),
	} {
		m := newLoaded(t, 100, 24)
		m = update(m, fetchDoneMsg{seq: m.fetchSeq, err: bad})

		lines := frameLines(m)
		if len(lines) != 24 {
			t.Errorf("frame is %d lines, want exactly 24", len(lines))
		}
		last := lines[len(lines)-1]
		if ansi.StringWidth(last) > 100 {
			t.Errorf("footer is %d cells wide", ansi.StringWidth(last))
		}
		// The footer must occupy one row, not shove the list off the top of
		// the screen -- clamping the frame would hide that on its own.
		out := ansi.Strip(m.View())
		if !strings.Contains(out, "#96") {
			t.Errorf("a long error evicted the pull request list: %q", firstLines(out, 4))
		}
		if !strings.Contains(out, "ghpr") {
			t.Error("a long error evicted the header")
		}
		// And the message itself has to actually reach the footer.
		if !strings.Contains(ansi.Strip(last), "5") && !strings.Contains(ansi.Strip(last), "x") &&
			!strings.Contains(ansi.Strip(last), "a b c d") {
			t.Errorf("footer does not carry the message: %q", ansi.Strip(last))
		}
	}
}

func firstLines(s string, n int) string {
	parts := strings.Split(s, "\n")
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, " / ")
}

func TestTransientWarningReachesTheFooter(t *testing.T) {
	m := newLoaded(t, 120, 24)
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, err: transient502()})

	lines := frameLines(m)
	footer := ansi.Strip(lines[len(lines)-1])
	if !strings.Contains(footer, "502") {
		t.Errorf("footer should explain the blip, got %q", footer)
	}
	if !strings.Contains(footer, "retrying") {
		t.Errorf("footer should say it is retrying, got %q", footer)
	}
}

func TestModeSwitchDoesNotCrashOnAnEmptyModel(t *testing.T) {
	// Switching mode clears the list while the next fetch is in flight; the
	// frame must still render, including the detail pane with no selection.
	m := newLoaded(t, 120, 30)
	m.showDetail, m.showEvents = true, true
	m = press(m, 'm')

	if len(m.rows) != 0 {
		t.Fatalf("mode switch should clear rows, got %d", len(m.rows))
	}
	if m.selected() != nil {
		t.Error("nothing should be selected after a mode switch")
	}
	lines := frameLines(m)
	if len(lines) > 30 {
		t.Errorf("frame is %d lines", len(lines))
	}
	if m.cfg.Mode != gh.ModeReviewRequested {
		t.Errorf("mode = %v, want review-requested", m.cfg.Mode)
	}

	// And a 502 arriving for the new mode must not take the app down.
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, err: transient502()})
	if got := len(frameLines(m)); got > 30 {
		t.Errorf("frame after a failed mode switch is %d lines", got)
	}
	if !strings.Contains(ansi.Strip(m.View()), "loading") {
		t.Error("an empty list mid-retry should say it is still loading")
	}
}
