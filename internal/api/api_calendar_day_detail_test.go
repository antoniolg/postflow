package api

import (
	"bytes"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
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

func TestCalendarDayDetailShowsMediaIndicatorWhenPostHasMedia(t *testing.T) {
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

	media, err := store.CreateMedia(t.Context(), domain.Media{
		Kind:         "image",
		OriginalName: "cover.png",
		StoragePath:  filepath.Join(tempDir, "cover.png"),
		MimeType:     "image/png",
		SizeBytes:    1234,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}

	selectedDay := time.Date(2026, time.February, 26, 0, 0, 0, 0, time.UTC)
	scheduledAt := selectedDay.Add(13*time.Hour + 24*time.Minute)
	payload, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
		"text":         "scheduled post with media indicator",
		"scheduled_at": scheduledAt.Format(time.RFC3339),
		"media_ids":    []string{media.ID},
	})
	createReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(payload))
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for scheduled post with media, got %d: %s", createW.Code, createW.Body.String())
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

	if !strings.Contains(panel, "day-item-media-indicator") {
		t.Fatalf("expected day panel to render media indicator for scheduled post with media")
	}
	if !strings.Contains(panel, "data-lucide=\"image\"") {
		t.Fatalf("expected day panel media indicator to use lucide image icon")
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
	if !strings.Contains(body, "data-view=\"calendar\"") {
		t.Fatalf("expected calendar to be the default active view")
	}
	if !strings.Contains(body, "<aside class=\"day-panel\" aria-label=\"Day detail\">") {
		t.Fatalf("expected day detail panel")
	}
	if !strings.Contains(body, "aria-label=\"Previous month\"") || !strings.Contains(body, "aria-label=\"Next month\"") || !strings.Contains(body, "href=\"/?view=calendar&month=") {
		t.Fatalf("expected calendar month controls in calendar header")
	}
	if !strings.Contains(body, "<a class=\"create-pill\" href=\"/?view=create") {
		t.Fatalf("expected create post button in calendar header")
	}
	if !strings.Contains(body, "data-day-events") || !strings.Contains(body, "data-day-overflow") {
		t.Fatalf("expected day cells to expose event and overflow markers")
	}
	hideNetworkRule := regexp.MustCompile(`(?s)body\[data-view="calendar"\]\s*\.event-network\s*\{[^}]*display\s*:\s*none`)
	if hideNetworkRule.MatchString(body) {
		t.Fatalf("did not expect calendar desktop css to hide event network icons")
	}
	fullWidthEventRule := regexp.MustCompile(`(?s)body\[data-view="calendar"\]\s*\.day-event\s*\{[^}]*width\s*:\s*100%`)
	if !fullWidthEventRule.MatchString(body) {
		t.Fatalf("expected calendar event cards to use full row width")
	}
}

func TestCalendarCreateKeepsSelectedDayOnBack(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	monthParam := "2026-02"
	dayParam := "2026-02-26"
	calendarReq := httptest.NewRequest(http.MethodGet, "/?view=calendar&month="+monthParam+"&day="+dayParam, nil)
	calendarW := httptest.NewRecorder()
	h.ServeHTTP(calendarW, calendarReq)
	if calendarW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", calendarW.Code)
	}

	calendarBody := calendarW.Body.String()
	createHrefMatch := regexp.MustCompile(`<a class="create-pill" href="([^"]+)"`).FindStringSubmatch(calendarBody)
	if len(createHrefMatch) != 2 {
		t.Fatalf("expected calendar create button in header")
	}
	createHref := html.UnescapeString(createHrefMatch[1])
	parsedCreateHref, err := url.Parse(createHref)
	if err != nil {
		t.Fatalf("parse create href: %v", err)
	}
	if got := parsedCreateHref.Query().Get("view"); got != "create" {
		t.Fatalf("expected create button to target create view, got %q", got)
	}
	returnTo := parsedCreateHref.Query().Get("return_to")
	expectedReturnTo := "/?view=calendar&month=" + monthParam + "&day=" + dayParam
	if returnTo != expectedReturnTo {
		t.Fatalf("expected return_to %q, got %q", expectedReturnTo, returnTo)
	}
	scheduledLocalRaw := parsedCreateHref.Query().Get("scheduled_at_local")
	if scheduledLocalRaw == "" {
		t.Fatalf("expected scheduled_at_local in calendar create href")
	}
	scheduledLocal, err := time.ParseInLocation("2006-01-02T15:04", scheduledLocalRaw, time.UTC)
	if err != nil {
		t.Fatalf("parse scheduled_at_local: %v", err)
	}
	if scheduledLocal.Format("2006-01-02") != dayParam {
		t.Fatalf("expected scheduled_at_local date %q, got %q", dayParam, scheduledLocal.Format("2006-01-02"))
	}

	createReq := httptest.NewRequest(http.MethodGet, createHref, nil)
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusOK {
		t.Fatalf("expected 200 for create view, got %d", createW.Code)
	}

	createBody := createW.Body.String()
	expectedBackHref := "<a class=\"title-back\" href=\"/?view=calendar&amp;month=" + monthParam + "&amp;day=" + dayParam + "\""
	if !strings.Contains(createBody, expectedBackHref) {
		t.Fatalf("expected create back button to return to selected calendar day")
	}
	if !strings.Contains(createBody, `id="create-scheduled-at" type="datetime-local" name="scheduled_at_local" data-date-picker value="`+scheduledLocalRaw+`"`) {
		t.Fatalf("expected create view to prefill scheduled_at_local with calendar default")
	}
}
