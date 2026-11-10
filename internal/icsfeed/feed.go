// Package icsfeed implements read-only automatic subscriptions to HTTP iCalendar (.ics) feeds.
package icsfeed

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/Georgy-Garnov/agendling/internal/ics"
	"github.com/Georgy-Garnov/agendling/internal/model"
	"github.com/Georgy-Garnov/agendling/internal/storage"
)

const maxFeedBytes = 8 << 20 // guard against accidentally fetching a huge feed

var httpClient = &http.Client{Timeout: 30 * time.Second}

// NormalizeURL accepts https://, http:// and webcal:// subscription links.
// The webcal convention is converted to HTTPS before downloading.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid subscription URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "webcal", "webcals":
		u.Scheme = "https"
	case "https", "http":
		u.Scheme = strings.ToLower(u.Scheme)
	default:
		return "", fmt.Errorf("use an https://, http:// or webcal:// subscription URL")
	}
	if u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("invalid subscription URL: expected a host, without credentials")
	}
	u.Fragment = ""
	return u.String(), nil
}

// Fetch retrieves the whole feed. Only a successfully parsed iCalendar document may replace
// the last working cache. No feed-side authentication or local files are supported.
func Fetch(ctx context.Context, rawURL string) ([]*model.Event, error) {
	feedURL, err := NormalizeURL(rawURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/calendar, text/plain;q=0.8, */*;q=0.2")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch ICS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ICS server returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read ICS feed: %w", err)
	}
	if len(data) > maxFeedBytes {
		return nil, fmt.Errorf("ICS feed exceeds 8 MiB limit")
	}
	if !strings.Contains(strings.ToUpper(string(data[:min(len(data), 1024)])), "BEGIN:VCALENDAR") {
		return nil, fmt.Errorf("URL did not return an iCalendar feed (BEGIN:VCALENDAR missing)")
	}
	cal, err := ical.NewDecoder(strings.NewReader(string(data))).Decode()
	if err != nil {
		return nil, fmt.Errorf("parse ICS feed: %w", err)
	}
	if cal == nil {
		return nil, fmt.Errorf("empty ICS feed")
	}
	// Cancelled series are removed; cancelled individual instances become EXDATEs.
	// Cancellation notices may omit DTSTART, so inspect their UID/RECURRENCE-ID
	// before running the full VEVENT parser.
	cancelledSeries := make(map[string]bool)
	cancelledInstances := make(map[string][]time.Time)
	var events []*model.Event
	for _, comp := range cal.Children {
		if comp.Name != ical.CompEvent {
			continue
		}
		if status := comp.Props.Get("STATUS"); status != nil && strings.EqualFold(status.Value, "CANCELLED") {
			uidProp := comp.Props.Get(ical.PropUID)
			if uidProp == nil || uidProp.Value == "" {
				continue
			}
			uid := uidProp.Value
			if rid := comp.Props.Get(ical.PropRecurrenceID); rid != nil {
				if at, _, err := ics.ParseTime(rid); err == nil {
					cancelledInstances[uid] = append(cancelledInstances[uid], at)
				}
			} else {
				cancelledSeries[uid] = true
			}
			continue
		}
		one := ical.NewCalendar()
		one.Children = append(one.Children, comp)
		events = append(events, ics.ToEvents(one, "", "")...)
	}
	var result []*model.Event
	for _, e := range events {
		if cancelledSeries[e.UID] {
			continue
		}
		if !e.RecurrenceID.IsZero() {
			if isCancelled(e.RecurrenceID, cancelledInstances[e.UID]) {
				continue
			}
		} else {
			e.ExDates = append(e.ExDates, cancelledInstances[e.UID]...)
		}
		result = append(result, e)
	}
	return result, nil
}

func isCancelled(at time.Time, cancelled []time.Time) bool {
	for _, c := range cancelled {
		if at.Equal(c) {
			return true
		}
	}
	return false
}

// Sync merges one validated feed into the app database. New-meeting notifications are only
// generated for newly introduced UIDs after the first successful download.
func Sync(ctx context.Context, store *storage.Store, src *model.CalendarSource) ([]*model.Event, error) {
	events, err := Fetch(ctx, src.URL)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	known, err := store.KnownUIDs(src.ID)
	if err != nil {
		return nil, err
	}
	var added []*model.Event
	if !src.LastSync.IsZero() {
		for _, e := range events {
			if !known[e.UID] && e.RecurrenceID.IsZero() {
				added = append(added, e)
			}
		}
	}
	if err := store.ReplaceRemoteEvents(src.ID, events); err != nil {
		return nil, err
	}
	return added, nil
}
