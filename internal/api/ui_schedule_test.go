package api

import (
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
