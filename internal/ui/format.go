package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// compactAge renders a duration in the smallest sensible unit: 4m, 3h, 2d, 5w.
func compactAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/(24*7)))
	}
	return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
}

// longAge is the spelled-out form used in the detail pane.
func longAge(d time.Duration) string {
	if d < time.Minute {
		return "moments ago"
	}
	if d < time.Hour {
		return fmt.Sprintf("%s ago", plural(int(d.Minutes()), "minute"))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%s ago", plural(int(d.Hours()), "hour"))
	}
	return fmt.Sprintf("%s ago", plural(int(d.Hours()/24), "day"))
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// tidyDuration drops the zero tail Go leaves on a round duration, so an hour
// set with -seed 1h reads back as "1h" rather than "1h0m0s".
func tidyDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// pad right-pads to exactly w display cells, truncating with an ellipsis.
func pad(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = runewidth.Truncate(s, w, "…")
	return s + spaces(w-runewidth.StringWidth(s))
}

// padLeft left-pads to exactly w display cells.
func padLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = runewidth.Truncate(s, w, "…")
	return spaces(w-runewidth.StringWidth(s)) + s
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	const blanks = "                                                                                "
	if n <= len(blanks) {
		return blanks[:n]
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = ' '
	}
	return string(out)
}

// diffStat renders the +/- churn, compacting thousands so the column stays narrow.
func diffStat(add, del int) string {
	return fmt.Sprintf("+%s/-%s", compactNum(add), compactNum(del))
}

func compactNum(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%dk", n/1000)
}

// eventRef renders "repo#number" in exactly w cells, shortening the repository
// name rather than the number. The number is what identifies a pull request, so
// it is the last thing that may be dropped — truncating the whole reference
// left activity lines reading "sensor-presence-collector#…", which named the
// repository but not the pull request.
func eventRef(repo string, number, w int) string {
	return pad(eventRefText(repo, number, w), w)
}

// eventRefText is the same reference without the column padding, for when a
// hyperlink should wrap only the visible text.
func eventRefText(repo string, number, w int) string {
	if w <= 0 {
		return ""
	}
	num := fmt.Sprintf("#%d", number)
	nw := runewidth.StringWidth(num)
	if nw >= w {
		// No room for any of the name; the number still earns its place.
		return padLeft(num, w)
	}
	return runewidth.Truncate(repo, w-nw, "…") + num
}

// Activity line columns: timestamp, reference, description, actor.
const (
	evTimeWidth  = 8
	evWhatWidth  = 21 // widest real text is "changes requested"; leave headroom
	evActorWidth = 16
	evRefMin     = 12
	evRefMax     = 46
	// leading space + time + gap + gap + what + gap + actor
	evReserved = 1 + evTimeWidth + 2 + 2 + evWhatWidth + 2 + evActorWidth
)

// eventRefWidth is how much of an activity line to give the reference column,
// leaving room for the timestamp, description and actor.
func eventRefWidth(total int) int {
	w := total - evReserved
	if w < evRefMin {
		w = evRefMin
	}
	if w > evRefMax {
		w = evRefMax
	}
	return w
}
