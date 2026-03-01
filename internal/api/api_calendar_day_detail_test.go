package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/publisher/internal/db"
)

func TestCalendarDayDetailShowsPendingBeforePublished(t *testing.T) {
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
	publishedAt := selectedDay.Add(9 * time.Hour)
	pendingAt := selectedDay.Add(10 * time.Hour)

	createPublishedBody, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
		"text":         "published item should be below",
		"scheduled_at": publishedAt.Format(time.RFC3339),
	})
	createPublishedReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createPublishedBody))
	createPublishedW := httptest.NewRecorder()
	h.ServeHTTP(createPublishedW, createPublishedReq)
	if createPublishedW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for published seed post, got %d", createPublishedW.Code)
	}
	var publishedResp map[string]any
	if err := json.Unmarshal(createPublishedW.Body.Bytes(), &publishedResp); err != nil {
		t.Fatalf("decode published seed response: %v", err)
	}
	publishedID, _ := publishedResp["id"].(string)
	if publishedID == "" {
		t.Fatalf("expected published post id")
	}
	if err := store.MarkPublished(t.Context(), publishedID, "x-published-seed"); err != nil {
		t.Fatalf("mark published: %v", err)
	}

	createPendingBody, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
		"text":         "pending item should be first",
		"scheduled_at": pendingAt.Format(time.RFC3339),
	})
	createPendingReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createPendingBody))
	createPendingW := httptest.NewRecorder()
	h.ServeHTTP(createPendingW, createPendingReq)
	if createPendingW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for pending post, got %d", createPendingW.Code)
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
	panelStart := strings.Index(body, "<aside class=\"day-panel\" aria-label=\"Day detail\">")
	if panelStart == -1 {
		t.Fatalf("expected day panel in calendar view")
	}
	panelEndRel := strings.Index(body[panelStart:], "</aside>")
	if panelEndRel == -1 {
		t.Fatalf("expected day panel closing tag")
	}
	panel := body[panelStart : panelStart+panelEndRel]

	if !strings.Contains(panel, "to publish (1)") {
		t.Fatalf("expected pending section header")
	}
	if !strings.Contains(panel, "published (1)") {
		t.Fatalf("expected published section header")
	}

	pendingIdx := strings.Index(panel, "pending item should be first")
	separatorIdx := strings.Index(panel, "class=\"day-separator\">published</div>")
	publishedIdx := strings.Index(panel, "published item should be below")
	if pendingIdx == -1 || separatorIdx == -1 || publishedIdx == -1 {
		t.Fatalf("expected pending item, separator, and published item in day panel")
	}
	if !(pendingIdx < separatorIdx && separatorIdx < publishedIdx) {
		t.Fatalf("expected pending section before separator and published section")
	}
}

func TestCalendarDayDetailDeleteButtonVisibilityByStatus(t *testing.T) {
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
	scheduledAt := selectedDay.Add(10 * time.Hour)
	publishedAt := selectedDay.Add(11 * time.Hour)
	accountID := testAccountID(t, store)

	scheduledPayload, _ := json.Marshal(map[string]any{
		"account_id":   accountID,
		"text":         "scheduled deletable on panel",
		"scheduled_at": scheduledAt.Format(time.RFC3339),
	})
	scheduledReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(scheduledPayload))
	scheduledW := httptest.NewRecorder()
	h.ServeHTTP(scheduledW, scheduledReq)
	if scheduledW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for scheduled post, got %d", scheduledW.Code)
	}
	var scheduledResp map[string]any
	if err := json.Unmarshal(scheduledW.Body.Bytes(), &scheduledResp); err != nil {
		t.Fatalf("decode scheduled response: %v", err)
	}
	scheduledID, _ := scheduledResp["id"].(string)
	if scheduledID == "" {
		t.Fatalf("expected scheduled post id")
	}

	publishedPayload, _ := json.Marshal(map[string]any{
		"account_id":   accountID,
		"text":         "published should hide delete",
		"scheduled_at": publishedAt.Format(time.RFC3339),
	})
	publishedReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(publishedPayload))
	publishedW := httptest.NewRecorder()
	h.ServeHTTP(publishedW, publishedReq)
	if publishedW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for published seed post, got %d", publishedW.Code)
	}
	var publishedResp map[string]any
	if err := json.Unmarshal(publishedW.Body.Bytes(), &publishedResp); err != nil {
		t.Fatalf("decode published response: %v", err)
	}
	publishedID, _ := publishedResp["id"].(string)
	if publishedID == "" {
		t.Fatalf("expected published post id")
	}
	if err := store.MarkPublished(t.Context(), publishedID, "x-published-seed"); err != nil {
		t.Fatalf("mark published: %v", err)
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
	panelStart := strings.Index(body, "<aside class=\"day-panel\" aria-label=\"Day detail\">")
	if panelStart == -1 {
		t.Fatalf("expected day panel in calendar view")
	}
	panelEndRel := strings.Index(body[panelStart:], "</aside>")
	if panelEndRel == -1 {
		t.Fatalf("expected day panel closing tag")
	}
	panel := body[panelStart : panelStart+panelEndRel]

	separatorIdx := strings.Index(panel, "class=\"day-separator\">published</div>")
	if separatorIdx == -1 {
		t.Fatalf("expected separator between pending and published sections")
	}
	pendingPanel := panel[:separatorIdx]
	publishedPanel := panel[separatorIdx:]

	if !strings.Contains(pendingPanel, "/posts/"+scheduledID+"/delete") {
		t.Fatalf("expected delete action for scheduled post in pending section")
	}
	if !strings.Contains(pendingPanel, "onsubmit=\"return confirm('Delete this publication?');\"") {
		t.Fatalf("expected delete action to require confirmation")
	}
	if strings.Contains(pendingPanel, "day-item-btn-del\" title=\"Delete\" disabled") {
		t.Fatalf("expected pending delete action to be enabled")
	}
	if strings.Contains(pendingPanel, "title=\"Edit\"") {
		t.Fatalf("did not expect explicit edit button in day panel items")
	}
	if strings.Contains(publishedPanel, "/posts/"+publishedID+"/delete") {
		t.Fatalf("did not expect delete action for published post")
	}
}

func TestDefaultViewIsCalendar(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, ">CALENDAR</h1>") {
		t.Fatalf("expected CALENDAR as default view title")
	}
	if !strings.Contains(body, "<a class=\"create-fab\" href=\"") {
		t.Fatalf("expected floating create action in non-create views")
	}
	if !strings.Contains(body, "<div class=\"calendar-layout\"") {
		t.Fatalf("expected calendar layout container")
	}
	if !strings.Contains(body, "<div class=\"calendar-wrap\"") {
		t.Fatalf("expected calendar container")
	}
	if !strings.Contains(body, "<aside class=\"day-panel\"") {
		t.Fatalf("expected day detail panel")
	}
	if !strings.Contains(body, "<div class=\"calendar-controls\">") {
		t.Fatalf("expected calendar month controls in calendar header")
	}
	if !strings.Contains(body, "syncDayPanelHeightToCalendar") {
		t.Fatalf("expected script hook for syncing calendar and day detail heights")
	}
}
