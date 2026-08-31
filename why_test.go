package main

import (
	"strings"
	"testing"
	"time"

	"github.com/jbonatakis/ghpr/internal/config"
	"github.com/jbonatakis/ghpr/internal/gh"
	"github.com/jbonatakis/ghpr/internal/ui"
)

// Every age was printed as -153722867m, which is what subtracting a real time
// from the zero one gives. The result had a column of identical nonsense down
// it and every judgement about what was in or out of the window had to be made
// from the raw dates instead.
func TestTheAgesAreRelativeToNow(t *testing.T) {
	cfg := ui.Config{
		Client: fixtureServer(t), Mode: gh.ModeAuthored, Max: 200,
		Prefs: config.Defaults(), Seed: 100000 * time.Hour, Watch: gh.AllShapes,
	}
	out := capture(t, func() error { return explainSeed(cfg) })

	if strings.Contains(out, "-153722867") {
		t.Error("ages are measured from the zero time")
	}
	// Every dated line is "<label> <age> <IN|outside> <date>". The age is the
	// field before the verdict, and an age is never negative.
	var checked int
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if f != "IN" && f != "outside" {
				continue
			}
			if i == 0 {
				break
			}
			checked++
			if age := fields[i-1]; strings.HasPrefix(age, "-") {
				t.Errorf("negative age %q on %q", age, strings.TrimSpace(line))
			}
			break
		}
	}
	if checked == 0 {
		t.Fatal("no dated lines to check")
	}
}

// The diagnostic has to describe the run about to happen. With a record behind
// it the searches cover the gap and the rest comes off disk, and reporting the
// whole window instead describes a launch nobody is about to make — which is
// how it came to report ninety-nine events for a run that would gather a
// handful.
func TestTheDiagnosticAppliesTheSameClampAsALaunch(t *testing.T) {
	watermark := time.Now().Add(-20 * time.Minute)
	cfg := ui.Config{
		Client: fixtureServer(t), Mode: gh.ModeAuthored, Max: 200,
		Prefs: config.Defaults(), Seed: 48 * time.Hour, Watch: gh.AllShapes,
		Watermark: watermark,
	}
	out := capture(t, func() error { return explainSeed(cfg) })

	if !strings.Contains(out, "the record already covers up to") {
		t.Errorf("the diagnostic does not mention the record clamping the searches:\n%s", out)
	}
	if !strings.Contains(out, "-remember=false") {
		t.Error("nothing says how to see what a first launch would search")
	}

	// Twenty minutes of gap is one window, not the eight a full 48h would be.
	planned := len(gh.BackfillSearches("", watermark, time.Now(), gh.AllShapes))
	full := len(gh.BackfillSearches("", time.Now().Add(-48*time.Hour), time.Now(), gh.AllShapes))
	if planned >= full {
		t.Fatalf("the clamp saves nothing: %d searches against %d", planned, full)
	}
	var searches int
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "is:pr archived:false") {
			searches++
		}
	}
	if searches != planned {
		t.Errorf("the diagnostic listed %d searches, but a launch would run %d", searches, planned)
	}
}

// With nothing on record it is the whole window, and it should say so plainly
// rather than describing a clamp that is not happening.
func TestTheDiagnosticSaysWhenNothingIsOnRecord(t *testing.T) {
	cfg := ui.Config{
		Client: fixtureServer(t), Mode: gh.ModeAuthored, Max: 200,
		Prefs: config.Defaults(), Seed: time.Hour, Watch: gh.AllShapes,
	}
	out := capture(t, func() error { return explainSeed(cfg) })

	if !strings.Contains(out, "nothing is on record as covered") {
		t.Errorf("a first launch is not described as one:\n%s", out)
	}
	if strings.Contains(out, "the record already covers") {
		t.Error("a clamp was described that is not happening")
	}
}
