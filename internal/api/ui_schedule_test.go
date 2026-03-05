package api

import (
	"net/url"
	"testing"
	"time"
)

func TestDefaultCalendarCreateScheduledLocal(t *testing.T) {
	loc := time.UTC
	selectedDay := time.Date(2026, time.March, 12, 0, 0, 0, 0, loc)
	now := time.Date(2026, time.March, 3, 16, 24, 10, 0, loc)

	got := defaultCalendarCreateScheduledLocal(selectedDay, now)
	want := "2026-03-12T17:24"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestDefaultCalendarCreateScheduledLocalHandlesZeroInputs(t *testing.T) {
	got := defaultCalendarCreateScheduledLocal(time.Time{}, time.Now().UTC())
	if got != "" {
		t.Fatalf("expected empty result for zero selected day, got %q", got)
	}

	got = defaultCalendarCreateScheduledLocal(time.Now().UTC(), time.Time{})
	if got != "" {
		t.Fatalf("expected empty result for zero now, got %q", got)
	}
}

func TestCalendarSelectedDayFromCreateQuery(t *testing.T) {
	loc := time.UTC
	query := url.Values{
		"calendar_day": []string{"2026-03-12"},
		"return_to":    []string{"/?view=calendar&day=2026-03-01"},
	}

	day, ok := calendarSelectedDayFromCreateQuery(query, loc)
	if !ok {
		t.Fatalf("expected selected day from calendar_day query param")
	}
	if got := day.Format("2006-01-02"); got != "2026-03-12" {
		t.Fatalf("expected selected day 2026-03-12, got %q", got)
	}
}

func TestCalendarSelectedDayFromCreateQueryFallsBackToReturnTo(t *testing.T) {
	loc := time.UTC
	query := url.Values{
		"return_to": []string{"/?view=calendar&month=2026-03&day=2026-03-19"},
	}

	day, ok := calendarSelectedDayFromCreateQuery(query, loc)
	if !ok {
		t.Fatalf("expected selected day from return_to query param")
	}
	if got := day.Format("2006-01-02"); got != "2026-03-19" {
		t.Fatalf("expected selected day 2026-03-19, got %q", got)
	}
}
