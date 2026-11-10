package icsfeed

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Georgy-Garnov/agendling/internal/model"
	"github.com/Georgy-Garnov/agendling/internal/storage"
)

func calendarWithEvents(body string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//TEST//EN\r\n" + body + "END:VCALENDAR\r\n"
}

func event(uid, summary string) string {
	return fmt.Sprintf("BEGIN:VEVENT\r\nUID:%s\r\nDTSTART:20261201T180000Z\r\nDTEND:20261201T200000Z\r\nSUMMARY:%s\r\nEND:VEVENT\r\n", uid, summary)
}

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		input, want string
		valid       bool
	}{
		{"webcal://calendar.example/bvb.ics", "https://calendar.example/bvb.ics", true},
		{"https://calendar.example/f?key=abc", "https://calendar.example/f?key=abc", true},
		{"http://localhost:8080/file.ics", "http://localhost:8080/file.ics", true},
		{"file:///tmp/a.ics", "", false},
		{"javascript:alert(1)", "", false},
		{"https://user:pw@example.com/secret", "", false},
		{"", "", false},
	}
	for _, tt := range cases {
		got, err := NormalizeURL(tt.input)
		if (err == nil) != tt.valid || (tt.valid && got != tt.want) {
			t.Errorf("NormalizeURL(%q) = %q, %v", tt.input, got, err)
		}
	}
}

func TestFetchCancelledOccurrenceWithoutStart(t *testing.T) {
	feed := calendarWithEvents(strings.ReplaceAll(`BEGIN:VEVENT
UID:series
DTSTART:20261201T180000Z
DTEND:20261201T190000Z
RRULE:FREQ=DAILY;COUNT=3
SUMMARY:Daily training
END:VEVENT
BEGIN:VEVENT
UID:series
RECURRENCE-ID:20261202T180000Z
STATUS:CANCELLED
END:VEVENT
BEGIN:VEVENT
UID:cancelled
DTSTART:20261201T180000Z
STATUS:CANCELLED
END:VEVENT
`, "\n", "\r\n"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		fmt.Fprint(w, feed)
	}))
	defer srv.Close()
	got, err := Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].UID != "series" || len(got[0].ExDates) != 1 {
		t.Fatalf("unexpected cancellation result: %+v", got)
	}
	if got[0].ExDates[0].UTC().Format("20060102T150405") != "20261202T180000" {
		t.Fatalf("wrong EXDATE: %v", got[0].ExDates)
	}
}

func TestSyncRefreshFailureKeepsCachedEvents(t *testing.T) {
	response := calendarWithEvents(event("game-one", "BVB game one"))
	status := http.StatusOK
	var mu sync.RWMutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		code, body := status, response
		mu.RUnlock()
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	store, err := storage.Open(filepath.Join(t.TempDir(), "calendar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	src := &model.CalendarSource{Name: "BVB", Type: model.SourceICS, URL: srv.URL, Enabled: true, SyncInterval: 15}
	if err := store.SaveSource(src); err != nil {
		t.Fatal(err)
	}
	if added, err := Sync(context.Background(), store, src); err != nil || len(added) != 0 {
		t.Fatalf("first sync should not alert: %v, %+v", err, added)
	}
	if err := store.SetSyncStatus(src.ID, "", time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	src.LastSync = time.Now()
	mu.Lock()
	response = calendarWithEvents(event("game-two", "BVB game two"))
	mu.Unlock()
	added, err := Sync(context.Background(), store, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].UID != "game-two" {
		t.Fatalf("new events: %+v", added)
	}
	known, err := store.KnownUIDs(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(known) != 1 || !known["game-two"] || known["game-one"] {
		t.Fatalf("stale events: %+v", known)
	}
	mu.Lock()
	status = http.StatusServiceUnavailable
	mu.Unlock()
	if _, err := Sync(context.Background(), store, src); err == nil {
		t.Fatal("expected a fetch error")
	}
	known, err = store.KnownUIDs(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(known) != 1 || !known["game-two"] {
		t.Fatalf("cache lost on fetch failure: %+v", known)
	}
}
