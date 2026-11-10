package core

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Georgy-Garnov/agendling/internal/model"
	"github.com/Georgy-Garnov/agendling/internal/storage"
)

func TestICSSubscriptionIsVisibleButCannotBeEdited(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "calendar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	local := &model.CalendarSource{Name: "Personal", Type: model.SourceLocal, Enabled: true, Color: "#111111"}
	sub := &model.CalendarSource{Name: "BVB", Type: model.SourceICS, URL: "https://example.com/feed.ics", Enabled: true, Color: "#ffff00"}
	if err := store.SaveSource(local); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSource(sub); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: store}
	cals := svc.Calendars()
	if len(cals) != 2 || cals[1].Source.Type != model.SourceICS {
		t.Fatalf("visible calendars = %+v", cals)
	}
	editable, _ := svc.EditableCalendars(&model.Event{})
	if len(editable) != 1 || editable[0].Source.Type != model.SourceLocal {
		t.Fatalf("subscribed ICS calendar should not be an edit destination: %+v", editable)
	}
	if err := svc.RemoveSeries(sub.ID, "fixture"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected read-only delete error, got %v", err)
	}
	if err := svc.SaveEvent(&model.Event{UID: "fixture"}, 0, cals[1]); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected read-only save error, got %v", err)
	}
	details := ReadOnlyEventDetails(&model.Event{
		Title: "Dortmund vs Frankfurt", StartTime: time.Date(2026, 12, 1, 20, 0, 0, 0, time.UTC),
		EndTime: time.Date(2026, 12, 1, 22, 0, 0, 0, time.UTC), Location: "Dortmund",
	}, time.Time{})
	if !strings.Contains(details, "Dortmund vs Frankfurt") || !strings.Contains(details, "Read-only") {
		t.Fatal(details)
	}
}
