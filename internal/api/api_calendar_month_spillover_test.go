package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
)

func TestCalendarShowsVisibleSpilloverDayPublications(t *testing.T) {
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

	spilloverDay := time.Date(2026, time.February, 26, 0, 0, 0, 0, time.UTC)
	createBody, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
		"text":         "spillover edge post",
		"scheduled_at": spilloverDay.Add(10 * time.Hour).Format(time.RFC3339),
	})
	createReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createBody))
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for spillover seed post, got %d", createW.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/?view=calendar&month=2026-03&day=2026-02-26", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	selectedDayEventsRe := regexp.MustCompile(`(?s)<div class="day-cell[^"]*selected[^"]*">.*?<div class="day-events" data-day-events>(.*?)</div>\s*</a>`)
	selectedDayEventsMatch := selectedDayEventsRe.FindStringSubmatch(body)
	if len(selectedDayEventsMatch) != 2 {
		t.Fatalf("expected selected spillover day events block in calendar grid")
	}
	if !strings.Contains(selectedDayEventsMatch[1], "spillover edge post") {
		t.Fatalf("expected spillover day event to render inside the visible calendar grid")
	}

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
		t.Fatalf("expected spillover day panel to count the visible publication")
	}
	if !strings.Contains(panel, "spillover edge post") {
		t.Fatalf("expected spillover day panel to render the visible publication")
	}
}
