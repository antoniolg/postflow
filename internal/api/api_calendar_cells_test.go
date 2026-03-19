package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
)

func TestCalendarCellsRenderAllEventsForDynamicOverflow(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	settingsForm := url.Values{}
	settingsForm.Set("timezone", "UTC")
	settingsReq := httptest.NewRequest(http.MethodPost, "/settings/timezone", bytes.NewBufferString(settingsForm.Encode()))
	settingsReq.Header.Set("content-type", "application/x-www-form-urlencoded")
	settingsW := httptest.NewRecorder()
	h.ServeHTTP(settingsW, settingsReq)
	if settingsW.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on timezone update, got %d", settingsW.Code)
	}

	selectedDay := time.Date(2026, time.February, 26, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		createBody, _ := json.Marshal(map[string]any{
			"account_id":   testAccountID(t, store),
			"text":         "dyn-overflow-" + strconv.Itoa(i+1),
			"scheduled_at": selectedDay.Add(time.Duration(9+i) * time.Hour).Format(time.RFC3339),
		})
		createReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createBody))
		createW := httptest.NewRecorder()
		h.ServeHTTP(createW, createReq)
		if createW.Code != http.StatusCreated {
			t.Fatalf("expected 201 for seed post %d, got %d", i+1, createW.Code)
		}
	}

	monthParam := selectedDay.Format("2006-01")
	dayParam := selectedDay.Format("2006-01-02")
	calendarReq := httptest.NewRequest(http.MethodGet, "/?view=calendar&month="+monthParam+"&day="+dayParam, nil)
	calendarW := httptest.NewRecorder()
	h.ServeHTTP(calendarW, calendarReq)
	if calendarW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", calendarW.Code)
	}

	body := calendarW.Body.String()
	selectedDayEventsRe := regexp.MustCompile(`(?s)<div class="day-cell[^"]*selected[^"]*">.*?<div class="day-events" data-day-events>(.*?)</div>\s*</a>`)
	selectedDayEventsMatch := selectedDayEventsRe.FindStringSubmatch(body)
	if len(selectedDayEventsMatch) != 2 {
		t.Fatalf("expected selected day events block in calendar grid")
	}
	selectedDayEvents := selectedDayEventsMatch[1]
	if got := strings.Count(selectedDayEvents, "dyn-overflow-"); got != 6 {
		t.Fatalf("expected all 6 selected-day events rendered for client-side fit, got %d", got)
	}
	if !strings.Contains(selectedDayEvents, "data-day-overflow hidden") {
		t.Fatalf("expected selected day overflow indicator placeholder for dynamic sizing")
	}
	if strings.Contains(body, "+3 more") {
		t.Fatalf("expected no server-side fixed +N more truncation")
	}
}

func TestCalendarDayEventShowsPlatformLogoOnRight(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	selectedDay := time.Date(2026, time.February, 28, 0, 0, 0, 0, time.UTC)
	createBody, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
		"text":         "calendar icon test",
		"scheduled_at": selectedDay.Add(10 * time.Hour).Format(time.RFC3339),
	})
	createReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createBody))
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for seed post, got %d", createW.Code)
	}

	monthParam := selectedDay.Format("2006-01")
	dayParam := selectedDay.Format("2006-01-02")
	calendarReq := httptest.NewRequest(http.MethodGet, "/?view=calendar&month="+monthParam+"&day="+dayParam, nil)
	calendarW := httptest.NewRecorder()
	h.ServeHTTP(calendarW, calendarReq)
	if calendarW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", calendarW.Code)
	}

	body := calendarW.Body.String()
	selectedDayEventsRe := regexp.MustCompile(`(?s)<div class="day-cell[^"]*selected[^"]*">.*?<div class="day-events" data-day-events>(.*?)</div>\s*</a>`)
	selectedDayEventsMatch := selectedDayEventsRe.FindStringSubmatch(body)
	if len(selectedDayEventsMatch) != 2 {
		t.Fatalf("expected selected day events block in calendar grid")
	}
	selectedDayEvents := selectedDayEventsMatch[1]
	if !strings.Contains(selectedDayEvents, `data-platform="x"`) {
		t.Fatalf("expected selected-day event to include platform marker")
	}
	if !strings.Contains(selectedDayEvents, `class="event-network"`) {
		t.Fatalf("expected selected-day event to render platform icon container")
	}
}

func TestCalendarDayEventCollapsesMultilinePreviewIntoSingleVisualLine(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	selectedDay := time.Date(2026, time.March, 18, 0, 0, 0, 0, time.UTC)
	createBody, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
		"text":         "La IA no hace...\n\nLo que hace es...",
		"scheduled_at": selectedDay.Add(16 * time.Hour).Format(time.RFC3339),
	})
	createReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createBody))
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for multiline seed post, got %d", createW.Code)
	}

	monthParam := selectedDay.Format("2006-01")
	dayParam := selectedDay.Format("2006-01-02")
	calendarReq := httptest.NewRequest(http.MethodGet, "/?view=calendar&month="+monthParam+"&day="+dayParam, nil)
	calendarW := httptest.NewRecorder()
	h.ServeHTTP(calendarW, calendarReq)
	if calendarW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", calendarW.Code)
	}

	body := calendarW.Body.String()
	selectedDayEventsRe := regexp.MustCompile(`(?s)<div class="day-cell[^"]*selected[^"]*">.*?<div class="day-events" data-day-events>(.*?)</div>\s*</a>`)
	selectedDayEventsMatch := selectedDayEventsRe.FindStringSubmatch(body)
	if len(selectedDayEventsMatch) != 2 {
		t.Fatalf("expected selected day events block in calendar grid")
	}
	selectedDayEvents := selectedDayEventsMatch[1]
	if strings.Contains(selectedDayEvents, "<br>") {
		t.Fatalf("expected calendar grid preview to collapse multiline copy without <br> tags")
	}
	if !strings.Contains(selectedDayEvents, "La IA no hace... Lo que hace es...") {
		t.Fatalf("expected calendar grid preview to collapse multiline copy into a single compact preview")
	}
}
