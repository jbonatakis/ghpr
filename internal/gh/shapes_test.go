package gh

import (
	"strings"
	"testing"
	"time"
)

func TestParseShapes(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    []Shape
		wantErr bool
		why     string
	}{
		{"involved,requested,reviewed", AllShapes, false, "the whole set"},
		{"requested", []Shape{ShapeRequested}, false, "just the one that covers CODEOWNERS"},
		{"involved,reviewed", []Shape{ShapeInvolved, ShapeReviewed}, false, "dropping review requests"},
		{" involved , reviewed ", []Shape{ShapeInvolved, ShapeReviewed}, false, "spaces around names"},
		{"INVOLVED", []Shape{ShapeInvolved}, false, "case does not matter"},
		{"reviewed,reviewed", []Shape{ShapeReviewed}, false, "a repeat is not two searches"},
		{"", nil, false, "empty watches nothing"},
		{"everything", nil, true, "an unknown name is refused, not ignored"},
		{"involved,typo", nil, true, "one bad name spoils the list"},
	} {
		got, err := ParseShapes(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseShapes(%q) was accepted — %s", tc.in, tc.why)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseShapes(%q): %v", tc.in, err)
			continue
		}
		if ShapeNames(got) != ShapeNames(tc.want) {
			t.Errorf("ParseShapes(%q) = %v, want %v — %s", tc.in, got, tc.want, tc.why)
		}
	}
}

// Each shape has to produce the qualifier it claims to, or the toggle turns
// something else on and off.
func TestEachShapeSearchesForWhatItNames(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		shape Shape
		want  string
	}{
		{ShapeInvolved, "involves:@me"},
		{ShapeRequested, "review-requested:@me"},
		{ShapeReviewed, "reviewed-by:@me"},
	} {
		got := BackfillSearches("", now.Add(-20*time.Minute), now, []Shape{tc.shape})
		if len(got) != 1 {
			t.Fatalf("%s produced %d searches", tc.shape, len(got))
		}
		if !strings.Contains(got[0].Query, tc.want) {
			t.Errorf("%s searches %q, want %s", tc.shape, got[0].Query, tc.want)
		}
		if got[0].Shape != tc.shape {
			t.Errorf("the plan is labelled %s, want %s", got[0].Shape, tc.shape)
		}
	}
}

// Turning a shape off has to stop its searches, or the toggle does nothing.
func TestATurnedOffShapeIsNotSearchedFor(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	got := BackfillSearches("", now.Add(-24*time.Hour), now, []Shape{ShapeInvolved, ShapeReviewed})

	for _, p := range got {
		if strings.Contains(p.Query, "review-requested:@me") {
			t.Errorf("a shape that was switched off is still searched: %q", p.Query)
		}
	}
	if len(got) == 0 {
		t.Fatal("dropping one shape stopped every search")
	}
	for _, p := range got {
		if p.Needs != 2 {
			t.Errorf("a window says it needs %d answers, but two shapes cover it", p.Needs)
		}
	}
}

// Watching nothing is a coherent request, and has to mean no searches rather
// than all of them.
func TestWatchingNothingSearchesForNothing(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	if got := BackfillSearches("", now.Add(-24*time.Hour), now, nil); len(got) != 0 {
		t.Errorf("watching nothing still ran %d searches", len(got))
	}
}

// The selection is part of what a stretch was covered *with*, so changing it
// has to lapse the coverage — otherwise turning a shape on would leave every
// earlier stretch marked as covered by searches that never ran.
func TestTheWatchSelectionIsPartOfTheScope(t *testing.T) {
	full := BackfillScope("", AllShapes)
	narrow := BackfillScope("", []Shape{ShapeInvolved})

	if full == narrow {
		t.Error("watching more is indistinguishable from watching less; " +
			"turning a shape on would never re-search anything")
	}
	if BackfillScope("", AllShapes) != full {
		t.Error("the same selection produced two different scopes")
	}
	if BackfillScope("org:acme", AllShapes) == full {
		t.Error("the extra qualifiers dropped out of the scope")
	}
}
