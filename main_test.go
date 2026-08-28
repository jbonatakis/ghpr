package main

import (
	"testing"
	"time"
)

// The saved seed window has three distinct answers, and "nothing saved" must
// not be confused with "saved as zero" — the first leaves the flag's default
// alone, the second is a deliberate choice to start the feed blank.
func TestParseSeed(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    time.Duration
		wantErr bool
		why     string
	}{
		{"", -1, false, "nothing saved: keep the flag default"},
		{"   ", -1, false, "blank is nothing saved"},
		{"1h", time.Hour, false, "the usual case"},
		{"30m", 30 * time.Minute, false, "a shorter window"},
		{"0", 0, false, "deliberately blank feed"},
		{"0s", 0, false, "the same, spelled out"},
		{"-5m", 0, false, "backwards is not a window; treat it as off"},
		{"soon", -1, true, "not a duration"},
		{"60", -1, true, "a bare number is not a duration"},
	} {
		got, err := parseSeed(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseSeed(%q) accepted it — %s", tc.in, tc.why)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSeed(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseSeed(%q) = %v, want %v — %s", tc.in, got, tc.want, tc.why)
		}
	}
}
