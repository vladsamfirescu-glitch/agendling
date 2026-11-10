package core

import (
	"strings"
	"time"

	"github.com/Georgy-Garnov/agendling/internal/model"
)

// ReadOnlyEventDetails renders a small preview for events from subscribed ICS calendars.
func ReadOnlyEventDetails(e *model.Event, occurrenceStart time.Time) string {
	start := e.StartTime
	end := e.EndTime
	if !occurrenceStart.IsZero() {
		end = occurrenceStart.Add(e.Duration())
		start = occurrenceStart
	}
	var b strings.Builder
	b.WriteString(e.Title)
	b.WriteString("\n\n")
	if e.AllDay {
		b.WriteString(start.Format("Mon, 02 Jan 2006"))
	} else {
		b.WriteString(start.Format("Mon, 02 Jan 2006 15:04"))
		b.WriteString(" – ")
		b.WriteString(end.Format("Mon, 02 Jan 2006 15:04"))
	}
	if e.Location != "" {
		b.WriteString("\nLocation: " + e.Location)
	}
	if e.URL != "" {
		b.WriteString("\nLink: " + e.URL)
	}
	if e.Description != "" {
		b.WriteString("\n\n" + e.Description)
	}
	b.WriteString("\n\nRead-only ICS subscription")
	return b.String()
}
