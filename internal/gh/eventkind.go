package gh

import "fmt"

// Event kinds are written to disk, so they travel by name rather than by the
// number they happen to have. Reordering the constants would otherwise silently
// relabel every event in a saved log.
var eventKindNames = map[EventKind]string{
	EventOpened:          "opened",
	EventArrived:         "arrived",
	EventMerged:          "merged",
	EventClosed:          "closed",
	EventComment:         "comment",
	EventChecks:          "checks",
	EventReview:          "review",
	EventReadyForReview:  "ready",
	EventConflict:        "conflict",
	EventPush:            "push",
	EventReviewRequested: "review-requested",
	EventMention:         "mention",
	EventSessionStart:    "session-start",
}

func (k EventKind) String() string {
	if s, ok := eventKindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// MarshalText writes the stable name.
func (k EventKind) MarshalText() ([]byte, error) {
	if _, ok := eventKindNames[k]; !ok {
		return nil, fmt.Errorf("unknown event kind %d", int(k))
	}
	return []byte(k.String()), nil
}

// UnmarshalText reads it back. A name from a newer ghpr is an error rather than
// a guess, so a reader can drop that one line and carry on with the rest.
func (k *EventKind) UnmarshalText(b []byte) error {
	for kind, name := range eventKindNames {
		if name == string(b) {
			*k = kind
			return nil
		}
	}
	return fmt.Errorf("unknown event kind %q", b)
}
